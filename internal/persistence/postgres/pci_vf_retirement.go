package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrPCIVFRetirementStale = errors.New("stale PCI VF retirement authority")

type PCIVFRetirementRequest struct {
	OperationID                                                            string
	OperationGeneration                                                    uint64
	ClaimID                                                                string
	AllocationGeneration                                                   uint64
	SourceHostID, DeviceAddress, PortID, WorkloadID, VMID, OwnershipMarker string
	PortGeneration, BindingGeneration, VMGeneration                        uint64
}

type PCIVFRetirementOperation struct {
	PCIVFRetirementRequest
	State, TerminalEvidenceID string
	ClaimGeneration           uint64
	ClaimMode                 string
	ClaimExpiresAt            time.Time
}

type PCIVFRetirementClaim struct {
	ClaimID              string
	AllocationGeneration uint64
	Owner                string
	ClaimGeneration      uint64
	ClaimMode            string
	ExpiresAt            time.Time
}

type PCIVFRetirementCommand struct{ JobID, CommandID, PayloadDigest string }

type PCIVFRetirementObservation struct {
	EvidenceID string
	PCIVFRetirementRequest
	ClaimGeneration                                                     uint64
	OwnershipMarkerMatches, SourceDomainNotRunning, SourceHostdevAbsent bool
	VFDriverReleased, VFHolderAbsent, IOMMUGroupMatches                 bool
	PCIObservationGeneration, LibvirtObservationGeneration              uint64
	PCIObservationDigest, LibvirtObservationDigest, ApplyResponseState  string
	CommandID, VerificationID, VerifierDigest                           string
	AttemptIndex                                                        int
	ResultState, EvidenceDigest                                         string
}

type PCIVFHandoffRequest struct {
	HandoffID, WorkloadID, PortID                                       string
	PortGeneration                                                      uint64
	SourceClaimID                                                       string
	SourceAllocationGeneration                                          uint64
	SourceHostID, SourceDeviceAddress, SourceRetirementEvidenceID       string
	DestinationClaimID                                                  string
	DestinationAllocationGeneration                                     uint64
	DestinationHostID, DestinationDeviceAddress, DestinationAdmissionID string
}

func CommitPCIVFRetirement(ctx context.Context, db TxBeginner, r PCIVFRetirementRequest) (PCIVFRetirementOperation, error) {
	var out PCIVFRetirementOperation
	if r.OperationID == "" || r.OperationGeneration == 0 || r.ClaimID == "" || r.AllocationGeneration == 0 || r.SourceHostID == "" || r.DeviceAddress == "" || r.PortID == "" || r.PortGeneration == 0 || r.BindingGeneration == 0 || r.WorkloadID == "" || r.VMID == "" || r.VMGeneration == 0 || len(r.OwnershipMarker) != 64 {
		return out, ErrPCIVFRetirementStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "pci-vf-retirement/"+r.ClaimID); err != nil {
			return err
		}
		var host, device, workload, port, state string
		var generation, portGeneration, bindingGeneration uint64
		var detachQualified bool
		if err := tx.QueryRow(ctx, `SELECT claim.host_id,claim.device_address,claim.workload_id,claim.allocation_generation,coalesce(claim.port_id,''),coalesce(claim.port_generation,0),coalesce(claim.binding_generation,0),claim.claim_state,'VF_DETACH'=ANY(qualification.validated_operations) AND 'VF_READ_BACK'=ANY(qualification.validated_operations)
			FROM kim.pci_vf_allocation_claims claim JOIN kim.pci_qualification_evidence qualification ON qualification.qualification_id=claim.qualification_id AND qualification.qualification_revision=claim.qualification_revision AND qualification.evidence_state='QUALIFIED'
			WHERE claim.claim_id=$1 FOR UPDATE OF claim`, r.ClaimID).Scan(&host, &device, &workload, &generation, &port, &portGeneration, &bindingGeneration, &state, &detachQualified); err != nil || host != r.SourceHostID || device != r.DeviceAddress || workload != r.WorkloadID || generation != r.AllocationGeneration || port != r.PortID || portGeneration != r.PortGeneration || bindingGeneration != r.BindingGeneration || state != "ACTIVE" || !detachQualified {
			return ErrPCIVFRetirementStale
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT operation_id FROM kim.pci_vf_retirement_operations_current WHERE claim_id=$1 AND allocation_generation=$2`, r.ClaimID, r.AllocationGeneration).Scan(&existing)
		if err == nil {
			if existing != r.OperationID {
				return ErrPCIVFRetirementStale
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.pci_vf_retirement_operations_current(claim_id,allocation_generation,operation_id,operation_generation,source_host_id,device_address,port_id,port_generation,binding_generation,workload_id,vm_id,vm_generation,ownership_marker,operation_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'PENDING')`, r.ClaimID, r.AllocationGeneration, r.OperationID, r.OperationGeneration, r.SourceHostID, r.DeviceAddress, r.PortID, r.PortGeneration, r.BindingGeneration, r.WorkloadID, r.VMID, r.VMGeneration, r.OwnershipMarker); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.pci_vf_retirement_latest_current(claim_id,allocation_generation,operation_id,operation_generation,operation_state) VALUES($1,$2,$3,$4,'PENDING') ON CONFLICT(claim_id) DO UPDATE SET allocation_generation=EXCLUDED.allocation_generation,operation_id=EXCLUDED.operation_id,operation_generation=EXCLUDED.operation_generation,operation_state='PENDING',terminal_evidence_id=NULL,updated_at=statement_timestamp() WHERE kim.pci_vf_retirement_latest_current.allocation_generation<EXCLUDED.allocation_generation`, r.ClaimID, r.AllocationGeneration, r.OperationID, r.OperationGeneration)
		return err
	})
	out.PCIVFRetirementRequest = r
	out.State = "PENDING"
	return out, err
}

func ClaimPCIVFRetirement(ctx context.Context, db TxBeginner, claimID string, allocationGeneration uint64, owner string, lease time.Duration) (PCIVFRetirementClaim, error) {
	var out PCIVFRetirementClaim
	if claimID == "" || allocationGeneration == 0 || owner == "" || lease <= 0 || lease > 24*time.Hour {
		return out, ErrPCIVFRetirementStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state string
		var expired bool
		var priorOwner *string
		var last uint64
		if err := tx.QueryRow(ctx, `SELECT operation_state,coalesce(claim_expires_at<=statement_timestamp(),false),claim_owner,last_claim_generation FROM kim.pci_vf_retirement_operations_current WHERE claim_id=$1 AND allocation_generation=$2 FOR UPDATE`, claimID, allocationGeneration).Scan(&state, &expired, &priorOwner, &last); err != nil {
			return err
		}
		if state == "VERIFIED" || state == "CONFLICTING" || state == "STALE" {
			return ErrPCIVFRetirementStale
		}
		mode := "APPLY_ALLOWED"
		if state != "PENDING" {
			mode = "READ_BACK_FIRST"
		}
		if state == "CLAIMED" && !expired {
			return ErrPCIVFRetirementStale
		}
		generation := last + 1
		if err := tx.QueryRow(ctx, `UPDATE kim.pci_vf_retirement_operations_current SET operation_state='CLAIMED',claim_owner=$3,claim_generation=$4,last_claim_generation=$4,claim_expires_at=statement_timestamp()+($5*interval '1 microsecond'),updated_at=statement_timestamp() WHERE claim_id=$1 AND allocation_generation=$2 RETURNING claim_expires_at`, claimID, allocationGeneration, owner, generation, lease.Microseconds()).Scan(&out.ExpiresAt); err != nil {
			return err
		}
		var operationID string
		var operationGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT operation_id,operation_generation FROM kim.pci_vf_retirement_operations_current WHERE claim_id=$1 AND allocation_generation=$2`, claimID, allocationGeneration).Scan(&operationID, &operationGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.pci_vf_retirement_attempt_evidence(claim_id,allocation_generation,claim_generation,claim_owner,claim_mode,lease_expires_at,operation_id,operation_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, claimID, allocationGeneration, generation, owner, mode, out.ExpiresAt, operationID, operationGeneration); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.pci_vf_retirement_latest_current SET operation_state='CLAIMED',updated_at=statement_timestamp() WHERE claim_id=$1 AND allocation_generation=$2`, claimID, allocationGeneration)
		out = PCIVFRetirementClaim{claimID, allocationGeneration, owner, generation, mode, out.ExpiresAt}
		return err
	})
	return out, err
}

// AuthorizePCIVFRetirementCommand constructs the closed payload from current
// PostgreSQL authority. Callers cannot supply XML, argv, sysfs paths, or a
// replacement BDF. Delivery and the Agent mutation remain governed by the
// ordinary Command Lease/session-generation path.
func AuthorizePCIVFRetirementCommand(ctx context.Context, db TxBeginner, claim PCIVFRetirementClaim, jobID, commandID string) (PCIVFRetirementCommand, error) {
	var out PCIVFRetirementCommand
	if jobID == "" || commandID == "" {
		return out, ErrPCIVFRetirementStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var r PCIVFRetirementRequest
		var iommu string
		if err := tx.QueryRow(ctx, `SELECT operation.operation_id,operation.operation_generation,operation.claim_id,operation.allocation_generation,operation.source_host_id,operation.device_address,operation.port_id,operation.workload_id,operation.vm_id::text,operation.ownership_marker,operation.port_generation,operation.binding_generation,operation.vm_generation,pci.iommu_group
			FROM kim.pci_vf_retirement_operations_current operation JOIN kim.host_pci_device_projections pci ON pci.host_id=operation.source_host_id AND pci.device_address=operation.device_address
			WHERE operation.claim_id=$1 AND operation.allocation_generation=$2 AND operation.operation_state='CLAIMED' AND operation.claim_owner=$3 AND operation.claim_generation=$4 AND operation.claim_expires_at>statement_timestamp() FOR UPDATE OF operation`, claim.ClaimID, claim.AllocationGeneration, claim.Owner, claim.ClaimGeneration).Scan(&r.OperationID, &r.OperationGeneration, &r.ClaimID, &r.AllocationGeneration, &r.SourceHostID, &r.DeviceAddress, &r.PortID, &r.WorkloadID, &r.VMID, &r.OwnershipMarker, &r.PortGeneration, &r.BindingGeneration, &r.VMGeneration, &iommu); err != nil || iommu == "" {
			return ErrPCIVFRetirementStale
		}
		payload, _ := json.Marshal(map[string]any{"domain_uuid": r.VMID, "vm_generation": r.VMGeneration, "port_id": r.PortID, "port_generation": r.PortGeneration, "binding_generation": r.BindingGeneration, "source_host_id": r.SourceHostID, "device_address": r.DeviceAddress, "vf_claim_id": r.ClaimID, "allocation_generation": r.AllocationGeneration, "iommu_group": iommu, "ownership_marker": r.OwnershipMarker, "operation_id": r.OperationID, "operation_generation": r.OperationGeneration, "desired_state": "RETIRED"})
		out = PCIVFRetirementCommand{JobID: jobID, CommandID: commandID, PayloadDigest: digestReleaseBytes(payload)}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'PCI_VF_RETIREMENT',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, jobID, r.ClaimID, r.AllocationGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,'PCI_VF_RETIRE','kim.command.pci-vf-retire/v1',$4,$5,$6) ON CONFLICT(command_id) DO NOTHING`, commandID, jobID, r.SourceHostID, "port:"+r.PortID, payload, out.PayloadDigest); err != nil {
			return err
		}
		var accepted string
		if err := tx.QueryRow(ctx, `SELECT payload_digest FROM kim.execution_commands WHERE command_id=$1 AND job_id=$2`, commandID, jobID).Scan(&accepted); err != nil || accepted != out.PayloadDigest {
			return ErrPCIVFRetirementStale
		}
		return nil
	})
	return out, err
}

func CompletePCIVFRetirement(ctx context.Context, db TxBeginner, claim PCIVFRetirementClaim, o PCIVFRetirementObservation) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var r PCIVFRetirementRequest
		if err := tx.QueryRow(ctx, `SELECT operation_id,operation_generation,claim_id,allocation_generation,source_host_id,device_address,port_id,workload_id,vm_id::text,ownership_marker,port_generation,binding_generation,vm_generation FROM kim.pci_vf_retirement_operations_current WHERE claim_id=$1 AND allocation_generation=$2 AND operation_state='CLAIMED' AND claim_owner=$3 AND claim_generation=$4 AND claim_expires_at>statement_timestamp() FOR UPDATE`, claim.ClaimID, claim.AllocationGeneration, claim.Owner, claim.ClaimGeneration).Scan(&r.OperationID, &r.OperationGeneration, &r.ClaimID, &r.AllocationGeneration, &r.SourceHostID, &r.DeviceAddress, &r.PortID, &r.WorkloadID, &r.VMID, &r.OwnershipMarker, &r.PortGeneration, &r.BindingGeneration, &r.VMGeneration); err != nil {
			return ErrPCIVFRetirementStale
		}
		if o.ClaimGeneration != claim.ClaimGeneration || o.OperationID != r.OperationID || o.ClaimID != r.ClaimID || o.AllocationGeneration != r.AllocationGeneration || o.SourceHostID != r.SourceHostID || o.DeviceAddress != r.DeviceAddress || o.PortID != r.PortID || o.PortGeneration != r.PortGeneration || o.BindingGeneration != r.BindingGeneration || o.VMID != r.VMID || o.VMGeneration != r.VMGeneration || o.OwnershipMarker != r.OwnershipMarker || len(o.PCIObservationDigest) != 64 || len(o.LibvirtObservationDigest) != 64 || len(o.EvidenceDigest) != 64 || o.CommandID == "" || o.VerificationID == "" || o.AttemptIndex < 1 || len(o.VerifierDigest) != 64 {
			return ErrPCIVFRetirementStale
		}
		var verificationState string
		if err := tx.QueryRow(ctx, `SELECT verification.verification_state FROM kim.execution_commands command JOIN kim.command_verification_evidence verification ON verification.command_id=command.command_id WHERE command.command_id=$1 AND command.command_type='PCI_VF_RETIRE' AND command.schema_version='kim.command.pci-vf-retire/v1' AND command.host_id=$2 AND command.target_resource_id='port:'||$3 AND verification.verification_id=$4 AND verification.attempt_index=$5 AND verification.verifier_artifact_digest=$6 AND verification.observation_digest=$7`, o.CommandID, r.SourceHostID, r.PortID, o.VerificationID, o.AttemptIndex, o.VerifierDigest, o.LibvirtObservationDigest).Scan(&verificationState); err != nil || (o.ResultState == "VERIFIED" && verificationState != "MATCHED") || (o.ResultState == "UNKNOWN" && verificationState != "UNKNOWN") || (o.ResultState == "CONFLICTING" && verificationState != "CONFLICTING") {
			return ErrPCIVFRetirementStale
		}
		positive := o.OwnershipMarkerMatches && o.SourceDomainNotRunning && o.SourceHostdevAbsent && o.VFDriverReleased && o.VFHolderAbsent && o.IOMMUGroupMatches
		if o.ResultState == "VERIFIED" && !positive {
			return ErrPCIVFRetirementStale
		}
		if o.ResultState != "VERIFIED" && o.ResultState != "CONFLICTING" && o.ResultState != "UNKNOWN" {
			return ErrPCIVFRetirementStale
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.pci_vf_retirement_evidence(evidence_id,claim_id,allocation_generation,claim_generation,operation_id,operation_generation,source_host_id,device_address,port_id,port_generation,binding_generation,vm_id,vm_generation,ownership_marker,ownership_marker_matches,source_domain_not_running,source_hostdev_absent,vf_driver_released,vf_holder_absent,iommu_group_matches,pci_observation_generation,pci_observation_digest,libvirt_observation_generation,libvirt_observation_digest,command_id,attempt_index,verification_id,verifier_digest,apply_response_state,result_state,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31)`, o.EvidenceID, o.ClaimID, o.AllocationGeneration, o.ClaimGeneration, o.OperationID, o.OperationGeneration, o.SourceHostID, o.DeviceAddress, o.PortID, o.PortGeneration, o.BindingGeneration, o.VMID, o.VMGeneration, o.OwnershipMarker, o.OwnershipMarkerMatches, o.SourceDomainNotRunning, o.SourceHostdevAbsent, o.VFDriverReleased, o.VFHolderAbsent, o.IOMMUGroupMatches, o.PCIObservationGeneration, o.PCIObservationDigest, o.LibvirtObservationGeneration, o.LibvirtObservationDigest, o.CommandID, o.AttemptIndex, o.VerificationID, o.VerifierDigest, o.ApplyResponseState, o.ResultState, o.EvidenceDigest); err != nil {
			return err
		}
		state := "DISPATCH_UNKNOWN"
		var terminal any = nil
		if o.ResultState == "VERIFIED" {
			state = "VERIFIED"
			terminal = o.EvidenceID
		} else if o.ResultState == "CONFLICTING" {
			state = "CONFLICTING"
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.pci_vf_retirement_operations_current SET operation_state=$5,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,terminal_evidence_id=$6,updated_at=statement_timestamp() WHERE claim_id=$1 AND allocation_generation=$2 AND claim_owner=$3 AND claim_generation=$4`, claim.ClaimID, claim.AllocationGeneration, claim.Owner, claim.ClaimGeneration, state, terminal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.pci_vf_retirement_latest_current SET operation_state=$3,terminal_evidence_id=$4,updated_at=statement_timestamp() WHERE claim_id=$1 AND allocation_generation=$2`, claim.ClaimID, claim.AllocationGeneration, state, terminal); err != nil {
			return err
		}
		if state == "VERIFIED" {
			_, err := tx.Exec(ctx, `UPDATE kim.pci_vf_allocation_claims SET claim_state='RELEASE_PENDING' WHERE claim_id=$1 AND allocation_generation=$2 AND claim_state='ACTIVE'`, claim.ClaimID, claim.AllocationGeneration)
			return err
		}
		return nil
	})
}

func CommitPCIVFHandoff(ctx context.Context, db TxBeginner, r PCIVFHandoffRequest) error {
	if r.HandoffID == "" || r.SourceClaimID == "" || r.DestinationClaimID == "" || r.SourceAllocationGeneration == 0 || r.DestinationAllocationGeneration == 0 || r.SourceHostID == r.DestinationHostID {
		return ErrPCIVFRetirementStale
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error { return commitPCIVFHandoffTx(ctx, tx, r) })
}

func commitPCIVFHandoffTx(ctx context.Context, tx pgx.Tx, r PCIVFHandoffRequest) error {
	if r.HandoffID == "" || r.SourceClaimID == "" || r.DestinationClaimID == "" || r.SourceAllocationGeneration == 0 || r.DestinationAllocationGeneration == 0 || r.SourceHostID == r.DestinationHostID {
		return ErrPCIVFRetirementStale
	}
	var verified bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.pci_vf_retirement_evidence e JOIN kim.pci_vf_retirement_operations_current c ON c.claim_id=e.claim_id AND c.allocation_generation=e.allocation_generation AND c.terminal_evidence_id=e.evidence_id WHERE e.evidence_id=$1 AND e.claim_id=$2 AND e.allocation_generation=$3 AND e.source_host_id=$4 AND e.device_address=$5 AND e.result_state='VERIFIED' AND c.operation_state='VERIFIED')`, r.SourceRetirementEvidenceID, r.SourceClaimID, r.SourceAllocationGeneration, r.SourceHostID, r.SourceDeviceAddress).Scan(&verified); err != nil || !verified {
		return ErrPCIVFRetirementStale
	}
	var workload, host, device, state string
	var generation uint64
	if err := tx.QueryRow(ctx, `SELECT workload_id,host_id,device_address,allocation_generation,claim_state FROM kim.pci_vf_allocation_claims WHERE claim_id=$1 FOR SHARE`, r.DestinationClaimID).Scan(&workload, &host, &device, &generation, &state); err != nil || workload != r.WorkloadID || host != r.DestinationHostID || device != r.DestinationDeviceAddress || generation != r.DestinationAllocationGeneration || state != "ACTIVE" {
		return ErrPCIVFRetirementStale
	}
	raw, _ := json.Marshal(r)
	digest := digestReleaseBytes(raw)
	if _, err := tx.Exec(ctx, `INSERT INTO kim.pci_vf_handoff_evidence(handoff_id,workload_id,port_id,port_generation,source_claim_id,source_allocation_generation,source_host_id,source_device_address,source_retirement_evidence_id,destination_claim_id,destination_allocation_generation,destination_host_id,destination_device_address,destination_admission_id,handoff_state,handoff_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'DESTINATION_RESERVED',$15) ON CONFLICT(handoff_id) DO NOTHING`, r.HandoffID, r.WorkloadID, r.PortID, r.PortGeneration, r.SourceClaimID, r.SourceAllocationGeneration, r.SourceHostID, r.SourceDeviceAddress, r.SourceRetirementEvidenceID, r.DestinationClaimID, r.DestinationAllocationGeneration, r.DestinationHostID, r.DestinationDeviceAddress, r.DestinationAdmissionID, digest); err != nil {
		return err
	}
	var acceptedDigest string
	if err := tx.QueryRow(ctx, `SELECT handoff_digest FROM kim.pci_vf_handoff_evidence WHERE handoff_id=$1`, r.HandoffID).Scan(&acceptedDigest); err != nil || acceptedDigest != digest {
		return ErrPCIVFRetirementStale
	}
	_, err := tx.Exec(ctx, `INSERT INTO kim.pci_vf_handoffs_current(handoff_id,workload_id,port_id,port_generation,source_claim_id,source_allocation_generation,destination_claim_id,destination_allocation_generation,handoff_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'DESTINATION_RESERVED') ON CONFLICT(handoff_id) DO NOTHING`, r.HandoffID, r.WorkloadID, r.PortID, r.PortGeneration, r.SourceClaimID, r.SourceAllocationGeneration, r.DestinationClaimID, r.DestinationAllocationGeneration)
	if err != nil {
		return err
	}
	var currentMatches bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.pci_vf_handoffs_current WHERE handoff_id=$1 AND source_claim_id=$2 AND source_allocation_generation=$3 AND destination_claim_id=$4 AND destination_allocation_generation=$5)`, r.HandoffID, r.SourceClaimID, r.SourceAllocationGeneration, r.DestinationClaimID, r.DestinationAllocationGeneration).Scan(&currentMatches); err != nil || !currentMatches {
		return ErrPCIVFRetirementStale
	}
	var sourceState string
	if err := tx.QueryRow(ctx, `SELECT claim_state FROM kim.pci_vf_allocation_claims WHERE claim_id=$1 AND allocation_generation=$2 FOR UPDATE`, r.SourceClaimID, r.SourceAllocationGeneration).Scan(&sourceState); err != nil {
		return err
	}
	if sourceState == "RELEASE_PENDING" {
		if _, err := tx.Exec(ctx, `UPDATE kim.pci_vf_allocation_claims SET claim_state='RELEASED',released_at=statement_timestamp() WHERE claim_id=$1 AND allocation_generation=$2`, r.SourceClaimID, r.SourceAllocationGeneration); err != nil {
			return err
		}
	} else if sourceState != "RELEASED" {
		return ErrPCIVFRetirementStale
	}
	return nil
}
