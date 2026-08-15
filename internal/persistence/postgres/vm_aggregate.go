package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

var (
	ErrVMAggregateConflict = errors.New("VM aggregate authority conflict")
	ErrNoVMAggregateWork   = errors.New("no VM aggregate lifecycle work available")
)

// VMAggregateCreateRequest is deliberately logical. It contains no Host,
// Admission, backend, Binding, LV, pCPU or other physical incarnation.
type VMAggregateCreateRequest struct {
	RequestID, OperationID, VMID, ProjectID, Name        string
	FlavorID, ImageID, AvailabilityPolicyID              string
	PlacementScopeID, RootVolumeID, DesiredPowerState    string
	FlavorRevision, ImageRevision                        uint64
	AvailabilityPolicyRevision, PlacementScopeGeneration uint64
	RootVolumeRevision                                   uint64
	DeleteProtection                                     bool
}

type VMAggregate struct {
	VMID, ProjectID, Name, DesiredPowerState, LifecycleState string
	ConvergenceState, OperationID, OperationState            string
	DependencySnapshotID, DependencyDigest, DesiredDigest    string
	RootVolumeID                                             string
	VMRevision, RuntimeIntentGeneration, RootVolumeRevision  uint64
}

type VMAggregateClaim struct {
	OperationID, Owner, ClaimMode, OperationState string
	OperationGeneration, ClaimGeneration          uint64
	LeaseExpiresAt                                time.Time
}

type VMAggregateVerification struct {
	VerificationID, VerificationDigest  string
	VMID, AdmissionID, HostID, PlanID   string
	VMRevision, RuntimeIntentGeneration uint64
	VMGeneration                        uint64
	VerificationState                   string
}

func digestVMAggregate(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validateVMAggregateCreate(r VMAggregateCreateRequest) error {
	if r.RequestID == "" || r.OperationID == "" || !vmUUIDPattern.MatchString(r.VMID) ||
		r.ProjectID == "" || r.Name == "" || len(r.Name) > 255 || r.FlavorID == "" ||
		r.ImageID == "" || r.AvailabilityPolicyID == "" || r.PlacementScopeID == "" ||
		r.RootVolumeID == "" || r.FlavorRevision == 0 || r.ImageRevision == 0 ||
		r.AvailabilityPolicyRevision == 0 || r.PlacementScopeGeneration == 0 ||
		r.RootVolumeRevision == 0 || r.DesiredPowerState != "RUNNING" {
		return ErrVMAggregateConflict
	}
	return nil
}

// CreateVMAggregate snapshots exact current logical dependencies and creates a
// first-class lifecycle Operation. The initial qualified profile is one ROOT
// Volume, zero Ports, no PCI, desired RUNNING.
func CreateVMAggregate(ctx context.Context, db TxBeginner, r VMAggregateCreateRequest) (VMAggregate, error) {
	if err := validateVMAggregateCreate(r); err != nil {
		return VMAggregate{}, err
	}
	requestDigest := digestVMAggregate(r)
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "vm-resource/"+r.VMID); err != nil {
			return err
		}
		var replayVM, replayOperation, replayDigest string
		if err := tx.QueryRow(ctx, `SELECT vm_id::text,operation_id,request_digest FROM kim.vm_lifecycle_operation_evidence WHERE request_id=$1`, r.RequestID).Scan(&replayVM, &replayOperation, &replayDigest); err == nil {
			if replayVM != r.VMID || replayOperation != r.OperationID || replayDigest != requestDigest {
				return ErrVMAggregateConflict
			}
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_resources_current WHERE vm_id=$1)`, r.VMID).Scan(&exists); err != nil || exists {
			return ErrVMAggregateConflict
		}

		var flavorOwner, flavorDigest string
		if err := tx.QueryRow(ctx, `SELECT e.owner_project_id,e.revision_digest FROM kim.flavors_current c JOIN kim.flavor_revision_evidence e ON(e.flavor_id,e.flavor_revision)=(c.flavor_id,c.flavor_revision) WHERE c.flavor_id=$1 AND c.flavor_revision=$2 AND c.lifecycle_state='ACTIVE' FOR SHARE OF c,e`, r.FlavorID, r.FlavorRevision).Scan(&flavorOwner, &flavorDigest); err != nil || flavorOwner != r.ProjectID {
			return ErrVMAggregateConflict
		}
		var imageOwner, imageVisibility, imageDigest string
		if err := tx.QueryRow(ctx, `SELECT e.owner_project_id,e.visibility,e.revision_digest FROM kim.images_current c JOIN kim.image_revision_evidence e ON(e.image_id,e.image_revision)=(c.image_id,c.image_revision) WHERE c.image_id=$1 AND c.image_revision=$2 AND c.lifecycle_state='ACTIVE' AND e.validation_state='VERIFIED' FOR SHARE OF c,e`, r.ImageID, r.ImageRevision).Scan(&imageOwner, &imageVisibility, &imageDigest); err != nil || (imageOwner != r.ProjectID && imageVisibility != "PUBLIC") {
			return ErrVMAggregateConflict
		}
		var policyDigest string
		if err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.availability_policies_current WHERE policy_id=$1 AND policy_revision=$2 AND lifecycle_state='ACTIVE' FOR SHARE`, r.AvailabilityPolicyID, r.AvailabilityPolicyRevision).Scan(&policyDigest); err != nil {
			return ErrVMAggregateConflict
		}
		var scopeProject, scopeDigest string
		if err := tx.QueryRow(ctx, `SELECT project_id,scope_digest FROM kim.placement_scopes_current WHERE placement_scope_id=$1 AND scope_generation=$2 AND consumer_type='VM_PLACEMENT' AND lifecycle_state='ACTIVE' FOR SHARE`, r.PlacementScopeID, r.PlacementScopeGeneration).Scan(&scopeProject, &scopeDigest); err != nil || scopeProject != r.ProjectID {
			return ErrVMAggregateConflict
		}
		var volumeProject, volumeDigest, volumeState string
		var bootable bool
		if err := tx.QueryRow(ctx, `SELECT v.project_id,v.desired_digest,v.lifecycle_state,v.bootable FROM kim.volumes_current v JOIN kim.volume_materializations_current m ON m.volume_id=v.volume_id AND m.volume_revision=v.volume_revision AND m.materialization_state='VERIFIED' AND m.terminal_evidence_id IS NOT NULL JOIN kim.volume_backend_bindings_current b ON b.binding_id=m.binding_id AND b.binding_generation=m.binding_generation AND b.binding_state='BOUND' WHERE v.volume_id=$1 AND v.volume_revision=$2 AND v.authority_source='VOLUME_RESOURCE' FOR UPDATE OF v`, r.RootVolumeID, r.RootVolumeRevision).Scan(&volumeProject, &volumeDigest, &volumeState, &bootable); err != nil || volumeProject != r.ProjectID || volumeState != "AVAILABLE" || !bootable {
			return ErrVMAggregateConflict
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.volume_attachment_intents_current WHERE volume_id=$1 AND intent_state IN('REQUESTED','ATTACHED'))`, r.RootVolumeID).Scan(&exists); err != nil || exists {
			return ErrVMAggregateConflict
		}

		vmRevision, runtimeGeneration := uint64(1), uint64(1)
		snapshotID := fmt.Sprintf("vm-dependencies:%s:%d", r.VMID, runtimeGeneration)
		attachmentIntentID := fmt.Sprintf("vm-volume-intent:%s:%d:root", r.VMID, runtimeGeneration)
		attachmentID := fmt.Sprintf("vm-volume-attachment:%s:%d:root", r.VMID, runtimeGeneration)
		dependencyPayload := map[string]any{
			"flavor":              map[string]any{"id": r.FlavorID, "revision": r.FlavorRevision, "digest": flavorDigest},
			"image":               map[string]any{"id": r.ImageID, "revision": r.ImageRevision, "digest": imageDigest},
			"availability_policy": map[string]any{"id": r.AvailabilityPolicyID, "revision": r.AvailabilityPolicyRevision, "digest": policyDigest},
			"placement_scope":     map[string]any{"id": r.PlacementScopeID, "generation": r.PlacementScopeGeneration, "digest": scopeDigest},
			"ports":               []any{},
			"volumes":             []any{map[string]any{"id": r.RootVolumeID, "revision": r.RootVolumeRevision, "role": "ROOT", "digest": volumeDigest, "attachment_intent_id": attachmentIntentID, "attachment_id": attachmentID}},
		}
		dependencyRaw, _ := json.Marshal(dependencyPayload)
		dependencyDigest := digestBytes(dependencyRaw)
		desiredDigest := digestVMAggregate(map[string]any{"request": r, "vm_revision": vmRevision, "lifecycle": "ACTIVE"})
		intentDigest := digestVMAggregate(map[string]any{"vm_id": r.VMID, "vm_revision": vmRevision, "runtime_generation": runtimeGeneration, "snapshot": snapshotID, "dependency_digest": dependencyDigest, "power": r.DesiredPowerState})
		operationDigest := digestVMAggregate(map[string]any{"operation_id": r.OperationID, "generation": 1, "request_digest": requestDigest, "vm_id": r.VMID, "intent_digest": intentDigest})

		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_revision_evidence(vm_id,vm_revision,project_id,vm_name,flavor_id,flavor_revision,image_id,image_revision,availability_policy_id,availability_policy_revision,placement_scope_id,placement_scope_generation,desired_power_state,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'ACTIVE',NULL,$14)`, r.VMID, r.ProjectID, r.Name, r.FlavorID, r.FlavorRevision, r.ImageID, r.ImageRevision, r.AvailabilityPolicyID, r.AvailabilityPolicyRevision, r.PlacementScopeID, r.PlacementScopeGeneration, r.DesiredPowerState, r.DeleteProtection, desiredDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_dependency_snapshot_evidence(dependency_snapshot_id,vm_id,vm_revision,runtime_intent_generation,flavor_id,flavor_revision,flavor_revision_digest,image_id,image_revision,image_revision_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,placement_scope_id,placement_scope_generation,placement_scope_digest,port_count,volume_count,dependency_payload,dependency_digest) VALUES($1,$2,1,1,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0,1,$15,$16)`, snapshotID, r.VMID, r.FlavorID, r.FlavorRevision, flavorDigest, r.ImageID, r.ImageRevision, imageDigest, r.AvailabilityPolicyID, r.AvailabilityPolicyRevision, policyDigest, r.PlacementScopeID, r.PlacementScopeGeneration, scopeDigest, dependencyRaw, dependencyDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_dependency_volume_evidence(dependency_snapshot_id,volume_ordinal,volume_id,volume_revision,device_role,desired_digest,attachment_intent_id,requested_attachment_id) VALUES($1,0,$2,$3,'ROOT',$4,$5,$6)`, snapshotID, r.RootVolumeID, r.RootVolumeRevision, volumeDigest, attachmentIntentID, attachmentID); err != nil {
			return err
		}
		intentEvidenceDigest := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%s/%s", attachmentIntentID, r.RootVolumeID, r.RootVolumeRevision, r.VMID, attachmentID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intent_evidence(attachment_intent_id,volume_id,volume_revision,attachment_generation,workload_id,requested_attachment_id,requested_physical_attachment_generation,intent_state,intent_digest) VALUES($1,$2,$3,1,$4,$5,1,'REQUESTED',$6)`, attachmentIntentID, r.RootVolumeID, r.RootVolumeRevision, r.VMID, attachmentID, intentEvidenceDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intents_current(volume_id,volume_revision,attachment_intent_id,attachment_generation,workload_id,requested_attachment_id,requested_physical_attachment_generation,intent_state) VALUES($1,$2,$3,1,$4,$5,1,'REQUESTED')`, r.RootVolumeID, r.RootVolumeRevision, attachmentIntentID, r.VMID, attachmentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation,vm_revision,dependency_snapshot_id,desired_power_state,intent_digest) VALUES($1,1,1,$2,$3,$4)`, r.VMID, snapshotID, r.DesiredPowerState, intentDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_operation_evidence(operation_id,operation_generation,request_id,request_digest,operation_kind,vm_id,vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,desired_power_state,operation_digest) VALUES($1,1,$2,$3,'CREATE',$4,1,1,$5,$6,$7,$8)`, r.OperationID, r.RequestID, requestDigest, r.VMID, snapshotID, dependencyDigest, r.DesiredPowerState, operationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_operations_current(operation_id,operation_generation,vm_id,vm_revision,runtime_intent_generation,operation_state) VALUES($1,1,$2,1,1,'PENDING')`, r.OperationID, r.VMID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.vm_resources_current(vm_id,vm_revision,runtime_intent_generation,project_id,vm_name,lifecycle_state,convergence_state,current_operation_id,desired_digest) VALUES($1,1,1,$2,$3,'CREATING','PENDING',$4,$5)`, r.VMID, r.ProjectID, r.Name, r.OperationID, desiredDigest)
		return err
	})
	if err != nil {
		return VMAggregate{}, err
	}
	return GetVMAggregate(ctx, db, r.VMID)
}

func GetVMAggregate(ctx context.Context, db TxBeginner, vmID string) (VMAggregate, error) {
	var v VMAggregate
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT c.vm_id::text,c.project_id,e.vm_name,e.desired_power_state,c.lifecycle_state,c.convergence_state,c.current_operation_id,o.operation_state,s.dependency_snapshot_id,s.dependency_digest,c.desired_digest,d.volume_id,c.vm_revision,c.runtime_intent_generation,d.volume_revision FROM kim.vm_resources_current c JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(c.vm_id,c.vm_revision) JOIN kim.vm_lifecycle_operations_current o ON o.operation_id=c.current_operation_id JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(c.vm_id,c.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id JOIN kim.vm_dependency_volume_evidence d ON d.dependency_snapshot_id=s.dependency_snapshot_id AND d.device_role='ROOT' WHERE c.vm_id=$1`, vmID).Scan(&v.VMID, &v.ProjectID, &v.Name, &v.DesiredPowerState, &v.LifecycleState, &v.ConvergenceState, &v.OperationID, &v.OperationState, &v.DependencySnapshotID, &v.DependencyDigest, &v.DesiredDigest, &v.RootVolumeID, &v.VMRevision, &v.RuntimeIntentGeneration, &v.RootVolumeRevision)
	})
	return v, err
}

func ClaimVMAggregateLifecycle(ctx context.Context, db TxBeginner, operationID, owner string, lease time.Duration) (VMAggregateClaim, error) {
	var c VMAggregateClaim
	if operationID == "" || owner == "" || lease <= 0 {
		return c, ErrVMAggregateConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state, response string
		var generation, last uint64
		if err := tx.QueryRow(ctx, `SELECT operation_generation,operation_state,last_claim_generation,COALESCE(response_state,'') FROM kim.vm_lifecycle_operations_current WHERE operation_id=$1 AND operation_state NOT IN('VERIFIED','FAILED') AND (claim_owner IS NULL OR claim_expires_at<=statement_timestamp()) FOR UPDATE`, operationID).Scan(&generation, &state, &last, &response); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoVMAggregateWork
		} else if err != nil {
			return err
		}
		mode := "APPLY_ALLOWED"
		if response == "LOST" || response == "UNKNOWN" || state == "UNKNOWN" {
			mode = "READ_BACK_FIRST"
		}
		next := last + 1
		if err := tx.QueryRow(ctx, `UPDATE kim.vm_lifecycle_operations_current SET last_claim_generation=$2,claim_owner=$3,claim_generation=$2,claim_expires_at=statement_timestamp()+($4::bigint*interval '1 microsecond'),updated_at=statement_timestamp() WHERE operation_id=$1 RETURNING claim_expires_at`, operationID, next, owner, lease.Microseconds()).Scan(&c.LeaseExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_attempt_evidence(operation_id,operation_generation,claim_generation,claim_owner,claim_mode,operation_state,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, operationID, generation, next, owner, mode, state, c.LeaseExpiresAt); err != nil {
			return err
		}
		c.OperationID, c.Owner, c.ClaimMode, c.OperationState = operationID, owner, mode, state
		c.OperationGeneration, c.ClaimGeneration = generation, next
		return nil
	})
	return c, err
}

func lockVMAggregateClaim(ctx context.Context, tx pgx.Tx, c VMAggregateClaim) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_lifecycle_operations_current WHERE operation_id=$1 AND operation_generation=$2 AND claim_owner=$3 AND claim_generation=$4 AND claim_expires_at>statement_timestamp() AND operation_state NOT IN('VERIFIED','FAILED'))`, c.OperationID, c.OperationGeneration, c.Owner, c.ClaimGeneration).Scan(&ok); err != nil || !ok {
		return ErrVMAggregateConflict
	}
	return nil
}

// CompileVMAggregatePlacement re-derives the ordinary Placement request from
// the immutable snapshot. It fails closed on any current logical or backend
// incarnation drift and never accepts a caller-selected Host.
func CompileVMAggregatePlacement(ctx context.Context, db TxBeginner, c VMAggregateClaim) (PlacementAdmissionRequest, error) {
	var out PlacementAdmissionRequest
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, c); err != nil {
			return err
		}
		var vmID, projectID, flavorID, imageID, scopeID, dependencyDigest string
		var vmRevision, runtimeGeneration, flavorRevision, imageRevision, scopeGeneration uint64
		var portCount, volumeCount int
		if err := tx.QueryRow(ctx, `SELECT o.vm_id::text,o.vm_revision,o.runtime_intent_generation,e.project_id,s.flavor_id,s.flavor_revision,s.image_id,s.image_revision,s.placement_scope_id,s.placement_scope_generation,s.dependency_digest,s.port_count,s.volume_count FROM kim.vm_lifecycle_operation_evidence o JOIN kim.vm_lifecycle_operations_current operation ON operation.operation_id=o.operation_id AND operation.operation_generation=o.operation_generation AND operation.operation_state='PENDING' JOIN kim.vm_resources_current current ON current.vm_id=o.vm_id AND current.vm_revision=o.vm_revision AND current.runtime_intent_generation=o.runtime_intent_generation AND current.current_operation_id=o.operation_id JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(o.vm_id,o.vm_revision) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=o.dependency_snapshot_id AND s.dependency_digest=o.dependency_digest WHERE o.operation_id=$1 AND o.operation_generation=$2`, c.OperationID, c.OperationGeneration).Scan(&vmID, &vmRevision, &runtimeGeneration, &projectID, &flavorID, &flavorRevision, &imageID, &imageRevision, &scopeID, &scopeGeneration, &dependencyDigest, &portCount, &volumeCount); err != nil || portCount != 0 || volumeCount != 1 {
			return ErrVMAggregateConflict
		}
		var current bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.flavors_current WHERE flavor_id=$1 AND flavor_revision=$2 AND lifecycle_state='ACTIVE') AND EXISTS(SELECT 1 FROM kim.images_current WHERE image_id=$3 AND image_revision=$4 AND lifecycle_state='ACTIVE') AND EXISTS(SELECT 1 FROM kim.placement_scopes_current WHERE placement_scope_id=$5 AND scope_generation=$6 AND lifecycle_state='ACTIVE')`, flavorID, flavorRevision, imageID, imageRevision, scopeID, scopeGeneration).Scan(&current); err != nil || !current {
			return ErrVMAggregateConflict
		}
		var storage placement.StorageRequirement
		if err := tx.QueryRow(ctx, `SELECT v.volume_id,v.volume_revision,a.requested_attachment_id,a.requested_physical_attachment_generation,a.attachment_intent_id,a.attachment_generation,c.allocation_decision_id,c.allocation_generation,i.backend_id,b.backend_generation,i.vg_uuid,v.storage_class_id,v.storage_class_revision,c.capacity_generation,class.fencing_policy_revision,v.size_bytes,v.access_mode,v.bootable FROM kim.vm_dependency_volume_evidence d JOIN kim.volumes_current v ON v.volume_id=d.volume_id AND v.volume_revision=d.volume_revision AND v.desired_digest=d.desired_digest AND v.authority_source='VOLUME_RESOURCE' AND v.lifecycle_state='AVAILABLE' JOIN kim.volume_attachment_intents_current a ON a.volume_id=v.volume_id AND a.attachment_intent_id=d.attachment_intent_id AND a.requested_attachment_id=d.requested_attachment_id AND a.workload_id=$2 AND a.intent_state='REQUESTED' JOIN kim.volume_materializations_current m ON m.volume_id=v.volume_id AND m.volume_revision=v.volume_revision AND m.materialization_state='VERIFIED' AND m.terminal_evidence_id IS NOT NULL JOIN kim.volume_backend_binding_intents i ON i.binding_id=m.binding_id AND i.binding_generation=m.binding_generation AND i.authority_source='VOLUME_RESOURCE' JOIN kim.volume_backend_bindings_current bound ON bound.binding_id=i.binding_id AND bound.binding_generation=i.binding_generation AND bound.binding_state='BOUND' JOIN kim.storage_capacity_claims c ON c.volume_id=v.volume_id AND c.allocation_decision_id=i.capacity_allocation_id AND c.allocation_generation=i.capacity_allocation_generation AND c.authority_source='VOLUME_RESOURCE' AND c.claim_state IN('RESERVED','ALLOCATED') JOIN kim.storage_backends_current b ON b.backend_id=i.backend_id AND b.host_id=i.host_id AND b.vg_uuid=i.vg_uuid AND b.lifecycle_state='ACTIVE' JOIN kim.storage_classes_current sc ON sc.storage_class_id=v.storage_class_id AND sc.class_revision=v.storage_class_revision AND sc.lifecycle_state='ACTIVE' JOIN kim.storage_class_revision_evidence class ON(class.storage_class_id,class.class_revision)=(sc.storage_class_id,sc.class_revision) WHERE d.dependency_snapshot_id=(SELECT dependency_snapshot_id FROM kim.vm_lifecycle_operation_evidence WHERE operation_id=$1 AND operation_generation=$3) AND d.device_role='ROOT'`, c.OperationID, vmID, c.OperationGeneration).Scan(&storage.VolumeID, &storage.VolumeRevision, &storage.AttachmentID, &storage.AttachmentGeneration, &storage.AttachmentIntentID, &storage.AttachmentIntentGeneration, &storage.CapacityAllocationID, &storage.CapacityAllocationGeneration, &storage.BackendID, &storage.BackendGeneration, &storage.VGUUID, &storage.StorageClassID, &storage.StorageClassRevision, &storage.CapacityGeneration, &storage.FencingPolicyRevision, &storage.SizeBytes, &storage.AccessMode, &storage.Bootable); err != nil {
			return ErrVMAggregateConflict
		}
		out = PlacementAdmissionRequest{RequestID: "vm-placement:" + c.OperationID + ":1", ProjectID: projectID, WorkloadID: vmID, ImageID: imageID, FlavorID: flavorID, PlacementScopeID: scopeID, Storage: []placement.StorageRequirement{storage}}
		_ = vmRevision
		_ = runtimeGeneration
		_ = dependencyDigest
		return nil
	})
	return out, err
}

// BindVMAggregateAdmission consumes only an already accepted ordinary
// Availability-aware Final Admission and binds its exact immutable provenance
// to the aggregate Operation.
func BindVMAggregateAdmission(ctx context.Context, db TxBeginner, c VMAggregateClaim, admissionID string) (string, error) {
	if admissionID == "" {
		return "", ErrVMAggregateConflict
	}
	bindingID := "vm-admission-binding:" + c.OperationID
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, c); err != nil {
			return err
		}
		var vmID, dependencyID, dependencyDigest, expectedPolicyID, expectedPolicyDigest, expectedScopeID string
		var vmRevision, runtimeGeneration, expectedPolicyRevision, expectedScopeGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT o.vm_id::text,o.vm_revision,o.runtime_intent_generation,o.dependency_snapshot_id,o.dependency_digest,s.availability_policy_id,s.availability_policy_revision,s.availability_policy_digest,s.placement_scope_id,s.placement_scope_generation FROM kim.vm_lifecycle_operation_evidence o JOIN kim.vm_lifecycle_operations_current current ON current.operation_id=o.operation_id AND current.operation_generation=o.operation_generation AND current.operation_state='PENDING' JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=o.dependency_snapshot_id WHERE o.operation_id=$1 AND o.operation_generation=$2`, c.OperationID, c.OperationGeneration).Scan(&vmID, &vmRevision, &runtimeGeneration, &dependencyID, &dependencyDigest, &expectedPolicyID, &expectedPolicyRevision, &expectedPolicyDigest, &expectedScopeID, &expectedScopeGeneration); err != nil {
			return ErrVMAggregateConflict
		}
		var workloadID, hostID, scopeID, scopeDigest, decision string
		var scopeGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT workload_id,host_id,placement_scope_id,placement_scope_generation,placement_scope_digest,decision_state FROM kim.placement_admission_decisions WHERE admission_id=$1 FOR SHARE`, admissionID).Scan(&workloadID, &hostID, &scopeID, &scopeGeneration, &scopeDigest, &decision); err != nil || workloadID != vmID || decision != "ACCEPTED" || scopeID != expectedScopeID || scopeGeneration != expectedScopeGeneration {
			return ErrVMAggregateConflict
		}
		var policyID, policyDigest string
		var policyRevision uint64
		if err := tx.QueryRow(ctx, `SELECT e.availability_policy_id,e.availability_policy_revision,e.availability_policy_digest FROM kim.vm_availability_binding_evidence e JOIN kim.vm_availability_bindings_current current ON(current.workload_id,current.binding_revision,current.binding_digest)=(e.workload_id,e.binding_revision,e.binding_digest) WHERE e.admission_id=$1`, admissionID).Scan(&policyID, &policyRevision, &policyDigest); err != nil || policyID != expectedPolicyID || policyRevision != expectedPolicyRevision || policyDigest != expectedPolicyDigest {
			return ErrVMAggregateConflict
		}
		var exactRoot bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_dependency_volume_evidence d JOIN kim.volume_attachment_intent_evidence requested ON requested.attachment_intent_id=d.attachment_intent_id AND requested.volume_id=d.volume_id AND requested.volume_revision=d.volume_revision AND requested.requested_attachment_id=d.requested_attachment_id AND requested.workload_id=$2 AND requested.intent_state='REQUESTED' JOIN kim.volume_attachment_intents_current a ON a.volume_id=d.volume_id AND a.volume_revision=d.volume_revision AND a.requested_attachment_id=d.requested_attachment_id AND a.workload_id=$2 AND a.intent_state='ATTACHED' JOIN kim.volume_attachment_intent_evidence e ON e.attachment_intent_id=a.attachment_intent_id AND e.placement_admission_id=$3 AND e.binding_id IS NOT NULL WHERE d.dependency_snapshot_id=$1 AND d.device_role='ROOT')`, dependencyID, vmID, admissionID).Scan(&exactRoot); err != nil || !exactRoot {
			return ErrVMAggregateConflict
		}
		digest := digestVMAggregate(map[string]any{"operation_id": c.OperationID, "generation": c.OperationGeneration, "vm_id": vmID, "vm_revision": vmRevision, "runtime_generation": runtimeGeneration, "dependency_digest": dependencyDigest, "admission_id": admissionID, "host_id": hostID, "scope_id": scopeID, "scope_generation": scopeGeneration, "scope_digest": scopeDigest})
		tag, err := tx.Exec(ctx, `INSERT INTO kim.vm_aggregate_admission_binding_evidence(binding_evidence_id,operation_id,operation_generation,vm_id,vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,admission_id,host_id,placement_scope_id,placement_scope_generation,placement_scope_digest,admission_binding_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(binding_evidence_id) DO NOTHING`, bindingID, c.OperationID, c.OperationGeneration, vmID, vmRevision, runtimeGeneration, dependencyID, dependencyDigest, admissionID, hostID, scopeID, scopeGeneration, scopeDigest, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var existingAdmission, existingDigest string
			if err := tx.QueryRow(ctx, `SELECT admission_id,admission_binding_digest FROM kim.vm_aggregate_admission_binding_evidence WHERE binding_evidence_id=$1`, bindingID).Scan(&existingAdmission, &existingDigest); err != nil || existingAdmission != admissionID || existingDigest != digest {
				return ErrVMAggregateConflict
			}
		}
		tag, err = tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET operation_state='MATERIALIZING',admission_id=$2,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state='RECEIVED',updated_at=statement_timestamp() WHERE operation_id=$1 AND operation_state='PENDING' AND claim_owner=$3 AND claim_generation=$4`, c.OperationID, admissionID, c.Owner, c.ClaimGeneration)
		if err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	return bindingID, err
}

// PrepareVMAggregateMaterialization delegates to the generic VM
// materialization primitive and records an exact aggregate binding. It creates
// no Recovery or EVACUATE identity.
func PrepareVMAggregateMaterialization(ctx context.Context, db TxBeginner, c VMAggregateClaim) (VMMaterializationDecision, error) {
	var decision VMMaterializationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, c); err != nil {
			return err
		}
		var vmID, admissionID, hostID, admissionBindingID string
		if err := tx.QueryRow(ctx, `SELECT o.vm_id::text,current.admission_id,b.host_id,b.binding_evidence_id FROM kim.vm_lifecycle_operation_evidence o JOIN kim.vm_lifecycle_operations_current current ON current.operation_id=o.operation_id JOIN kim.vm_aggregate_admission_binding_evidence b ON b.operation_id=o.operation_id AND b.operation_generation=o.operation_generation AND b.admission_id=current.admission_id WHERE o.operation_id=$1 AND o.operation_generation=$2 AND current.operation_state='MATERIALIZING'`, c.OperationID, c.OperationGeneration).Scan(&vmID, &admissionID, &hostID, &admissionBindingID); err != nil {
			return ErrVMAggregateConflict
		}
		planID := "vm-plan:" + c.OperationID + ":1"
		jobID := "vm-define-job:" + c.OperationID + ":1"
		commandID := "vm-define-command:" + c.OperationID + ":1"
		var err error
		decision, err = PrepareVMMaterialization(ctx, scopeTxBeginner{tx}, VMMaterializationRequest{VMID: vmID, AdmissionID: admissionID, PlanID: planID, JobID: jobID, CommandID: commandID, MaterializationGeneration: 1})
		if err != nil || decision.HostID != hostID {
			return ErrVMAggregateConflict
		}
		bindingID := "vm-materialization-binding:" + c.OperationID
		digest := digestVMAggregate(map[string]any{"operation_id": c.OperationID, "generation": c.OperationGeneration, "admission_binding": admissionBindingID, "vm_id": vmID, "vm_generation": 1, "admission_id": admissionID, "host_id": hostID, "plan_id": decision.PlanID, "plan_digest": decision.PlanDigest, "materialization_generation": 1})
		tag, err := tx.Exec(ctx, `INSERT INTO kim.vm_aggregate_materialization_binding_evidence(binding_evidence_id,operation_id,operation_generation,admission_binding_evidence_id,vm_id,vm_generation,admission_id,host_id,plan_id,plan_digest,materialization_generation,materialization_binding_digest) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,$9,1,$10) ON CONFLICT(binding_evidence_id) DO NOTHING`, bindingID, c.OperationID, c.OperationGeneration, admissionBindingID, vmID, admissionID, hostID, decision.PlanID, decision.PlanDigest, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var plan, oldDigest string
			if err := tx.QueryRow(ctx, `SELECT plan_id,materialization_binding_digest FROM kim.vm_aggregate_materialization_binding_evidence WHERE binding_evidence_id=$1`, bindingID).Scan(&plan, &oldDigest); err != nil || plan != decision.PlanID || oldDigest != digest {
				return ErrVMAggregateConflict
			}
		}
		tag, err = tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET plan_id=$2,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state='RECEIVED',updated_at=statement_timestamp() WHERE operation_id=$1 AND claim_owner=$3 AND claim_generation=$4`, c.OperationID, decision.PlanID, c.Owner, c.ClaimGeneration)
		if err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	return decision, err
}

// EvaluateVMAggregateEvidence is a DB-derived evidence consumer. Command
// success is insufficient: exact READY and RUNNING immutable read-back rows are
// mandatory. The function creates only immutable verification evidence.
func EvaluateVMAggregateEvidence(ctx context.Context, db TxBeginner, c VMAggregateClaim, verificationID string) (VMAggregateVerification, error) {
	var out VMAggregateVerification
	if verificationID == "" {
		return out, ErrVMAggregateConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, c); err != nil {
			return err
		}
		var vmID, dependencyID, dependencyDigest, admissionBindingID, materializationBindingID string
		var admissionID, hostID, planID, planDigest, definitionEvidence, imageEvidence, networkDigest, powerEvidence string
		var desiredPower, observedPower, convergence, bootReadiness, domainState, imageState, networkState, storageState string
		var vmRevision, runtimeGeneration, vmGeneration, readinessGeneration, powerGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT o.vm_id::text,o.vm_revision,o.runtime_intent_generation,o.dependency_snapshot_id,o.dependency_digest,ab.binding_evidence_id,mb.binding_evidence_id,ab.admission_id,ab.host_id,mb.plan_id,mb.plan_digest,mb.vm_generation,ready.observation_generation,ready.definition_evidence_id,COALESCE(ready.image_evidence_id,''),COALESCE(ready.network_evidence_set_digest,''),ready.boot_readiness,ready.domain_state,ready.image_state,ready.network_state,ready.storage_state,power.observation_generation,power.evidence_id,power.desired_power_state,power.observed_power_state,power.convergence_state FROM kim.vm_lifecycle_operation_evidence o JOIN kim.vm_lifecycle_operations_current operation ON operation.operation_id=o.operation_id AND operation.operation_generation=o.operation_generation AND operation.operation_state='MATERIALIZING' JOIN kim.vm_resources_current resource ON resource.vm_id=o.vm_id AND resource.vm_revision=o.vm_revision AND resource.runtime_intent_generation=o.runtime_intent_generation AND resource.current_operation_id=o.operation_id JOIN kim.vm_aggregate_admission_binding_evidence ab ON ab.operation_id=o.operation_id AND ab.operation_generation=o.operation_generation JOIN kim.vm_aggregate_materialization_binding_evidence mb ON mb.operation_id=o.operation_id AND mb.operation_generation=o.operation_generation AND mb.admission_binding_evidence_id=ab.binding_evidence_id JOIN kim.virtual_machines_current vm ON vm.vm_id=o.vm_id AND vm.placement_admission_id=ab.admission_id AND vm.host_id=ab.host_id AND vm.vm_generation=mb.vm_generation AND vm.current_plan_id=mb.plan_id JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.plan_id=mb.plan_id JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation WHERE o.operation_id=$1 AND o.operation_generation=$2`, c.OperationID, c.OperationGeneration).Scan(&vmID, &vmRevision, &runtimeGeneration, &dependencyID, &dependencyDigest, &admissionBindingID, &materializationBindingID, &admissionID, &hostID, &planID, &planDigest, &vmGeneration, &readinessGeneration, &definitionEvidence, &imageEvidence, &networkDigest, &bootReadiness, &domainState, &imageState, &networkState, &storageState, &powerGeneration, &powerEvidence, &desiredPower, &observedPower, &convergence); err != nil {
			return ErrVMAggregateConflict
		}
		if bootReadiness != "READY" || domainState != "DEFINED" || imageState != "REALIZED" || networkState != "REALIZED" || storageState != "BOUND" || imageEvidence == "" || networkDigest != digestBytes([]byte("[]")) || desiredPower != "RUNNING" || observedPower != "RUNNING" || convergence != "MATCHED" {
			return ErrVMAggregateConflict
		}
		var exactRoot bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_dependency_volume_evidence d JOIN kim.volume_attachment_intent_evidence requested ON requested.attachment_intent_id=d.attachment_intent_id AND requested.volume_id=d.volume_id AND requested.volume_revision=d.volume_revision AND requested.requested_attachment_id=d.requested_attachment_id AND requested.intent_state='REQUESTED' JOIN kim.volume_attachment_intents_current intent ON intent.volume_id=d.volume_id AND intent.volume_revision=d.volume_revision AND intent.requested_attachment_id=d.requested_attachment_id AND intent.workload_id=requested.workload_id AND intent.intent_state='ATTACHED' JOIN kim.volume_attachment_intent_evidence ie ON ie.attachment_intent_id=intent.attachment_intent_id AND ie.placement_admission_id=$2 JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=ie.binding_id AND binding.binding_generation=ie.binding_generation AND binding.host_id=$3 AND binding.binding_state='BOUND' JOIN kim.volume_materializations_current materialized ON materialized.volume_id=d.volume_id AND materialized.volume_revision=d.volume_revision AND materialized.binding_id=binding.binding_id AND materialized.binding_generation=binding.binding_generation AND materialized.materialization_state='VERIFIED' WHERE d.dependency_snapshot_id=$1 AND d.device_role='ROOT')`, dependencyID, admissionID, hostID).Scan(&exactRoot); err != nil || !exactRoot {
			return ErrVMAggregateConflict
		}
		digest := digestVMAggregate(map[string]any{"verification_id": verificationID, "operation_id": c.OperationID, "generation": c.OperationGeneration, "vm_id": vmID, "vm_revision": vmRevision, "runtime_generation": runtimeGeneration, "dependency_digest": dependencyDigest, "admission_binding": admissionBindingID, "materialization_binding": materializationBindingID, "admission_id": admissionID, "host_id": hostID, "vm_generation": vmGeneration, "plan_id": planID, "plan_digest": planDigest, "readiness_generation": readinessGeneration, "definition_evidence": definitionEvidence, "image_evidence": imageEvidence, "network_digest": networkDigest, "power_generation": powerGeneration, "power_evidence": powerEvidence})
		tag, err := tx.Exec(ctx, `INSERT INTO kim.vm_aggregate_verification_evidence(verification_id,operation_id,operation_generation,vm_id,vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,admission_binding_evidence_id,materialization_binding_evidence_id,admission_id,host_id,vm_generation,plan_id,plan_digest,readiness_observation_generation,definition_evidence_id,image_evidence_id,network_evidence_set_digest,power_observation_generation,power_evidence_id,desired_power_state,observed_power_state,verification_state,verification_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,'RUNNING','RUNNING','VERIFIED',$22) ON CONFLICT(verification_id) DO NOTHING`, verificationID, c.OperationID, c.OperationGeneration, vmID, vmRevision, runtimeGeneration, dependencyID, dependencyDigest, admissionBindingID, materializationBindingID, admissionID, hostID, vmGeneration, planID, planDigest, readinessGeneration, definitionEvidence, imageEvidence, networkDigest, powerGeneration, powerEvidence, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var oldOperation, oldDigest string
			if err := tx.QueryRow(ctx, `SELECT operation_id,verification_digest FROM kim.vm_aggregate_verification_evidence WHERE verification_id=$1`, verificationID).Scan(&oldOperation, &oldDigest); err != nil || oldOperation != c.OperationID || oldDigest != digest {
				return ErrVMAggregateConflict
			}
		}
		out = VMAggregateVerification{VerificationID: verificationID, VerificationDigest: digest, VMID: vmID, AdmissionID: admissionID, HostID: hostID, PlanID: planID, VMRevision: vmRevision, RuntimeIntentGeneration: runtimeGeneration, VMGeneration: vmGeneration, VerificationState: "VERIFIED"}
		return nil
	})
	return out, err
}

// CompleteVMAggregateLifecycle performs terminal-time drift fencing before
// publishing the immutable terminal and the rebuildable runtime projection.
func CompleteVMAggregateLifecycle(ctx context.Context, db TxBeginner, c VMAggregateClaim, verificationID, terminalID string) (string, error) {
	if verificationID == "" || terminalID == "" {
		return "", ErrVMAggregateConflict
	}
	var replayVerification string
	if err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT verification_id FROM kim.vm_aggregate_terminal_evidence WHERE terminal_evidence_id=$1 AND operation_id=$2 AND operation_generation=$3`, terminalID, c.OperationID, c.OperationGeneration).Scan(&replayVerification)
	}); err == nil {
		if replayVerification != verificationID {
			return "", ErrVMAggregateConflict
		}
		return terminalID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, c); err != nil {
			return err
		}
		var vmID, admissionID, hostID, planID, planDigest, verificationDigest string
		var vmRevision, runtimeGeneration, vmGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT v.vm_id::text,v.vm_revision,v.runtime_intent_generation,v.admission_id,v.host_id,v.vm_generation,v.plan_id,v.plan_digest,v.verification_digest FROM kim.vm_aggregate_verification_evidence v JOIN kim.vm_lifecycle_operations_current o ON o.operation_id=v.operation_id AND o.operation_generation=v.operation_generation JOIN kim.virtual_machines_current vm ON vm.vm_id=v.vm_id AND vm.vm_generation=v.vm_generation AND vm.placement_admission_id=v.admission_id AND vm.host_id=v.host_id AND vm.current_plan_id=v.plan_id JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=v.vm_id AND ready.vm_generation=v.vm_generation AND ready.plan_id=v.plan_id AND ready.observation_generation=v.readiness_observation_generation AND ready.definition_evidence_id=v.definition_evidence_id AND ready.image_evidence_id=v.image_evidence_id AND ready.network_evidence_set_digest=v.network_evidence_set_digest AND ready.boot_readiness='READY' JOIN kim.vm_power_state_current power ON power.vm_id=v.vm_id AND power.vm_generation=v.vm_generation AND power.observation_generation=v.power_observation_generation AND power.evidence_id=v.power_evidence_id AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED' WHERE v.verification_id=$1 AND v.operation_id=$2 AND v.operation_generation=$3 AND v.verification_state='VERIFIED' FOR UPDATE OF o,vm,ready,power`, verificationID, c.OperationID, c.OperationGeneration).Scan(&vmID, &vmRevision, &runtimeGeneration, &admissionID, &hostID, &vmGeneration, &planID, &planDigest, &verificationDigest); err != nil {
			return ErrVMAggregateConflict
		}
		terminalDigest := digestVMAggregate(map[string]any{"terminal_id": terminalID, "operation_id": c.OperationID, "generation": c.OperationGeneration, "verification_id": verificationID, "verification_digest": verificationDigest, "state": "VERIFIED"})
		tag, err := tx.Exec(ctx, `INSERT INTO kim.vm_aggregate_terminal_evidence(terminal_evidence_id,operation_id,operation_generation,verification_id,verification_digest,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,'VERIFIED',$6) ON CONFLICT(terminal_evidence_id) DO NOTHING`, terminalID, c.OperationID, c.OperationGeneration, verificationID, verificationDigest, terminalDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var oldVerification, oldDigest string
			if err := tx.QueryRow(ctx, `SELECT verification_id,terminal_digest FROM kim.vm_aggregate_terminal_evidence WHERE terminal_evidence_id=$1`, terminalID).Scan(&oldVerification, &oldDigest); err != nil || oldVerification != verificationID || oldDigest != terminalDigest {
				return ErrVMAggregateConflict
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_runtime_bindings_current(vm_id,vm_revision,runtime_intent_generation,admission_id,host_id,vm_generation,plan_id,materialization_generation,verification_id,terminal_evidence_id) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9) ON CONFLICT(vm_id) DO UPDATE SET vm_revision=EXCLUDED.vm_revision,runtime_intent_generation=EXCLUDED.runtime_intent_generation,admission_id=EXCLUDED.admission_id,host_id=EXCLUDED.host_id,vm_generation=EXCLUDED.vm_generation,plan_id=EXCLUDED.plan_id,materialization_generation=EXCLUDED.materialization_generation,verification_id=EXCLUDED.verification_id,terminal_evidence_id=EXCLUDED.terminal_evidence_id,updated_at=statement_timestamp()`, vmID, vmRevision, runtimeGeneration, admissionID, hostID, vmGeneration, planID, verificationID, terminalID); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resources_current SET lifecycle_state='ACTIVE',convergence_state='CONVERGED',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$2 AND runtime_intent_generation=$3 AND current_operation_id=$4`, vmID, vmRevision, runtimeGeneration, c.OperationID); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET operation_state='VERIFIED',terminal_evidence_id=$2,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state='RECEIVED',updated_at=statement_timestamp() WHERE operation_id=$1 AND claim_owner=$3 AND claim_generation=$4`, c.OperationID, terminalID, c.Owner, c.ClaimGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	return terminalID, err
}
