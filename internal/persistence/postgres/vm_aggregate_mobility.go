package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type VMAggregateMobilityAssociationRequest struct {
	AssociationID, VMID, MobilityKind, MobilityTerminalEvidenceID string
}

type VMAggregateMobilityAssociation struct {
	AssociationID, VMID, MobilityKind, MobilityTerminalEvidenceID string
	SourceAdmissionID, SourceHostID, SourcePlanID                 string
	DestinationAdmissionID, DestinationHostID, DestinationPlanID  string
	DependencySnapshotID, DependencyDigest, DesiredDigest         string
	AssociationDigest                                             string
	VMRevision, RuntimeIntentGeneration                           uint64
	AssociationGeneration, SourceMaterializationGeneration        uint64
	DestinationMaterializationGeneration                          uint64
}

type mobilityTerminalProjection struct {
	terminalDigest, sourceAdmission, sourceHost, sourcePlan string
	destinationAdmission, destinationHost, destinationPlan  string
	destinationPlanDigest, networkDigest, powerEvidence     string
	sourceVMGeneration, sourceMaterialization               uint64
	destinationVMGeneration, destinationMaterialization     uint64
	readinessGeneration, powerGeneration                    uint64
}

func loadAggregateMobilityTerminalTx(ctx context.Context, tx pgx.Tx, r VMAggregateMobilityAssociationRequest) (mobilityTerminalProjection, error) {
	var p mobilityTerminalProjection
	var err error
	switch r.MobilityKind {
	case "RECOVERY":
		err = tx.QueryRow(ctx, `SELECT t.decision_digest,release.source_admission_id,operation.source_host_id,retirement.source_plan_id,retirement.vm_generation,retirement.source_materialization_generation,v.destination_admission_id,v.destination_host_id,m.vm_plan_id,m.vm_plan_digest,m.vm_generation,m.materialization_generation,ready.observation_generation,COALESCE(ready.network_evidence_set_digest,''),v.power_evidence_id,v.power_observation_generation
			FROM kim.recovery_terminal_decision_evidence t
			JOIN kim.recovery_operation_evidence operation ON operation.recovery_operation_id=t.recovery_operation_id
			JOIN kim.recovery_verification_evidence v ON v.verification_id=t.verification_id AND v.verification_digest=t.verification_digest AND v.result_state='VERIFIED' AND v.vm_id=$2
			JOIN kim.recovery_materialization_evidence m ON m.materialization_id=v.materialization_id AND m.recovery_operation_id=t.recovery_operation_id AND m.destination_admission_id=v.destination_admission_id AND m.destination_host_id=v.destination_host_id
			JOIN kim.recovery_source_compute_release_evidence release ON release.recovery_operation_id=t.recovery_operation_id
			JOIN kim.source_materialization_retirement_decision_evidence retirement ON retirement.failure_epoch_id=t.failure_epoch_id AND retirement.vm_id=v.vm_id AND retirement.source_host_id=operation.source_host_id
			JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=m.vm_id AND ready.vm_generation=m.vm_generation AND ready.plan_id=m.vm_plan_id AND ready.observation_generation=v.network_observation_generation AND ready.boot_readiness='READY'
			JOIN kim.vm_power_state_current power ON power.vm_id=v.vm_id AND power.vm_generation=v.vm_generation AND power.evidence_id=v.power_evidence_id AND power.observation_generation=v.power_observation_generation AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED'
			WHERE t.terminal_decision_id=$1 AND t.decision_state='VERIFIED'`, r.MobilityTerminalEvidenceID, r.VMID).Scan(&p.terminalDigest, &p.sourceAdmission, &p.sourceHost, &p.sourcePlan, &p.sourceVMGeneration, &p.sourceMaterialization, &p.destinationAdmission, &p.destinationHost, &p.destinationPlan, &p.destinationPlanDigest, &p.destinationVMGeneration, &p.destinationMaterialization, &p.readinessGeneration, &p.networkDigest, &p.powerEvidence, &p.powerGeneration)
	case "HOST_EVACUATION":
		err = tx.QueryRow(ctx, `SELECT parent.decision_digest,w.source_admission_id,w.source_host_id,w.source_plan_id,w.vm_generation,w.source_materialization_generation,child.destination_admission_id,child.destination_host_id,b.destination_plan_id,b.destination_plan_digest,b.vm_generation,v.destination_materialization_generation,b.materialization_observation_generation,COALESCE(ready.network_evidence_set_digest,''),b.power_evidence_id,b.power_observation_generation
			FROM kim.host_evacuation_terminal_evidence parent
			JOIN kim.host_evacuation_operation_evidence operation ON (operation.evacuation_operation_id,operation.evacuation_generation)=(parent.evacuation_operation_id,parent.evacuation_generation) AND operation.workload_set_digest=parent.workload_set_digest
			JOIN kim.host_evacuation_workload_evidence w ON (w.workload_set_id,w.workload_set_generation)=(operation.workload_set_id,operation.workload_set_generation) AND w.vm_id=$2
			JOIN kim.host_evacuation_child_terminal_evidence child ON child.child_operation_id=w.child_operation_id AND child.child_generation=w.child_generation AND child.terminal_state='VERIFIED'
			JOIN kim.host_evacuation_child_verification_evidence v ON v.verification_id=child.child_verification_id AND v.verification_digest=child.child_verification_digest
			JOIN kim.host_evacuation_destination_evidence_binding b ON b.destination_binding_id=v.destination_binding_id AND b.destination_admission_id=child.destination_admission_id AND b.destination_host_id=child.destination_host_id
			JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=b.vm_id AND ready.vm_generation=b.vm_generation AND ready.plan_id=b.destination_plan_id AND ready.observation_generation=b.materialization_observation_generation AND ready.boot_readiness='READY'
			JOIN kim.vm_power_state_current power ON power.vm_id=b.vm_id AND power.vm_generation=b.vm_generation AND power.evidence_id=b.power_evidence_id AND power.observation_generation=b.power_observation_generation AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED'
			WHERE parent.terminal_evidence_id=$1 AND parent.terminal_state='VERIFIED'`, r.MobilityTerminalEvidenceID, r.VMID).Scan(&p.terminalDigest, &p.sourceAdmission, &p.sourceHost, &p.sourcePlan, &p.sourceVMGeneration, &p.sourceMaterialization, &p.destinationAdmission, &p.destinationHost, &p.destinationPlan, &p.destinationPlanDigest, &p.destinationVMGeneration, &p.destinationMaterialization, &p.readinessGeneration, &p.networkDigest, &p.powerEvidence, &p.powerGeneration)
	default:
		return p, ErrVMAggregateConflict
	}
	if err != nil {
		return p, ErrVMAggregateConflict
	}
	return p, nil
}

// AssociateVMAggregateMobility consumes an already VERIFIED mobility terminal.
// It only advances rebuildable aggregate runtime pointers; logical VM/Port/
// Volume revision and dependency snapshot authority are read-only invariants.
func AssociateVMAggregateMobility(ctx context.Context, db TxBeginner, r VMAggregateMobilityAssociationRequest) (VMAggregateMobilityAssociation, error) {
	var out VMAggregateMobilityAssociation
	if r.AssociationID == "" || !vmUUIDPattern.MatchString(r.VMID) || r.MobilityTerminalEvidenceID == "" || (r.MobilityKind != "RECOVERY" && r.MobilityKind != "HOST_EVACUATION") {
		return out, ErrVMAggregateConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "vm-resource/"+r.VMID); err != nil {
			return err
		}
		var existingKind, existingTerminal, existingDigest string
		var existingVM string
		if err := tx.QueryRow(ctx, `SELECT vm_id::text,mobility_kind,mobility_terminal_evidence_id,association_digest FROM kim.vm_aggregate_mobility_association_evidence WHERE association_id=$1`, r.AssociationID).Scan(&existingVM, &existingKind, &existingTerminal, &existingDigest); err == nil {
			if existingVM != r.VMID || existingKind != r.MobilityKind || existingTerminal != r.MobilityTerminalEvidenceID {
				return ErrVMAggregateConflict
			}
			return tx.QueryRow(ctx, `SELECT association_id,vm_id::text,mobility_kind,mobility_terminal_evidence_id,source_admission_id,source_host_id,source_plan_id,destination_admission_id,destination_host_id,destination_plan_id,dependency_snapshot_id,dependency_digest,desired_digest,association_digest,vm_revision,runtime_intent_generation,association_generation,source_materialization_generation,destination_materialization_generation FROM kim.vm_aggregate_mobility_association_evidence WHERE association_id=$1 AND association_digest=$2`, r.AssociationID, existingDigest).Scan(&out.AssociationID, &out.VMID, &out.MobilityKind, &out.MobilityTerminalEvidenceID, &out.SourceAdmissionID, &out.SourceHostID, &out.SourcePlanID, &out.DestinationAdmissionID, &out.DestinationHostID, &out.DestinationPlanID, &out.DependencySnapshotID, &out.DependencyDigest, &out.DesiredDigest, &out.AssociationDigest, &out.VMRevision, &out.RuntimeIntentGeneration, &out.AssociationGeneration, &out.SourceMaterializationGeneration, &out.DestinationMaterializationGeneration)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var vmRevision, runtimeGeneration, associationGeneration, sourceVMGeneration, sourceMaterialization uint64
		var dependencyID, dependencyDigest, desiredDigest, aggregateTerminal, sourceAdmission, sourceHost, sourcePlan string
		if err := tx.QueryRow(ctx, `SELECT resource.vm_revision,resource.runtime_intent_generation,intent.dependency_snapshot_id,snapshot.dependency_digest,resource.desired_digest,binding.terminal_evidence_id,binding.admission_id,binding.host_id,binding.vm_generation,binding.plan_id,binding.materialization_generation,binding.mobility_association_generation FROM kim.vm_resources_current resource JOIN kim.vm_runtime_intent_evidence intent ON(intent.vm_id,intent.runtime_intent_generation)=(resource.vm_id,resource.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence snapshot ON snapshot.dependency_snapshot_id=intent.dependency_snapshot_id JOIN kim.vm_resource_runtime_bindings_current binding ON binding.vm_id=resource.vm_id WHERE resource.vm_id=$1 AND resource.lifecycle_state='ACTIVE' AND resource.convergence_state='CONVERGED' FOR UPDATE OF resource,binding`, r.VMID).Scan(&vmRevision, &runtimeGeneration, &dependencyID, &dependencyDigest, &desiredDigest, &aggregateTerminal, &sourceAdmission, &sourceHost, &sourceVMGeneration, &sourcePlan, &sourceMaterialization, &associationGeneration); err != nil {
			return ErrVMAggregateConflict
		}
		terminal, err := loadAggregateMobilityTerminalTx(ctx, tx, r)
		if err != nil || terminal.sourceAdmission != sourceAdmission || terminal.sourceHost != sourceHost || terminal.sourceVMGeneration != sourceVMGeneration || terminal.sourcePlan != sourcePlan || terminal.sourceMaterialization != sourceMaterialization || terminal.destinationHost == sourceHost {
			return ErrVMAggregateConflict
		}
		var desiredCurrent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_resource_revision_evidence e JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.vm_revision)=(e.vm_id,e.vm_revision) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id WHERE e.vm_id=$1 AND e.vm_revision=$2 AND e.desired_digest=$3 AND i.runtime_intent_generation=$4 AND s.dependency_digest=$5)`, r.VMID, vmRevision, desiredDigest, runtimeGeneration, dependencyDigest).Scan(&desiredCurrent); err != nil || !desiredCurrent {
			return ErrVMAggregateConflict
		}
		var portCount int
		if err := tx.QueryRow(ctx, `SELECT port_count FROM kim.vm_dependency_snapshot_evidence WHERE dependency_snapshot_id=$1`, dependencyID).Scan(&portCount); err != nil || portCount < 0 || portCount > 1 {
			return ErrVMAggregateConflict
		}
		portSet := ""
		if portCount == 0 {
			if terminal.networkDigest != digestBytes([]byte("[]")) {
				return ErrVMAggregateConflict
			}
		} else {
			if err := tx.QueryRow(ctx, `SELECT current.evidence_id||':'||e.observation_digest FROM kim.vm_dependency_port_evidence d JOIN kim.network_ports_current p ON p.port_id=d.port_id AND p.port_revision=d.port_revision AND p.desired_digest=d.desired_digest AND p.placement_admission_id=$2 AND p.desired_state='RESERVED' JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=$2 AND b.host_id=$3 AND b.binding_state='RESERVED' JOIN kim.vm_network_port_realizations_current current ON current.vm_id=$4 AND current.vm_generation=$5 AND current.port_id=p.port_id AND current.binding_generation=b.binding_generation AND current.preboot_state='REALIZED' JOIN kim.vm_network_port_realization_evidence e ON e.evidence_id=current.evidence_id AND e.plan_id=$6 AND e.host_id=$3 AND e.binding_generation=b.binding_generation AND e.preboot_state='REALIZED' WHERE d.dependency_snapshot_id=$1`, dependencyID, terminal.destinationAdmission, terminal.destinationHost, r.VMID, terminal.destinationVMGeneration, terminal.destinationPlan).Scan(&portSet); err != nil || digestBytes([]byte(portSet)) != terminal.networkDigest {
				return ErrVMAggregateConflict
			}
		}
		var destinationCurrent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id AND plan.plan_digest=$5 JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.plan_id=plan.plan_id AND ready.observation_generation=$6 AND ready.network_evidence_set_digest=$7 AND ready.boot_readiness='READY' JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation AND power.evidence_id=$8 AND power.observation_generation=$9 AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED' WHERE vm.vm_id=$1 AND vm.host_id=$2 AND vm.placement_admission_id=$3 AND vm.vm_generation=$4)`, r.VMID, terminal.destinationHost, terminal.destinationAdmission, terminal.destinationVMGeneration, terminal.destinationPlanDigest, terminal.readinessGeneration, terminal.networkDigest, terminal.powerEvidence, terminal.powerGeneration).Scan(&destinationCurrent); err != nil || !destinationCurrent {
			return ErrVMAggregateConflict
		}
		next := associationGeneration + 1
		associationDigest := digestVMAggregate(map[string]any{"association_id": r.AssociationID, "generation": next, "kind": r.MobilityKind, "terminal_id": r.MobilityTerminalEvidenceID, "terminal_digest": terminal.terminalDigest, "vm_id": r.VMID, "vm_revision": vmRevision, "runtime_generation": runtimeGeneration, "dependency_digest": dependencyDigest, "desired_digest": desiredDigest, "source_admission": sourceAdmission, "source_host": sourceHost, "source_plan": sourcePlan, "source_materialization": sourceMaterialization, "destination_admission": terminal.destinationAdmission, "destination_host": terminal.destinationHost, "destination_plan": terminal.destinationPlan, "destination_plan_digest": terminal.destinationPlanDigest, "destination_materialization": terminal.destinationMaterialization, "network_digest": terminal.networkDigest, "power_evidence": terminal.powerEvidence, "port_set": portSet})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_aggregate_mobility_association_evidence(association_id,association_generation,mobility_kind,mobility_terminal_evidence_id,mobility_terminal_digest,vm_id,vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,desired_digest,source_aggregate_terminal_id,source_admission_id,source_host_id,source_vm_generation,source_plan_id,source_materialization_generation,destination_admission_id,destination_host_id,destination_vm_generation,destination_plan_id,destination_plan_digest,destination_materialization_generation,destination_readiness_observation_generation,destination_network_evidence_set_digest,destination_power_evidence_id,destination_power_observation_generation,port_count,port_evidence_set_digest,association_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)`, r.AssociationID, next, r.MobilityKind, r.MobilityTerminalEvidenceID, terminal.terminalDigest, r.VMID, vmRevision, runtimeGeneration, dependencyID, dependencyDigest, desiredDigest, aggregateTerminal, sourceAdmission, sourceHost, sourceVMGeneration, sourcePlan, sourceMaterialization, terminal.destinationAdmission, terminal.destinationHost, terminal.destinationVMGeneration, terminal.destinationPlan, terminal.destinationPlanDigest, terminal.destinationMaterialization, terminal.readinessGeneration, terminal.networkDigest, terminal.powerEvidence, terminal.powerGeneration, portCount, digestBytes([]byte(portSet)), associationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_aggregate_mobility_associations_current(vm_id,association_generation,association_id,mobility_kind,mobility_terminal_evidence_id,destination_admission_id,destination_host_id,destination_vm_generation,destination_plan_id,destination_materialization_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(vm_id) DO UPDATE SET association_generation=EXCLUDED.association_generation,association_id=EXCLUDED.association_id,mobility_kind=EXCLUDED.mobility_kind,mobility_terminal_evidence_id=EXCLUDED.mobility_terminal_evidence_id,destination_admission_id=EXCLUDED.destination_admission_id,destination_host_id=EXCLUDED.destination_host_id,destination_vm_generation=EXCLUDED.destination_vm_generation,destination_plan_id=EXCLUDED.destination_plan_id,destination_materialization_generation=EXCLUDED.destination_materialization_generation,updated_at=statement_timestamp() WHERE kim.vm_aggregate_mobility_associations_current.association_generation+1=EXCLUDED.association_generation`, r.VMID, next, r.AssociationID, r.MobilityKind, r.MobilityTerminalEvidenceID, terminal.destinationAdmission, terminal.destinationHost, terminal.destinationVMGeneration, terminal.destinationPlan, terminal.destinationMaterialization); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE kim.vm_resource_runtime_bindings_current SET admission_id=$2,host_id=$3,vm_generation=$4,plan_id=$5,materialization_generation=$6,mobility_association_generation=$7,mobility_association_id=$8,updated_at=statement_timestamp() WHERE vm_id=$1 AND admission_id=$9 AND host_id=$10 AND vm_generation=$11 AND plan_id=$12 AND materialization_generation=$13 AND mobility_association_generation=$14`, r.VMID, terminal.destinationAdmission, terminal.destinationHost, terminal.destinationVMGeneration, terminal.destinationPlan, terminal.destinationMaterialization, next, r.AssociationID, sourceAdmission, sourceHost, sourceVMGeneration, sourcePlan, sourceMaterialization, associationGeneration)
		if err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		out = VMAggregateMobilityAssociation{AssociationID: r.AssociationID, VMID: r.VMID, MobilityKind: r.MobilityKind, MobilityTerminalEvidenceID: r.MobilityTerminalEvidenceID, SourceAdmissionID: sourceAdmission, SourceHostID: sourceHost, SourcePlanID: sourcePlan, DestinationAdmissionID: terminal.destinationAdmission, DestinationHostID: terminal.destinationHost, DestinationPlanID: terminal.destinationPlan, DependencySnapshotID: dependencyID, DependencyDigest: dependencyDigest, DesiredDigest: desiredDigest, AssociationDigest: associationDigest, VMRevision: vmRevision, RuntimeIntentGeneration: runtimeGeneration, AssociationGeneration: next, SourceMaterializationGeneration: sourceMaterialization, DestinationMaterializationGeneration: terminal.destinationMaterialization}
		return nil
	})
	return out, err
}
