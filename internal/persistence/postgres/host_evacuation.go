package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrHostEvacuationConflict        = errors.New("Host evacuation authority conflict")
	ErrHostEvacuationStale           = errors.New("Host evacuation authority is stale")
	ErrHostEvacuationBlocked         = errors.New("Host evacuation is blocked")
	ErrHostEvacuationBudgetExhausted = errors.New("Host evacuation concurrency budget exhausted")
)

type HostEvacuationRequest struct {
	OperationID, SourceHostID, DrainPolicyID, Reason, RequestedBy string
	EvacuationGeneration, SourceHostAuthorityGeneration           uint64
	DrainPolicyRevision, EvacuationPolicyRevision                 uint64
	MaximumConcurrentWorkloads                                    int
}

type HostEvacuationOperation struct {
	OperationID, SourceHostID, PlacementPoolID, DrainID, WorkloadSetID string
	WorkloadSetDigest, LifecycleState, OperationDigest                 string
	EvacuationGeneration, SourceHostAuthorityGeneration                uint64
	PlacementPoolGeneration, MembershipGeneration                      uint64
	WorkloadSetGeneration, StateGeneration                             uint64
	MaximumConcurrentWorkloads, WorkloadCount                          int
}

type HostEvacuationWorkload struct {
	ChildOperationID, VMID, WorkloadID, SourceHostID, SourceAdmissionID string
	SourcePlanID, SourcePlanDigest, SnapshotDigest, Phase, ResultState  string
	ReasonCode, DestinationHostID, DestinationAdmissionID               string
	ChildGeneration, VMGeneration, SourceMaterializationGeneration      uint64
	StateGeneration, ChildPlanGeneration, LastClaimGeneration           uint64
}

type HostEvacuationClaim struct {
	OperationID, ChildOperationID, Owner, ClaimDigest string
	ClaimGeneration                                   uint64
	LeaseExpiresAt                                    time.Time
}

type PlannedSourceQuiescence struct {
	EvidenceID, ChildOperationID, VMID, SourceHostID, SourcePlanID string
	SourcePlanDigest, ShutdownCommandID, ShutdownResponseState     string
	ReadBackEvidenceID, ObservationDigest, QuiescenceDigest        string
	ChildGeneration, VMGeneration, SourceHostAuthorityGeneration   uint64
	SourceMaterializationGeneration, ReadBackObservationGeneration uint64
}

type HostEvacuationChildVerification struct {
	VerificationID, ChildOperationID, DestinationAdmissionID, DestinationHostID string
	DestinationBindingID, VerificationDigest                                    string
	ChildPlanGeneration                                                         uint64
}

type evacuationSnapshot struct {
	ChildOperationID, VMID, WorkloadID, AdmissionID, PlanID, PlanDigest string
	VMGeneration, MaterializationGeneration                             uint64
	BindingRevision                                                     *uint64
	BindingDigest                                                       *string
	Network, Storage, PCI                                               json.RawMessage
	AdmissionProvenanceDigest, SnapshotDigest                           string
}

func evacuationRequestDigest(request HostEvacuationRequest) string {
	raw, _ := json.Marshal(request)
	return digestReleaseBytes(raw)
}

// StartHostEvacuation atomically fences new Placement on the source Host and
// snapshots every current managed VM. The caller never supplies VM/backend
// identities. Planned authority remains entirely separate from Failure Epoch,
// Fencing Proof, and Recovery Budget.
func StartHostEvacuation(ctx context.Context, db TxBeginner, request HostEvacuationRequest) (HostEvacuationOperation, []HostEvacuationWorkload, error) {
	var operation HostEvacuationOperation
	var workloads []HostEvacuationWorkload
	if request.OperationID == "" || request.SourceHostID == "" || request.EvacuationGeneration == 0 ||
		request.SourceHostAuthorityGeneration == 0 || request.DrainPolicyID == "" || request.DrainPolicyRevision == 0 ||
		request.EvacuationPolicyRevision == 0 || request.MaximumConcurrentWorkloads <= 0 || request.Reason == "" || request.RequestedBy == "" {
		return operation, nil, ErrHostEvacuationConflict
	}
	requestDigest := evacuationRequestDigest(request)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		for _, key := range []string{"host-placement/" + request.SourceHostID, "host-evacuation/" + request.OperationID} {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
				return err
			}
		}
		var existingRequestDigest string
		err := tx.QueryRow(ctx, `SELECT request_digest,source_host_id,source_host_authority_generation,evacuation_generation,
			placement_pool_id,placement_pool_generation,membership_generation,drain_id,workload_set_id,
			workload_set_generation,workload_set_digest,maximum_concurrent_workloads,operation_digest
			FROM kim.host_evacuation_operation_evidence WHERE evacuation_operation_id=$1`, request.OperationID).
			Scan(&existingRequestDigest, &operation.SourceHostID, &operation.SourceHostAuthorityGeneration,
				&operation.EvacuationGeneration, &operation.PlacementPoolID, &operation.PlacementPoolGeneration,
				&operation.MembershipGeneration, &operation.DrainID, &operation.WorkloadSetID,
				&operation.WorkloadSetGeneration, &operation.WorkloadSetDigest,
				&operation.MaximumConcurrentWorkloads, &operation.OperationDigest)
		if err == nil {
			if existingRequestDigest != requestDigest || operation.SourceHostID != request.SourceHostID || operation.EvacuationGeneration != request.EvacuationGeneration {
				return ErrHostEvacuationConflict
			}
			operation.OperationID = request.OperationID
			if err := tx.QueryRow(ctx, `SELECT lifecycle_state,state_generation FROM kim.host_evacuation_operations_current WHERE evacuation_operation_id=$1`, request.OperationID).Scan(&operation.LifecycleState, &operation.StateGeneration); err != nil {
				return err
			}
			if err := loadHostEvacuationWorkloadsTx(ctx, tx, request.OperationID, &workloads); err != nil {
				return err
			}
			operation.WorkloadCount = len(workloads)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var authorityState, poolID, poolState, membershipState string
		var hostAuthorityGeneration, poolGeneration, membershipGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT a.authority_generation,a.authority_state,m.pool_id,m.membership_generation,m.membership_state,p.pool_generation,p.lifecycle_state
			FROM kim.host_operation_authorities_current a
			JOIN kim.host_placement_pool_memberships_current m ON m.host_id=a.host_id
			JOIN kim.placement_pools_current p ON p.pool_id=m.pool_id
			WHERE a.host_id=$1 FOR UPDATE OF a,m,p`, request.SourceHostID).Scan(&hostAuthorityGeneration, &authorityState,
			&poolID, &membershipGeneration, &membershipState, &poolGeneration, &poolState); err != nil {
			return ErrHostEvacuationStale
		}
		if authorityState != "ARMED" || hostAuthorityGeneration != request.SourceHostAuthorityGeneration || poolState != "ACTIVE" || membershipState != "ACTIVE" {
			return ErrHostEvacuationBlocked
		}
		var existingDrain string
		if err := tx.QueryRow(ctx, `SELECT drain_id FROM kim.host_placement_drains_current WHERE source_host_id=$1`, request.SourceHostID).Scan(&existingDrain); err == nil {
			return ErrHostEvacuationConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		snapshots, err := snapshotHostEvacuationWorkloadsTx(ctx, tx, request.OperationID, request.SourceHostID)
		if err != nil {
			return err
		}
		setDigests := make([]string, len(snapshots))
		for i := range snapshots {
			setDigests[i] = snapshots[i].SnapshotDigest
		}
		workloadSetDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%v", request.SourceHostID, request.EvacuationGeneration, setDigests)))
		drainID := fmt.Sprintf("host-drain:%s:%d", request.SourceHostID, request.EvacuationGeneration)
		drainDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%d/%s/%d/%d/%s/%s", request.SourceHostID,
			request.SourceHostAuthorityGeneration, poolID, poolGeneration, request.DrainPolicyID, request.DrainPolicyRevision,
			membershipGeneration, request.Reason, request.RequestedBy)))
		workloadSetID := request.OperationID + ":workload-set"
		operationDigest := digestReleaseBytes([]byte(requestDigest + "/" + drainDigest + "/" + workloadSetDigest))

		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_placement_drain_evidence(drain_id,source_host_id,drain_generation,source_host_authority_generation,placement_pool_id,placement_pool_generation,membership_generation,drain_policy_id,drain_policy_revision,drain_state,reason,actor,drain_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'DRAINING',$10,$11,$12)`, drainID, request.SourceHostID,
			request.EvacuationGeneration, request.SourceHostAuthorityGeneration, poolID, poolGeneration, membershipGeneration,
			request.DrainPolicyID, request.DrainPolicyRevision, request.Reason, request.RequestedBy, drainDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_placement_drains_current(source_host_id,drain_id,drain_generation,drain_state,drain_digest) VALUES($1,$2,$3,'DRAINING',$4)`, request.SourceHostID, drainID, request.EvacuationGeneration, drainDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_operation_evidence(evacuation_operation_id,evacuation_generation,source_host_id,source_host_authority_generation,placement_pool_id,placement_pool_generation,membership_generation,drain_id,drain_policy_id,drain_policy_revision,workload_set_id,workload_set_generation,workload_set_digest,maximum_concurrent_workloads,evacuation_policy_revision,reason,requested_by,request_digest,operation_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$2,$12,$13,$14,$15,$16,$17,$18)`, request.OperationID,
			request.EvacuationGeneration, request.SourceHostID, request.SourceHostAuthorityGeneration, poolID, poolGeneration,
			membershipGeneration, drainID, request.DrainPolicyID, request.DrainPolicyRevision, workloadSetID, workloadSetDigest,
			request.MaximumConcurrentWorkloads, request.EvacuationPolicyRevision, request.Reason, request.RequestedBy, requestDigest, operationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_workload_set_evidence(workload_set_id,workload_set_generation,evacuation_operation_id,evacuation_generation,workload_count,workload_set_digest) VALUES($1,$2,$3,$2,$4,$5)`, workloadSetID, request.EvacuationGeneration, request.OperationID, len(snapshots), workloadSetDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_operations_current(evacuation_operation_id,evacuation_generation,lifecycle_state,state_generation) VALUES($1,$2,'RUNNING',1)`, request.OperationID, request.EvacuationGeneration); err != nil {
			return err
		}
		for index, snapshot := range snapshots {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_workload_evidence(workload_set_id,workload_set_generation,child_operation_id,child_generation,ordinal,vm_id,vm_generation,workload_id,source_host_id,source_admission_id,source_plan_id,source_plan_digest,source_materialization_generation,availability_binding_revision,availability_binding_digest,network_requirements,storage_requirements,pci_requirements,admission_provenance_digest,snapshot_digest)
				VALUES($1,$2,$3,1,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, workloadSetID,
				request.EvacuationGeneration, snapshot.ChildOperationID, index+1, snapshot.VMID, snapshot.VMGeneration,
				snapshot.WorkloadID, request.SourceHostID, snapshot.AdmissionID, snapshot.PlanID, snapshot.PlanDigest,
				snapshot.MaterializationGeneration, snapshot.BindingRevision, snapshot.BindingDigest, snapshot.Network,
				snapshot.Storage, snapshot.PCI, snapshot.AdmissionProvenanceDigest, snapshot.SnapshotDigest); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_workloads_current(child_operation_id,child_generation,evacuation_operation_id,vm_id,vm_generation,phase,state_generation,result_state,reason_code) VALUES($1,1,$2,$3::uuid,$4,'DISCOVERED',1,'PENDING','snapshot_current')`, snapshot.ChildOperationID, request.OperationID, snapshot.VMID, snapshot.VMGeneration); err != nil {
				return err
			}
			transitionDigest := digestReleaseBytes([]byte(snapshot.SnapshotDigest + "/DISCOVERED"))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_child_transition_evidence(child_operation_id,child_generation,state_generation,from_phase,to_phase,result_state,reason_code,cause_type,cause_id,transition_digest) VALUES($1,1,1,NULL,'DISCOVERED','PENDING','snapshot_current','SNAPSHOT',$2,$3)`, snapshot.ChildOperationID, workloadSetID, transitionDigest); err != nil {
				return err
			}
		}
		eventPayload, _ := json.Marshal(map[string]any{"source_host_id": request.SourceHostID, "workload_count": len(snapshots), "maximum_concurrent_workloads": request.MaximumConcurrentWorkloads, "workload_set_digest": workloadSetDigest})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_event_evidence(evacuation_operation_id,evacuation_generation,event_sequence,event_type,event_payload,event_digest) VALUES($1,$2,1,'STARTED',$3,$4)`, request.OperationID, request.EvacuationGeneration, eventPayload, digestReleaseBytes(eventPayload)); err != nil {
			return err
		}
		operation = HostEvacuationOperation{OperationID: request.OperationID, SourceHostID: request.SourceHostID,
			PlacementPoolID: poolID, DrainID: drainID, WorkloadSetID: workloadSetID, WorkloadSetDigest: workloadSetDigest,
			LifecycleState: "RUNNING", OperationDigest: operationDigest, EvacuationGeneration: request.EvacuationGeneration,
			SourceHostAuthorityGeneration: request.SourceHostAuthorityGeneration, PlacementPoolGeneration: poolGeneration,
			MembershipGeneration: membershipGeneration, WorkloadSetGeneration: request.EvacuationGeneration,
			StateGeneration: 1, MaximumConcurrentWorkloads: request.MaximumConcurrentWorkloads, WorkloadCount: len(snapshots)}
		return loadHostEvacuationWorkloadsTx(ctx, tx, request.OperationID, &workloads)
	})
	return operation, workloads, err
}

func snapshotHostEvacuationWorkloadsTx(ctx context.Context, tx pgx.Tx, operationID, sourceHostID string) ([]evacuationSnapshot, error) {
	rows, err := tx.Query(ctx, `SELECT vm.vm_id::text,vm.vm_generation,vm.workload_id,vm.placement_admission_id,vm.current_plan_id,plan.plan_digest,
		COALESCE((plan.plan_payload->>'materialization_generation')::bigint,1),binding.binding_revision,binding.binding_digest,
		admission.network_requirements,admission.storage_requirements,admission.pci_requirements,
		admission.request_digest,admission.evaluation_digest
		FROM kim.virtual_machines_current vm
		JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id AND plan.vm_generation=vm.vm_generation AND plan.host_id=vm.host_id
		JOIN kim.placement_admission_decisions admission ON admission.admission_id=vm.placement_admission_id AND admission.host_id=vm.host_id
		LEFT JOIN kim.vm_availability_bindings_current binding ON binding.workload_id=vm.workload_id
		WHERE vm.host_id=$1 AND vm.lifecycle_state<>'DELETED'
		ORDER BY vm.vm_id FOR SHARE OF vm,plan,admission`, sourceHostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evacuationSnapshot
	for rows.Next() {
		var snapshot evacuationSnapshot
		var requestDigest, evaluationDigest string
		if err := rows.Scan(&snapshot.VMID, &snapshot.VMGeneration, &snapshot.WorkloadID, &snapshot.AdmissionID,
			&snapshot.PlanID, &snapshot.PlanDigest, &snapshot.MaterializationGeneration, &snapshot.BindingRevision,
			&snapshot.BindingDigest, &snapshot.Network, &snapshot.Storage, &snapshot.PCI, &requestDigest, &evaluationDigest); err != nil {
			return nil, err
		}
		snapshot.ChildOperationID = operationID + ":workload:" + snapshot.VMID
		snapshot.AdmissionProvenanceDigest = digestReleaseBytes([]byte(snapshot.AdmissionID + "/" + requestDigest + "/" + evaluationDigest))
		payload, _ := json.Marshal(snapshot)
		snapshot.SnapshotDigest = digestReleaseBytes(payload)
		out = append(out, snapshot)
	}
	return out, rows.Err()
}

func loadHostEvacuationWorkloadsTx(ctx context.Context, tx pgx.Tx, operationID string, out *[]HostEvacuationWorkload) error {
	rows, err := tx.Query(ctx, `SELECT c.child_operation_id,c.child_generation,c.vm_id::text,c.vm_generation,e.workload_id,e.source_host_id,e.source_admission_id,e.source_plan_id,e.source_plan_digest,e.source_materialization_generation,e.snapshot_digest,c.phase,c.result_state,c.reason_code,COALESCE(c.destination_host_id,''),COALESCE(c.destination_admission_id,''),COALESCE(c.child_plan_generation,0),c.state_generation,c.last_claim_generation
		FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e ON e.child_operation_id=c.child_operation_id AND e.child_generation=c.child_generation
		WHERE c.evacuation_operation_id=$1 ORDER BY e.ordinal`, operationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	*out = nil
	for rows.Next() {
		var item HostEvacuationWorkload
		if err := rows.Scan(&item.ChildOperationID, &item.ChildGeneration, &item.VMID, &item.VMGeneration, &item.WorkloadID,
			&item.SourceHostID, &item.SourceAdmissionID, &item.SourcePlanID, &item.SourcePlanDigest,
			&item.SourceMaterializationGeneration, &item.SnapshotDigest, &item.Phase, &item.ResultState,
			&item.ReasonCode, &item.DestinationHostID, &item.DestinationAdmissionID, &item.ChildPlanGeneration,
			&item.StateGeneration, &item.LastClaimGeneration); err != nil {
			return err
		}
		*out = append(*out, item)
	}
	return rows.Err()
}

// EvaluateHostEvacuationEligibility derives profile eligibility from the
// immutable snapshot. One ordinary boot root may proceed only to the later
// planned root-safety and relocation gates; physical PCI remains blocked.
func EvaluateHostEvacuationEligibility(ctx context.Context, db TxBeginner, operationID string) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT c.child_operation_id,jsonb_array_length(e.storage_requirements),jsonb_array_length(e.pci_requirements),COALESCE((e.storage_requirements->0->>'Bootable')::boolean,false)
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			WHERE c.evacuation_operation_id=$1 AND c.phase='DISCOVERED' ORDER BY e.ordinal FOR UPDATE OF c`, operationID)
		if err != nil {
			return err
		}
		type decision struct{ id, phase, result, reason string }
		var decisions []decision
		for rows.Next() {
			var d decision
			var storageCount, pciCount int
			var rootBootable bool
			if err := rows.Scan(&d.id, &storageCount, &pciCount, &rootBootable); err != nil {
				rows.Close()
				return err
			}
			switch {
			case storageCount > 1 || (storageCount == 1 && !rootBootable):
				d.phase, d.result, d.reason = "BLOCKED", "BLOCKED", "local_lvm_data_independence_unproven"
			case pciCount > 0:
				d.phase, d.result, d.reason = "BLOCKED", "BLOCKED", "physical_pci_vf_evacuation_unqualified"
			default:
				d.phase, d.result, d.reason = "READY_TO_QUIESCE", "ELIGIBLE", "planned_relocation_profile_requires_closed_later_gates"
			}
			decisions = append(decisions, d)
		}
		rows.Close()
		for _, d := range decisions {
			if err := transitionHostEvacuationChildTx(ctx, tx, d.id, d.phase, d.result, d.reason, "ELIGIBILITY", operationID+":eligibility"); err != nil {
				return err
			}
		}
		return updateHostEvacuationAggregateTx(ctx, tx, operationID)
	})
}

func transitionHostEvacuationChildTx(ctx context.Context, tx pgx.Tx, childID, phase, result, reason, causeType, causeID string) error {
	var from string
	var generation uint64
	if err := tx.QueryRow(ctx, `SELECT phase,state_generation FROM kim.host_evacuation_workloads_current WHERE child_operation_id=$1 FOR UPDATE`, childID).Scan(&from, &generation); err != nil {
		return err
	}
	generation++
	digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%s/%s", childID, generation, from, phase, result, causeID)))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_child_transition_evidence(child_operation_id,child_generation,state_generation,from_phase,to_phase,result_state,reason_code,cause_type,cause_id,transition_digest) SELECT $1,child_generation,$2,phase,$3,$4,$5,$6,$7,$8 FROM kim.host_evacuation_workloads_current WHERE child_operation_id=$1`, childID, generation, phase, result, reason, causeType, causeID, digest); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE kim.host_evacuation_workloads_current SET phase=$2,result_state=$3,reason_code=$4,state_generation=$5,updated_at=statement_timestamp() WHERE child_operation_id=$1`, childID, phase, result, reason, generation)
	return err
}

func ClaimHostEvacuationWorkload(ctx context.Context, db TxBeginner, operationID, owner string, lease time.Duration) (HostEvacuationClaim, error) {
	var out HostEvacuationClaim
	var exhausted bool
	if operationID == "" || owner == "" || lease <= 0 {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "host-evacuation-budget/"+operationID); err != nil {
			return err
		}
		var maximum, active int
		var state string
		if err := tx.QueryRow(ctx, `SELECT e.maximum_concurrent_workloads,c.lifecycle_state FROM kim.host_evacuation_operation_evidence e JOIN kim.host_evacuation_operations_current c USING(evacuation_operation_id,evacuation_generation) WHERE e.evacuation_operation_id=$1 FOR UPDATE OF c`, operationID).Scan(&maximum, &state); err != nil {
			return err
		}
		if state != "RUNNING" && state != "PARTIAL" {
			return ErrHostEvacuationBlocked
		}
		// Expiry is reclaimable only before source mutation. Dangerous/in-flight
		// phases remain UNKNOWN until explicit read-back reconciliation.
		expiredRows, err := tx.Query(ctx, `SELECT slot.child_operation_id,child.phase FROM kim.host_evacuation_slot_claims_current slot JOIN kim.host_evacuation_workloads_current child USING(child_operation_id) WHERE slot.evacuation_operation_id=$1 AND slot.claim_state='CLAIMED' AND slot.lease_expires_at<=statement_timestamp() FOR UPDATE OF slot,child`, operationID)
		if err != nil {
			return err
		}
		type expiredClaim struct{ childID, phase string }
		var expired []expiredClaim
		for expiredRows.Next() {
			var item expiredClaim
			if err := expiredRows.Scan(&item.childID, &item.phase); err != nil {
				expiredRows.Close()
				return err
			}
			expired = append(expired, item)
		}
		expiredRows.Close()
		for _, item := range expired {
			if item.phase == "READY_TO_QUIESCE" {
				if err := transitionHostEvacuationSlotTx(ctx, tx, operationID, item.childID, "RECLAIMED", "lease_expired_before_source_mutation"); err != nil {
					return err
				}
			} else if err := transitionHostEvacuationSlotTx(ctx, tx, operationID, item.childID, "UNKNOWN", "lease_expired_backend_effect_requires_read_back"); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.host_evacuation_slot_claims_current WHERE evacuation_operation_id=$1`, operationID).Scan(&active); err != nil {
			return err
		}
		if active >= maximum {
			exhausted = true
			return nil
		}
		var last uint64
		if err := tx.QueryRow(ctx, `SELECT child_operation_id,last_claim_generation FROM kim.host_evacuation_workloads_current WHERE evacuation_operation_id=$1 AND phase='READY_TO_QUIESCE' AND NOT EXISTS(SELECT 1 FROM kim.host_evacuation_slot_claims_current slot WHERE slot.child_operation_id=host_evacuation_workloads_current.child_operation_id) ORDER BY child_operation_id FOR UPDATE SKIP LOCKED LIMIT 1`, operationID).Scan(&out.ChildOperationID, &last); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				exhausted = true
				return nil
			}
			return err
		}
		out.OperationID, out.Owner, out.ClaimGeneration = operationID, owner, last+1
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()+$1::interval`, lease.String()).Scan(&out.LeaseExpiresAt); err != nil {
			return err
		}
		out.ClaimDigest = digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%s", operationID, out.ChildOperationID, out.ClaimGeneration, owner, out.LeaseExpiresAt.Format(time.RFC3339Nano))))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_slot_claim_evidence(evacuation_operation_id,child_operation_id,claim_generation,claim_owner,claim_state,lease_expires_at,claim_digest) VALUES($1,$2,$3,$4,'CLAIMED',$5,$6)`, operationID, out.ChildOperationID, out.ClaimGeneration, owner, out.LeaseExpiresAt, out.ClaimDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_slot_claims_current(evacuation_operation_id,child_operation_id,claim_generation,claim_owner,claim_state,lease_expires_at,claim_digest) VALUES($1,$2,$3,$4,'CLAIMED',$5,$6)`, operationID, out.ChildOperationID, out.ClaimGeneration, owner, out.LeaseExpiresAt, out.ClaimDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_evacuation_workloads_current SET last_claim_generation=$2 WHERE child_operation_id=$1`, out.ChildOperationID, out.ClaimGeneration); err != nil {
			return err
		}
		return transitionHostEvacuationChildTx(ctx, tx, out.ChildOperationID, "QUIESCING_SOURCE", "RUNNING", "bounded_slot_claimed", "SLOT_CLAIM", out.ClaimDigest)
	})
	if err == nil && exhausted {
		return HostEvacuationClaim{}, ErrHostEvacuationBudgetExhausted
	}
	return out, err
}

func validateEvacuationClaimTx(ctx context.Context, tx pgx.Tx, claim HostEvacuationClaim) error {
	var owner, digest, state string
	var generation uint64
	if err := tx.QueryRow(ctx, `SELECT claim_generation,claim_owner,claim_state,claim_digest FROM kim.host_evacuation_slot_claims_current WHERE evacuation_operation_id=$1 AND child_operation_id=$2 FOR UPDATE`, claim.OperationID, claim.ChildOperationID).Scan(&generation, &owner, &state, &digest); err != nil || generation != claim.ClaimGeneration || owner != claim.Owner || digest != claim.ClaimDigest || state != "CLAIMED" {
		return ErrHostEvacuationStale
	}
	return nil
}

func transitionHostEvacuationSlotTx(ctx context.Context, tx pgx.Tx, operationID, childID, toState, reason string) error {
	var generation uint64
	var fromState, claimDigest string
	if err := tx.QueryRow(ctx, `SELECT claim_generation,claim_state,claim_digest FROM kim.host_evacuation_slot_claims_current WHERE evacuation_operation_id=$1 AND child_operation_id=$2 FOR UPDATE`, operationID, childID).Scan(&generation, &fromState, &claimDigest); err != nil {
		return err
	}
	var transitionGeneration uint64
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(transition_generation),0)+1 FROM kim.host_evacuation_slot_transition_evidence WHERE evacuation_operation_id=$1 AND child_operation_id=$2 AND claim_generation=$3`, operationID, childID, generation).Scan(&transitionGeneration); err != nil {
		return err
	}
	transitionDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%d/%s/%s/%s", operationID, childID, generation, transitionGeneration, fromState, toState, reason)))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_slot_transition_evidence(evacuation_operation_id,child_operation_id,claim_generation,transition_generation,from_state,to_state,reason_code,claim_digest,transition_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, operationID, childID, generation, transitionGeneration, fromState, toState, reason, claimDigest, transitionDigest); err != nil {
		return err
	}
	if toState == "UNKNOWN" {
		_, err := tx.Exec(ctx, `UPDATE kim.host_evacuation_slot_claims_current SET claim_state='UNKNOWN',updated_at=statement_timestamp() WHERE evacuation_operation_id=$1 AND child_operation_id=$2`, operationID, childID)
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM kim.host_evacuation_slot_claims_current WHERE evacuation_operation_id=$1 AND child_operation_id=$2`, operationID, childID)
	return err
}

// AuthorizeHostEvacuationSourceShutdown binds the exact claimed child to the
// ordinary typed VM power-off primitive. It creates no quiescence evidence.
func AuthorizeHostEvacuationSourceShutdown(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, shutdownAuthorityID, jobID, commandID string) error {
	if shutdownAuthorityID == "" || jobID == "" || commandID == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var phase, vmID, sourceHost, sourcePlan string
		var vmGeneration, materializationGeneration, expectedAuthority, currentAuthority, currentPowerObservation uint64
		var authorityState string
		if err := tx.QueryRow(ctx, `SELECT c.phase,e.vm_id::text,e.vm_generation,e.source_host_id,e.source_plan_id,e.source_materialization_generation,o.source_host_authority_generation,a.authority_generation,a.authority_state,power.observation_generation
			FROM kim.host_evacuation_workloads_current c
			JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_operation_evidence o ON o.evacuation_operation_id=c.evacuation_operation_id
			JOIN kim.host_operation_authorities_current a ON a.host_id=e.source_host_id
			JOIN kim.virtual_machines_current vm ON vm.vm_id=e.vm_id AND vm.vm_generation=e.vm_generation AND vm.host_id=e.source_host_id AND vm.current_plan_id=e.source_plan_id
			JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED'
			WHERE c.child_operation_id=$1 FOR UPDATE OF c,vm,a`, claim.ChildOperationID).Scan(&phase, &vmID, &vmGeneration, &sourceHost, &sourcePlan, &materializationGeneration, &expectedAuthority, &currentAuthority, &authorityState, &currentPowerObservation); err != nil {
			return ErrHostEvacuationBlocked
		}
		if phase != "QUIESCING_SOURCE" || authorityState != "ARMED" || currentAuthority != expectedAuthority {
			return ErrHostEvacuationStale
		}
		if err := createTypedVMPowerCommandTx(ctx, tx, vmID, vmGeneration, currentPowerObservation+1, sourceHost, "SHUTOFF", jobID, commandID); err != nil {
			return err
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%d/%s/%s", claim.ChildOperationID, vmID, vmGeneration, sourceHost, currentAuthority, sourcePlan, commandID)))
		_, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_source_shutdown_authority_evidence(shutdown_authority_id,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_host_authority_generation,source_plan_id,source_materialization_generation,shutdown_command_id,authority_digest) VALUES($1,$2,1,$3::uuid,$4,$5,$6,$7,$8,$9,$10)`, shutdownAuthorityID, claim.ChildOperationID, vmID, vmGeneration, sourceHost, currentAuthority, sourcePlan, materializationGeneration, commandID, digest)
		return err
	})
}

// RecordPlannedSourceQuiescence accepts only an evidence identifier. Every
// positive fact is re-derived from immutable execution and libvirt read-back.
func RecordPlannedSourceQuiescence(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, evidenceID string) (PlannedSourceQuiescence, error) {
	var out PlannedSourceQuiescence
	if evidenceID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var phase, currentHost, currentPlan, authorityState, shutdownAuthorityID, verifierDigest string
		var expectedHostAuthorityGeneration, attemptIndex, leaseGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT c.phase,e.vm_id::text,e.vm_generation,e.source_host_id,e.source_plan_id,e.source_plan_digest,e.source_materialization_generation,operation.source_host_authority_generation,a.authority_generation,a.authority_state,vm.host_id,vm.current_plan_id,
			sa.shutdown_authority_id,sa.shutdown_command_id,power.evidence_id,power.observation_generation,power.observation_digest,power.verifier_digest,attempt.attempt_index,attempt.lease_generation,
			verification.verification_id,CASE WHEN result.execution_outcome IS NULL OR result.execution_outcome='UNKNOWN' THEN 'LOST' ELSE 'RECEIVED' END
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_operation_evidence operation ON operation.evacuation_operation_id=c.evacuation_operation_id
			JOIN kim.host_operation_authorities_current a ON a.host_id=e.source_host_id JOIN kim.virtual_machines_current vm ON vm.vm_id=e.vm_id
			JOIN kim.host_evacuation_source_shutdown_authority_evidence sa ON sa.child_operation_id=c.child_operation_id AND sa.child_generation=c.child_generation AND sa.vm_id=e.vm_id AND sa.vm_generation=e.vm_generation AND sa.source_host_id=e.source_host_id AND sa.source_plan_id=e.source_plan_id
			JOIN kim.vm_power_observation_evidence power ON power.command_id=sa.shutdown_command_id AND power.vm_id=e.vm_id AND power.vm_generation=e.vm_generation AND power.host_id=e.source_host_id AND power.desired_power_state='SHUTOFF' AND power.observed_power_state='SHUTOFF'
			JOIN kim.command_attempts attempt ON attempt.command_id=power.command_id AND attempt.attempt_index=power.attempt_index AND attempt.host_authority_generation=sa.source_host_authority_generation
			LEFT JOIN kim.command_results result ON result.command_id=attempt.command_id AND result.attempt_index=attempt.attempt_index
			JOIN kim.command_verification_evidence verification ON verification.verification_id=power.verification_id AND verification.command_id=attempt.command_id AND verification.attempt_index=attempt.attempt_index AND verification.verification_state='MATCHED' AND verification.observation_generation=power.observation_generation AND verification.observation_digest=power.observation_digest
			WHERE c.child_operation_id=$1 FOR UPDATE OF c,vm`, claim.ChildOperationID).Scan(&phase, &out.VMID, &out.VMGeneration, &out.SourceHostID, &out.SourcePlanID, &out.SourcePlanDigest, &out.SourceMaterializationGeneration, &expectedHostAuthorityGeneration, &out.SourceHostAuthorityGeneration, &authorityState, &currentHost, &currentPlan, &shutdownAuthorityID, &out.ShutdownCommandID, &out.ReadBackEvidenceID, &out.ReadBackObservationGeneration, &out.ObservationDigest, &verifierDigest, &attemptIndex, &leaseGeneration, new(string), &out.ShutdownResponseState); err != nil {
			return ErrHostEvacuationBlocked
		}
		if phase != "QUIESCING_SOURCE" || authorityState != "ARMED" || out.SourceHostAuthorityGeneration != expectedHostAuthorityGeneration || currentHost != out.SourceHostID || currentPlan != out.SourcePlanID {
			return ErrHostEvacuationStale
		}
		out.EvidenceID, out.ChildOperationID, out.ChildGeneration = evidenceID, claim.ChildOperationID, 1
		out.QuiescenceDigest = digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%s/%s/%d/SHUTOFF", out.ChildOperationID, out.VMID, out.VMGeneration, out.SourceHostID, out.SourcePlanDigest, out.ObservationDigest, out.ReadBackObservationGeneration)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.planned_source_quiescence_evidence(quiescence_evidence_id,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_host_authority_generation,source_plan_id,source_plan_digest,source_materialization_generation,shutdown_command_id,shutdown_response_state,read_back_evidence_id,read_back_observation_generation,observed_power_state,identity_matches,observation_digest,quiescence_digest) VALUES($1,$2,1,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'SHUTOFF',true,$14,$15)`, out.EvidenceID, out.ChildOperationID, out.VMID, out.VMGeneration, out.SourceHostID, out.SourceHostAuthorityGeneration, out.SourcePlanID, out.SourcePlanDigest, out.SourceMaterializationGeneration, out.ShutdownCommandID, out.ShutdownResponseState, out.ReadBackEvidenceID, out.ReadBackObservationGeneration, out.ObservationDigest, out.QuiescenceDigest); err != nil {
			return err
		}
		executionDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%d/%s", shutdownAuthorityID, out.ShutdownCommandID, attemptIndex, out.ReadBackEvidenceID, out.ReadBackObservationGeneration, out.ObservationDigest)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.planned_source_quiescence_execution_evidence(quiescence_evidence_id,shutdown_authority_id,shutdown_command_id,shutdown_attempt_index,shutdown_lease_generation,read_back_verification_id,power_observation_evidence_id,power_observation_generation,power_observation_digest,verifier_digest,evidence_digest) SELECT $1,$2,$3,$4,$5,power.verification_id,$6,$7,$8,$9,$10 FROM kim.vm_power_observation_evidence power WHERE power.evidence_id=$6`, out.EvidenceID, shutdownAuthorityID, out.ShutdownCommandID, attemptIndex, leaseGeneration, out.ReadBackEvidenceID, out.ReadBackObservationGeneration, out.ObservationDigest, verifierDigest, executionDigest); err != nil {
			return err
		}
		return transitionHostEvacuationChildTx(ctx, tx, claim.ChildOperationID, "SOURCE_QUIESCED", "RUNNING", "typed_shutdown_read_back_shutoff", "OBSERVATION", out.EvidenceID)
	})
	return out, err
}

func BlockHostEvacuationChild(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, reason string) error {
	if reason == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		if err := transitionHostEvacuationChildTx(ctx, tx, claim.ChildOperationID, "BLOCKED", "BLOCKED", reason, "RECONCILIATION", claim.ClaimDigest); err != nil {
			return err
		}
		if err := transitionHostEvacuationSlotTx(ctx, tx, claim.OperationID, claim.ChildOperationID, "RELEASED", "child_blocked"); err != nil {
			return err
		}
		return updateHostEvacuationAggregateTx(ctx, tx, claim.OperationID)
	})
}

func CancelHostEvacuationChild(ctx context.Context, db TxBeginner, operationID, childID, reason string) error {
	if reason == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var phase string
		if err := tx.QueryRow(ctx, `SELECT phase FROM kim.host_evacuation_workloads_current WHERE evacuation_operation_id=$1 AND child_operation_id=$2 FOR UPDATE`, operationID, childID).Scan(&phase); err != nil {
			return err
		}
		if phase != "DISCOVERED" && phase != "READY_TO_QUIESCE" {
			return ErrHostEvacuationBlocked
		}
		if err := transitionHostEvacuationChildTx(ctx, tx, childID, "CANCELLED", "CANCELLED", reason, "CANCELLATION", operationID); err != nil {
			return err
		}
		return updateHostEvacuationAggregateTx(ctx, tx, operationID)
	})
}

// ReconcileHostEvacuationSourceAuthority never converts planned work into
// Recovery. Loss of reachable ARMED authority marks all remaining children as
// RECOVERY_REQUIRED; a new normal Failure Epoch must own any recovery.
func ReconcileHostEvacuationSourceAuthority(ctx context.Context, db TxBeginner, operationID string) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var sourceHost, authorityState string
		var expected, current uint64
		if err := tx.QueryRow(ctx, `SELECT e.source_host_id,e.source_host_authority_generation,COALESCE(a.authority_generation,0),COALESCE(a.authority_state,'MISSING') FROM kim.host_evacuation_operation_evidence e LEFT JOIN kim.host_operation_authorities_current a ON a.host_id=e.source_host_id WHERE e.evacuation_operation_id=$1`, operationID).Scan(&sourceHost, &expected, &current, &authorityState); err != nil {
			return err
		}
		if authorityState == "ARMED" && current == expected {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT child_operation_id FROM kim.host_evacuation_workloads_current WHERE evacuation_operation_id=$1 AND phase NOT IN ('VERIFIED','BLOCKED','CONFLICTING','STALE','SKIPPED_NOT_CURRENT','CANCELLED','RECOVERY_REQUIRED') FOR UPDATE`, operationID)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if err := transitionHostEvacuationChildTx(ctx, tx, id, "RECOVERY_REQUIRED", "RECOVERY_REQUIRED", "source_authority_unreachable_start_normal_failure_chain", "RECONCILIATION", sourceHost); err != nil {
				return err
			}
		}
		slotRows, err := tx.Query(ctx, `SELECT child_operation_id FROM kim.host_evacuation_slot_claims_current WHERE evacuation_operation_id=$1 FOR UPDATE`, operationID)
		if err != nil {
			return err
		}
		var slotChildren []string
		for slotRows.Next() {
			var childID string
			if err := slotRows.Scan(&childID); err != nil {
				slotRows.Close()
				return err
			}
			slotChildren = append(slotChildren, childID)
		}
		slotRows.Close()
		for _, childID := range slotChildren {
			if err := transitionHostEvacuationSlotTx(ctx, tx, operationID, childID, "RELEASED", "planned_authority_stopped_source_unreachable"); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE kim.host_evacuation_operations_current SET lifecycle_state='SOURCE_UNREACHABLE',state_generation=state_generation+1,updated_at=statement_timestamp() WHERE evacuation_operation_id=$1`, operationID)
		return err
	})
}

func updateHostEvacuationAggregateTx(ctx context.Context, tx pgx.Tx, operationID string) error {
	var pending, verified, blocked int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE result_state IN ('PENDING','ELIGIBLE','RUNNING')),count(*) FILTER(WHERE result_state='VERIFIED'),count(*) FILTER(WHERE result_state IN ('BLOCKED','CONFLICTING','STALE','CANCELLED','RECOVERY_REQUIRED')) FROM kim.host_evacuation_workloads_current WHERE evacuation_operation_id=$1`, operationID).Scan(&pending, &verified, &blocked); err != nil {
		return err
	}
	state := "RUNNING"
	if blocked > 0 {
		state = "PARTIAL"
	}
	if pending == 0 && verified == 0 && blocked > 0 {
		state = "BLOCKED"
	}
	_, err := tx.Exec(ctx, `UPDATE kim.host_evacuation_operations_current SET lifecycle_state=$2,state_generation=CASE WHEN lifecycle_state=$2 THEN state_generation ELSE state_generation+1 END,updated_at=statement_timestamp() WHERE evacuation_operation_id=$1 AND lifecycle_state NOT IN ('VERIFIED','SOURCE_UNREACHABLE')`, operationID, state)
	return err
}

func FinalizeHostEvacuation(ctx context.Context, db TxBeginner, operationID, terminalID string) (HostEvacuationOperation, error) {
	var out HostEvacuationOperation
	if operationID == "" || terminalID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "host-evacuation/"+operationID); err != nil {
			return err
		}
		var recoveryTerminalExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.recovery_terminal_decision_evidence WHERE terminal_decision_id=$1)`, terminalID).Scan(&recoveryTerminalExists); err != nil {
			return err
		}
		if recoveryTerminalExists {
			return ErrHostEvacuationConflict
		}
		var drainRecorded time.Time
		if err := tx.QueryRow(ctx, `SELECT e.evacuation_generation,e.source_host_id,e.source_host_authority_generation,e.placement_pool_id,e.placement_pool_generation,e.membership_generation,e.drain_id,e.workload_set_id,e.workload_set_generation,e.workload_set_digest,e.maximum_concurrent_workloads,e.operation_digest,c.lifecycle_state,c.state_generation,d.recorded_at
			FROM kim.host_evacuation_operation_evidence e JOIN kim.host_evacuation_operations_current c USING(evacuation_operation_id,evacuation_generation) JOIN kim.host_placement_drain_evidence d ON d.drain_id=e.drain_id WHERE e.evacuation_operation_id=$1 FOR UPDATE OF c`, operationID).Scan(&out.EvacuationGeneration, &out.SourceHostID, &out.SourceHostAuthorityGeneration, &out.PlacementPoolID, &out.PlacementPoolGeneration, &out.MembershipGeneration, &out.DrainID, &out.WorkloadSetID, &out.WorkloadSetGeneration, &out.WorkloadSetDigest, &out.MaximumConcurrentWorkloads, &out.OperationDigest, &out.LifecycleState, &out.StateGeneration, &drainRecorded); err != nil {
			return err
		}
		out.OperationID = operationID
		var existingOperation string
		var existingGeneration uint64
		var existingWorkloads int
		if err := tx.QueryRow(ctx, `SELECT evacuation_operation_id,evacuation_generation,workload_count FROM kim.host_evacuation_terminal_evidence WHERE terminal_evidence_id=$1`, terminalID).Scan(&existingOperation, &existingGeneration, &existingWorkloads); err == nil {
			if existingOperation != operationID || existingGeneration != out.EvacuationGeneration {
				return ErrHostEvacuationConflict
			}
			var currentTerminal, drainState string
			if err := tx.QueryRow(ctx, `SELECT c.terminal_evidence_id,d.drain_state FROM kim.host_evacuation_operations_current c JOIN kim.host_placement_drains_current d ON d.drain_id=$2 AND d.source_host_id=$3 WHERE c.evacuation_operation_id=$1 AND c.lifecycle_state='VERIFIED'`, operationID, out.DrainID, out.SourceHostID).Scan(&currentTerminal, &drainState); err != nil || currentTerminal != terminalID || drainState != "DRAINED" {
				return ErrHostEvacuationConflict
			}
			out.LifecycleState, out.WorkloadCount = "VERIFIED", existingWorkloads
			return nil
		} else if err != pgx.ErrNoRows {
			return err
		}
		var total, verified, activeSource, postDrain int
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE result_state='VERIFIED') FROM kim.host_evacuation_workloads_current WHERE evacuation_operation_id=$1`, operationID).Scan(&total, &verified); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.virtual_machines_current WHERE host_id=$1 AND lifecycle_state<>'DELETED'`, out.SourceHostID).Scan(&activeSource); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.placement_admission_decisions WHERE host_id=$1 AND decided_at>$2`, out.SourceHostID, drainRecorded).Scan(&postDrain); err != nil {
			return err
		}
		if total != verified || activeSource != 0 || postDrain != 0 {
			return ErrHostEvacuationBlocked
		}
		decisionDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%d/%d/%d", operationID, out.EvacuationGeneration, out.WorkloadSetDigest, total, activeSource, postDrain)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_terminal_evidence(terminal_evidence_id,evacuation_operation_id,evacuation_generation,workload_set_digest,drain_id,workload_count,verified_count,active_source_workload_count,post_drain_admission_count,terminal_state,decision_digest) VALUES($1,$2,$3,$4,$5,$6,$6,0,0,'VERIFIED',$7) ON CONFLICT(terminal_evidence_id) DO NOTHING`, terminalID, operationID, out.EvacuationGeneration, out.WorkloadSetDigest, out.DrainID, total, decisionDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_evacuation_operations_current SET lifecycle_state='VERIFIED',state_generation=state_generation+1,terminal_evidence_id=$2,updated_at=statement_timestamp() WHERE evacuation_operation_id=$1`, operationID, terminalID); err != nil {
			return err
		}
		drainTransitionDigest := digestReleaseBytes([]byte(out.DrainID + "/DRAINING/DRAINED/" + terminalID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_placement_drain_transition_evidence(drain_id,transition_generation,from_state,to_state,cause_type,cause_id,transition_digest) VALUES($1,1,'DRAINING','DRAINED','EVACUATION_TERMINAL',$2,$3)`, out.DrainID, terminalID, drainTransitionDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_placement_drains_current SET drain_state='DRAINED',updated_at=statement_timestamp() WHERE source_host_id=$1 AND drain_id=$2`, out.SourceHostID, out.DrainID); err != nil {
			return err
		}
		out.LifecycleState, out.WorkloadCount, out.StateGeneration = "VERIFIED", total, out.StateGeneration+1
		return nil
	})
	return out, err
}

// ReleaseHostPlacementDrain is the explicit undrain authority. Evacuation
// success never invokes it automatically.
func ReleaseHostPlacementDrain(ctx context.Context, db TxBeginner, sourceHostID, drainID, actor, reason string) error {
	if sourceHostID == "" || drainID == "" || actor == "" || reason == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "host-placement/"+sourceHostID); err != nil {
			return err
		}
		var state, terminalState, authorityState, poolState, membershipState string
		if err := tx.QueryRow(ctx, `SELECT d.drain_state,e.lifecycle_state,authority.authority_state,pool.lifecycle_state,membership.membership_state FROM kim.host_placement_drains_current d JOIN kim.host_evacuation_operation_evidence operation ON operation.drain_id=d.drain_id JOIN kim.host_evacuation_operations_current e ON e.evacuation_operation_id=operation.evacuation_operation_id JOIN kim.host_operation_authorities_current authority ON authority.host_id=d.source_host_id JOIN kim.host_placement_pool_memberships_current membership ON membership.host_id=d.source_host_id AND membership.pool_id=operation.placement_pool_id JOIN kim.placement_pools_current pool ON pool.pool_id=membership.pool_id WHERE d.source_host_id=$1 AND d.drain_id=$2 FOR UPDATE OF d,e,authority,membership,pool`, sourceHostID, drainID).Scan(&state, &terminalState, &authorityState, &poolState, &membershipState); err != nil || state != "DRAINED" || terminalState != "VERIFIED" || authorityState != "ARMED" || poolState != "ACTIVE" || membershipState != "ACTIVE" {
			return ErrHostEvacuationBlocked
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/DRAINED/RELEASED/%s/%s", drainID, actor, reason)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_placement_drain_transition_evidence(drain_id,transition_generation,from_state,to_state,cause_type,cause_id,transition_digest) VALUES($1,2,'DRAINED','RELEASED','EXPLICIT_UNDRAIN',$2,$3)`, drainID, actor+":"+reason, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM kim.host_placement_drains_current WHERE source_host_id=$1 AND drain_id=$2`, sourceHostID, drainID)
		return err
	})
}

// HostEvacuationMetricsSnapshot intentionally exposes aggregate counts only;
// VM/Host identities are not metric labels.
type HostEvacuationMetricsSnapshot struct {
	ActiveEvacuations, WorkloadsTotal, WorkloadsPending, WorkloadsRunning int
	WorkloadsVerified, WorkloadsBlocked, CurrentConcurrency, UnknownCount int
}

func LoadHostEvacuationMetrics(ctx context.Context, row QueryRower) (HostEvacuationMetricsSnapshot, error) {
	var out HostEvacuationMetricsSnapshot
	err := row.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.host_evacuation_operations_current WHERE lifecycle_state IN ('DRAINING','RUNNING','PARTIAL')),
		count(*),count(*) FILTER(WHERE result_state IN ('PENDING','ELIGIBLE')),count(*) FILTER(WHERE result_state='RUNNING'),
		count(*) FILTER(WHERE result_state='VERIFIED'),count(*) FILTER(WHERE result_state IN ('BLOCKED','CONFLICTING','STALE','RECOVERY_REQUIRED')),
		(SELECT count(*) FROM kim.host_evacuation_slot_claims_current),
		(SELECT count(*) FROM kim.host_evacuation_slot_claims_current WHERE claim_state='UNKNOWN')
		FROM kim.host_evacuation_workloads_current`).Scan(&out.ActiveEvacuations, &out.WorkloadsTotal, &out.WorkloadsPending,
		&out.WorkloadsRunning, &out.WorkloadsVerified, &out.WorkloadsBlocked, &out.CurrentConcurrency, &out.UnknownCount)
	return out, err
}

// Keep deterministic ordering available to callers rebuilding an in-memory
// queue; PostgreSQL remains the authority.
func SortHostEvacuationWorkloads(items []HostEvacuationWorkload) {
	sort.Slice(items, func(i, j int) bool { return items[i].ChildOperationID < items[j].ChildOperationID })
}
