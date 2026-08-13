package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestHostEvacuationAuthorityPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('evacuation-integration',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	hostID, poolID := "evacuation-source-"+suffix, "evacuation-pool-"+suffix
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO kim.host_identities(host_id,enrollment_state,host_authority_generation) VALUES($1,'APPROVED',1)`, []any{hostID}},
		{`INSERT INTO kim.placement_pools_current(pool_id,pool_generation,lifecycle_state,policy_id,policy_generation) VALUES($1,1,'ACTIVE','evacuation-placement',1)`, []any{poolID}},
		{`INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$2,1,'ACTIVE')`, []any{hostID, poolID}},
		{`INSERT INTO kim.host_operation_authorities_current(host_id,authority_generation,authority_state,session_generation,credential_binding_revision,enrollment_decision_revision,capability_generation,baseline_assignment_generation,preflight_generation,compliance_generation,policy_id,policy_generation,armed_by,reason_code) VALUES($1,1,'ARMED',1,1,1,1,1,1,1,'evacuation-host-operation',1,'integration','planned_evacuation_fixture')`, []any{hostID}},
	} {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	request := HostEvacuationRequest{
		OperationID: "host-evacuation-" + suffix, SourceHostID: hostID,
		EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1,
		DrainPolicyID: "planned-maintenance", DrainPolicyRevision: 1,
		EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 2,
		Reason: "integration qualification", RequestedBy: "integration",
	}
	invalidConcurrency := request
	invalidConcurrency.OperationID += "-invalid-concurrency"
	invalidConcurrency.MaximumConcurrentWorkloads = 0
	if _, _, err := StartHostEvacuation(ctx, pool, invalidConcurrency); !errors.Is(err, ErrHostEvacuationConflict) {
		t.Fatalf("invalid concurrency error = %v", err)
	}
	staleHost := request
	staleHost.OperationID += "-stale-host"
	staleHost.SourceHostAuthorityGeneration = 2
	if _, _, err := StartHostEvacuation(ctx, pool, staleHost); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("stale source Host authority error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_placement_pool_memberships_current SET membership_state='BLOCKED' WHERE host_id=$1`, hostID); err != nil {
		t.Fatal(err)
	}
	notDrainable := request
	notDrainable.OperationID += "-not-drainable"
	if _, _, err := StartHostEvacuation(ctx, pool, notDrainable); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("not-drainable Host error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_placement_pool_memberships_current SET membership_state='ACTIVE' WHERE host_id=$1`, hostID); err != nil {
		t.Fatal(err)
	}
	beforeFailureEpochs, beforeFencingProofs := 0, 0
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&beforeFailureEpochs, &beforeFencingProofs); err != nil {
		t.Fatal(err)
	}
	operation, workloads, err := StartHostEvacuation(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	if operation.LifecycleState != "RUNNING" || operation.MaximumConcurrentWorkloads != 2 || len(workloads) != 0 {
		t.Fatalf("operation/workloads = %#v/%#v", operation, workloads)
	}
	if _, _, err := StartHostEvacuation(ctx, pool, request); err != nil {
		t.Fatalf("same request replay: %v", err)
	}
	conflict := request
	conflict.EvacuationPolicyRevision = 2
	if _, _, err := StartHostEvacuation(ctx, pool, conflict); !errors.Is(err, ErrHostEvacuationConflict) {
		t.Fatalf("policy drift replay error = %v", err)
	}
	if _, err := ClaimHostEvacuationWorkload(ctx, pool, request.OperationID, "worker", time.Minute); !errors.Is(err, ErrHostEvacuationBudgetExhausted) {
		t.Fatalf("empty workload claim error = %v", err)
	}
	terminal, err := FinalizeHostEvacuation(ctx, pool, request.OperationID, request.OperationID+":terminal")
	if err != nil {
		t.Fatal(err)
	}
	if terminal.LifecycleState != "VERIFIED" || terminal.WorkloadCount != 0 {
		t.Fatalf("terminal = %#v", terminal)
	}
	var drainState string
	var afterFailureEpochs, afterFencingProofs int
	if err := pool.QueryRow(ctx, `SELECT drain_state,(SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence) FROM kim.host_placement_drains_current WHERE source_host_id=$1`, hostID).Scan(&drainState, &afterFailureEpochs, &afterFencingProofs); err != nil {
		t.Fatal(err)
	}
	if drainState != "DRAINED" || beforeFailureEpochs != afterFailureEpochs || beforeFencingProofs != afterFencingProofs {
		t.Fatalf("drain/failure authority = %s epochs %d->%d fencing %d->%d", drainState, beforeFailureEpochs, afterFailureEpochs, beforeFencingProofs, afterFencingProofs)
	}
	metrics, err := LoadHostEvacuationMetrics(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.CurrentConcurrency != 0 || metrics.UnknownCount != 0 {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestHostEvacuationBoundedConcurrencyAndFailureEscalationPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('evacuation-concurrency',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	hostID, poolID := "evacuation-concurrency-source-"+suffix, "evacuation-concurrency-pool-"+suffix
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO kim.host_identities(host_id,enrollment_state,host_authority_generation) VALUES($1,'APPROVED',1)`, []any{hostID}},
		{`INSERT INTO kim.placement_pools_current(pool_id,pool_generation,lifecycle_state,policy_id,policy_generation) VALUES($1,1,'ACTIVE','evacuation-placement',1)`, []any{poolID}},
		{`INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$2,1,'ACTIVE')`, []any{hostID, poolID}},
		{`INSERT INTO kim.host_operation_authorities_current(host_id,authority_generation,authority_state,session_generation,credential_binding_revision,enrollment_decision_revision,capability_generation,baseline_assignment_generation,preflight_generation,compliance_generation,policy_id,policy_generation,armed_by,reason_code) VALUES($1,1,'ARMED',1,1,1,1,1,1,1,'evacuation-host-operation',1,'integration','concurrency_fixture')`, []any{hostID}},
	} {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	request := HostEvacuationRequest{OperationID: "host-evacuation-concurrency-" + suffix, SourceHostID: hostID,
		EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1, DrainPolicyID: "planned-maintenance", DrainPolicyRevision: 1,
		EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 2, Reason: "concurrency qualification", RequestedBy: "integration"}
	if _, _, err := StartHostEvacuation(ctx, pool, request); err != nil {
		t.Fatal(err)
	}

	// This campaign injects only rebuildable current child projections in order
	// to isolate parent slot/restart/failure reconciliation. Snapshot derivation
	// and immutability are exercised separately by StartHostEvacuation.
	childIDs := []string{request.OperationID + ":synthetic-child-1", request.OperationID + ":synthetic-child-2", request.OperationID + ":synthetic-child-3", request.OperationID + ":synthetic-cancel-child"}
	vmIDs := []string{"10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002", "10000000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000004"}
	for index := range childIDs {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_evacuation_workloads_current(child_operation_id,child_generation,evacuation_operation_id,vm_id,vm_generation,phase,state_generation,result_state,reason_code) VALUES($1,1,$2,$3::uuid,1,'READY_TO_QUIESCE',1,'ELIGIBLE','synthetic_concurrency_projection')`, childIDs[index], request.OperationID, vmIDs[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := CancelHostEvacuationChild(ctx, pool, request.OperationID, childIDs[3], "cancelled_before_quiescence"); err != nil {
		t.Fatalf("pre-quiescence cancel: %v", err)
	}
	claim1, err := ClaimHostEvacuationWorkload(ctx, pool, request.OperationID, "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim2, err := ClaimHostEvacuationWorkload(ctx, pool, request.OperationID, "worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimHostEvacuationWorkload(ctx, pool, request.OperationID, "worker-c", time.Minute); !errors.Is(err, ErrHostEvacuationBudgetExhausted) {
		t.Fatalf("third claim while 2/2 active = %v", err)
	}
	if err := BlockHostEvacuationChild(ctx, pool, claim1, "qualified_partial_block"); err != nil {
		t.Fatal(err)
	}
	claim3, err := ClaimHostEvacuationWorkload(ctx, pool, request.OperationID, "worker-c", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim3.ChildOperationID == claim1.ChildOperationID || claim3.ChildOperationID == claim2.ChildOperationID {
		t.Fatalf("third child claim = %#v", claim3)
	}

	// A worker dies after source mutation began. Expiry becomes UNKNOWN and
	// consumes its slot until explicit reconciliation; it is never auto-reused.
	for _, statement := range []string{
		`UPDATE kim.host_evacuation_workloads_current SET phase='SOURCE_QUIESCED',result_state='RUNNING' WHERE child_operation_id=$1`,
		`UPDATE kim.host_evacuation_slot_claims_current SET lease_expires_at=statement_timestamp()-interval '1 second' WHERE child_operation_id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, claim2.ChildOperationID); err != nil {
			t.Fatal(err)
		}
	}
	if err := CancelHostEvacuationChild(ctx, pool, request.OperationID, claim2.ChildOperationID, "late_cancel_must_fail"); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("post-quiescence cancel error = %v", err)
	}
	if _, err := ClaimHostEvacuationWorkload(ctx, pool, request.OperationID, "worker-d", time.Minute); !errors.Is(err, ErrHostEvacuationBudgetExhausted) {
		t.Fatalf("UNKNOWN slot was reused: %v", err)
	}
	var unknownState string
	if err := pool.QueryRow(ctx, `SELECT claim_state FROM kim.host_evacuation_slot_claims_current WHERE child_operation_id=$1`, claim2.ChildOperationID).Scan(&unknownState); err != nil || unknownState != "UNKNOWN" {
		t.Fatalf("expired dangerous claim state/error = %s/%v", unknownState, err)
	}
	if err := BlockHostEvacuationChild(ctx, pool, claim3, "second_partial_block"); err != nil {
		t.Fatal(err)
	}

	var beforeEpochs, beforeFencing int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&beforeEpochs, &beforeFencing); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_operation_authorities_current SET authority_state='DISARMED' WHERE host_id=$1`, hostID); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileHostEvacuationSourceAuthority(ctx, pool, request.OperationID); err != nil {
		t.Fatal(err)
	}
	var parentState, remainingPhase string
	var slotCount, afterEpochs, afterFencing int
	if err := pool.QueryRow(ctx, `SELECT lifecycle_state,(SELECT phase FROM kim.host_evacuation_workloads_current WHERE child_operation_id=$2),(SELECT count(*) FROM kim.host_evacuation_slot_claims_current WHERE evacuation_operation_id=$1),(SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence) FROM kim.host_evacuation_operations_current WHERE evacuation_operation_id=$1`, request.OperationID, claim2.ChildOperationID).Scan(&parentState, &remainingPhase, &slotCount, &afterEpochs, &afterFencing); err != nil {
		t.Fatal(err)
	}
	if parentState != "SOURCE_UNREACHABLE" || remainingPhase != "RECOVERY_REQUIRED" || slotCount != 0 || beforeEpochs != afterEpochs || beforeFencing != afterFencing {
		t.Fatalf("failure escalation parent=%s child=%s slots=%d epochs=%d->%d fencing=%d->%d", parentState, remainingPhase, slotCount, beforeEpochs, afterEpochs, beforeFencing, afterFencing)
	}
}
