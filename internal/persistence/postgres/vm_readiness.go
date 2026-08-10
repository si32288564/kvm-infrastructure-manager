package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type VMDefinitionObservation struct {
	EvidenceID, VMID, PlanID, PlanDigest, HostID     string
	CommandID, VerificationID                        string
	ObservationDigest, VerifierDigest, EvidenceState string
	VMGeneration, ObservationGeneration              uint64
	AttemptIndex                                     uint32
	DomainPresent, DomainIdentityMatches             bool
	PlanIdentityMatches, ComputeShapeMatches         bool
	RootVolumeIdentityMatches                        bool
}

// AcceptVMDefinitionObservation advances only the Domain component of VM
// materialization readiness. Image and Network remain independently blocked.
func AcceptVMDefinitionObservation(ctx context.Context, db TxBeginner, observation VMDefinitionObservation) error {
	if err := validateVMDefinitionObservation(observation); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, observation.HostID); err != nil {
			return err
		}
		var acceptedPlanDigest string
		if err := tx.QueryRow(ctx, `
			SELECT plan.plan_digest
			FROM kim.virtual_machines_current vm
			JOIN kim.vm_materialization_plan_evidence plan
			  ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id
			 AND plan.vm_generation=vm.vm_generation AND plan.host_id=vm.host_id
			JOIN kim.volume_backend_bindings_current binding
			  ON binding.binding_id=plan.root_binding_id
			 AND binding.binding_generation=plan.root_binding_generation
			 AND binding.volume_id=plan.root_volume_id AND binding.host_id=vm.host_id
			 AND binding.binding_state='BOUND'
			JOIN kim.volume_attachment_claims claim
			  ON claim.attachment_id=plan.root_attachment_id
			 AND claim.attachment_generation=plan.root_attachment_generation
			 AND claim.volume_id=plan.root_volume_id AND claim.host_id=vm.host_id
			 AND claim.claim_state='RESERVED'
			JOIN kim.execution_commands command
			  ON command.command_id=$6 AND command.job_id IN (
			      SELECT job_id FROM kim.execution_jobs
			      WHERE resource_type='VIRTUAL_MACHINE' AND resource_id=vm.vm_id::text
			  )
			 AND command.host_id=vm.host_id
			 AND command.command_type='VIRTUAL_MACHINE_DEFINE'
			 AND command.schema_version='kim.command.virtual-machine-define/v1'
			 AND command.target_resource_id='vm:' || vm.vm_id::text
			 AND command.payload_digest=plan.plan_digest
			JOIN kim.command_verification_evidence verification
			  ON verification.verification_id=$7 AND verification.command_id=command.command_id
			 AND verification.attempt_index=$8 AND verification.observation_generation=$9
			 AND verification.observation_digest=$10 AND verification.verification_state='MATCHED'
			 AND verification.verifier_artifact_digest=$11
			WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.current_plan_id=$3
			  AND vm.host_id=$4 AND plan.plan_digest=$5
			  AND (verification.evidence_payload->>'domain_uuid')=vm.vm_id::text
			  AND (verification.evidence_payload->>'materialization_generation')::bigint=vm.vm_generation
			  AND verification.evidence_payload->>'plan_digest'=plan.plan_digest
			  AND (verification.evidence_payload->>'domain_present')::boolean=$12
			  AND (verification.evidence_payload->>'domain_identity_matches')::boolean=$13
			  AND (verification.evidence_payload->>'plan_identity_matches')::boolean=$14
			  AND (verification.evidence_payload->>'compute_shape_matches')::boolean=$15
			  AND (verification.evidence_payload->>'root_volume_identity_matches')::boolean=$16
			  AND verification.evidence_payload->>'image_materialization_state'='PENDING'
			  AND verification.evidence_payload->>'network_realization_state'='PENDING'
			FOR UPDATE OF vm, binding, claim
		`, observation.VMID, observation.VMGeneration, observation.PlanID,
			observation.HostID, observation.PlanDigest, observation.CommandID,
			observation.VerificationID, observation.AttemptIndex,
			observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.DomainPresent,
			observation.DomainIdentityMatches, observation.PlanIdentityMatches,
			observation.ComputeShapeMatches, observation.RootVolumeIdentityMatches).Scan(&acceptedPlanDigest); err != nil || acceptedPlanDigest != observation.PlanDigest {
			return ErrVMMaterializationConflict
		}
		if !observation.DomainPresent || !observation.DomainIdentityMatches || !observation.PlanIdentityMatches || !observation.ComputeShapeMatches || !observation.RootVolumeIdentityMatches {
			return ErrVMMaterializationConflict
		}
		var currentEvidenceID string
		var currentGeneration uint64
		err := tx.QueryRow(ctx, `SELECT definition_evidence_id,observation_generation FROM kim.vm_materialization_readiness_current WHERE vm_id=$1 FOR UPDATE`, observation.VMID).Scan(&currentEvidenceID, &currentGeneration)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && (observation.ObservationGeneration < currentGeneration || (observation.ObservationGeneration == currentGeneration && currentEvidenceID != observation.EvidenceID)) {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.vm_definition_observation_evidence (
				evidence_id,vm_id,vm_generation,plan_id,plan_digest,host_id,
				command_id,attempt_index,verification_id,observation_generation,
				observation_digest,verifier_digest,domain_present,
				domain_identity_matches,plan_identity_matches,compute_shape_matches,
				root_volume_identity_matches,evidence_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT (evidence_id) DO NOTHING
		`, observation.EvidenceID, observation.VMID, observation.VMGeneration,
			observation.PlanID, observation.PlanDigest, observation.HostID,
			observation.CommandID, observation.AttemptIndex, observation.VerificationID,
			observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.DomainPresent,
			observation.DomainIdentityMatches, observation.PlanIdentityMatches,
			observation.ComputeShapeMatches, observation.RootVolumeIdentityMatches,
			observation.EvidenceState); err != nil {
			return err
		}
		var evidenceMatches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM kim.vm_definition_observation_evidence
			WHERE evidence_id=$1 AND vm_id=$2 AND vm_generation=$3 AND plan_id=$4
			  AND plan_digest=$5 AND host_id=$6 AND command_id=$7
			  AND attempt_index=$8 AND verification_id=$9
			  AND observation_generation=$10 AND observation_digest=$11
			  AND verifier_digest=$12 AND domain_present=$13
			  AND domain_identity_matches=$14 AND plan_identity_matches=$15
			  AND compute_shape_matches=$16 AND root_volume_identity_matches=$17
			  AND evidence_state=$18
		)`, observation.EvidenceID, observation.VMID, observation.VMGeneration,
			observation.PlanID, observation.PlanDigest, observation.HostID,
			observation.CommandID, observation.AttemptIndex, observation.VerificationID,
			observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.DomainPresent,
			observation.DomainIdentityMatches, observation.PlanIdentityMatches,
			observation.ComputeShapeMatches, observation.RootVolumeIdentityMatches,
			observation.EvidenceState).Scan(&evidenceMatches); err != nil || !evidenceMatches {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.vm_materialization_readiness_current (
				vm_id,vm_generation,plan_id,observation_generation,
				definition_evidence_id,domain_state,image_state,network_state,
				storage_state,boot_readiness,blocking_reasons
			) VALUES ($1,$2,$3,$4,$5,'DEFINED','PENDING','PENDING','BOUND','BLOCKED',ARRAY['image_pending','network_pending'])
			ON CONFLICT (vm_id) DO UPDATE SET
				observation_generation=EXCLUDED.observation_generation,
				definition_evidence_id=EXCLUDED.definition_evidence_id,
				domain_state='DEFINED',storage_state='BOUND',
				updated_at=statement_timestamp()
			WHERE kim.vm_materialization_readiness_current.vm_generation=EXCLUDED.vm_generation
			  AND kim.vm_materialization_readiness_current.observation_generation < EXCLUDED.observation_generation
		`, observation.VMID, observation.VMGeneration, observation.PlanID,
			observation.ObservationGeneration, observation.EvidenceID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET lifecycle_state='DEFINED',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$2`, observation.VMID, observation.VMGeneration)
		return err
	})
}

func validateVMDefinitionObservation(value VMDefinitionObservation) error {
	if value.EvidenceID == "" || !vmUUIDPattern.MatchString(value.VMID) || value.PlanID == "" || len(value.PlanDigest) != 64 || value.HostID == "" || value.CommandID == "" || value.VerificationID == "" || value.VMGeneration == 0 || value.AttemptIndex == 0 || value.ObservationGeneration == 0 || len(value.ObservationDigest) != 64 || len(value.VerifierDigest) != 64 || value.EvidenceState != "MATCHED" {
		return errors.New("complete MATCHED VM definition observation is required")
	}
	return nil
}
