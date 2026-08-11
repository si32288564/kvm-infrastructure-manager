package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMaintenanceHostGroupSnapshotConsumerPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES('maintenance-host-group-snapshot',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	hosts := []string{"maint-a-" + suffix, "maint-b-" + suffix, "maint-c-" + suffix, "maint-d-" + suffix}
	for _, hostID := range hosts {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID); err != nil {
			t.Fatal(err)
		}
	}
	groupID := "maintenance-cohort-" + suffix
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 1,
		GroupType: "OPERATIONAL_COHORT", Dimension: "maintenance-ring", Level: "ring", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	set1, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "maint-set-1-" + suffix, HostGroupID: groupID, SourceType: "EXPLICIT",
		SourceRevision: "operator-1", BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		Members: upgradeSnapshotMembers(groupID, hosts[:3], 1, "operator-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	maintenanceSnapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "maintenance-snapshot-1-" + suffix, HostGroupID: groupID, Purpose: "MAINTENANCE"})
	if err != nil || maintenanceSnapshot.MembershipSetGeneration != set1.MembershipSetGeneration || !validDigest(maintenanceSnapshot.SnapshotDigest) {
		t.Fatalf("maintenance snapshot=%+v err=%v", maintenanceSnapshot, err)
	}
	upgradeSnapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "upgrade-purpose-snapshot-" + suffix, HostGroupID: groupID, Purpose: "UPGRADE"})
	if err != nil {
		t.Fatal(err)
	}
	request := maintenancePlanFixture("maintenance-"+suffix, maintenanceSnapshot)
	policy := MaintenancePolicyRevision{PolicyID: "maintenance-policy-" + suffix, PolicyRevision: 1,
		OperationType: request.OperationType, OperationSchemaVersion: request.OperationSchemaVersion,
		ProfileID: request.ProfileID, ProfileRevision: request.ProfileRevision, ProfileDigest: request.ProfileDigest,
		MaximumConcurrent:               request.MaximumConcurrent,
		FailureDomainMaximumUnavailable: request.FailureDomainMaximumUnavailable, LifecycleState: "ACTIVE"}
	policyDigest, err := PublishMaintenancePolicy(ctx, pool, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{
		PublishRequestID: "maintenance-binding-request-" + suffix, BindingID: "maintenance-binding-" + suffix,
		HostGroupID: groupID, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN",
		PolicyID: policy.PolicyID, PolicyRevision: 1, PolicyDigest: policyDigest, Priority: 100,
		LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	plan, err := PublishMaintenancePlan(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishMaintenancePlan(ctx, pool, request); err != nil || replay.PlanDigest != plan.PlanDigest {
		t.Fatalf("maintenance replay=%+v err=%v", replay, err)
	}
	if _, err := PublishMaintenancePlan(ctx, pool, maintenancePlanFixture("wrong-maintenance-purpose-"+suffix, upgradeSnapshot)); !errors.Is(err, ErrMaintenanceEvidenceConflict) {
		t.Fatalf("UPGRADE snapshot accepted by maintenance: %v", err)
	}
	assertMaintenanceTargets(t, ctx, pool, request.MaintenanceID, maintenanceSnapshot, hosts[:3])

	set2, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "maint-set-2-" + suffix, HostGroupID: groupID, SourceType: "EXPLICIT",
		SourceRevision: "operator-2", BasedOnHostGroupGeneration: 1,
		ExpectedCurrentSetGeneration: set1.MembershipSetGeneration,
		Members:                      upgradeSnapshotMembers(groupID, []string{hosts[0], hosts[2], hosts[3]}, 2, "operator-2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMaintenanceTargets(t, ctx, pool, request.MaintenanceID, maintenanceSnapshot, hosts[:3])
	var persistedPlanDigest, persistedSnapshotDigest string
	if err := pool.QueryRow(ctx, `SELECT plan.plan_digest,snapshot.snapshot_digest
		FROM kim.maintenance_plan_evidence plan JOIN kim.host_group_membership_snapshot_evidence snapshot
		 ON snapshot.snapshot_id=plan.source_snapshot_id WHERE plan.maintenance_id=$1`, request.MaintenanceID).Scan(
		&persistedPlanDigest, &persistedSnapshotDigest); err != nil || persistedPlanDigest != plan.PlanDigest || persistedSnapshotDigest != maintenanceSnapshot.SnapshotDigest {
		t.Fatalf("drift changed digests plan=%s snapshot=%s err=%v", persistedPlanDigest, persistedSnapshotDigest, err)
	}

	maintenanceSnapshot2, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "maintenance-snapshot-2-" + suffix, HostGroupID: groupID, Purpose: "MAINTENANCE"})
	if err != nil || maintenanceSnapshot2.MembershipSetGeneration != set2.MembershipSetGeneration {
		t.Fatalf("snapshot2=%+v err=%v", maintenanceSnapshot2, err)
	}
	var racedSnapshot HostGroupSnapshot
	raceErrors := make(chan error, 2)
	go func() {
		var createErr error
		racedSnapshot, createErr = CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
			SnapshotID: "maintenance-snapshot-race-" + suffix, HostGroupID: groupID, Purpose: "MAINTENANCE"})
		raceErrors <- createErr
	}()
	go func() {
		_, publishErr := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
			PublishRequestID: "maint-set-race-" + suffix, HostGroupID: groupID, SourceType: "EXPLICIT",
			SourceRevision: "operator-race", BasedOnHostGroupGeneration: 1,
			ExpectedCurrentSetGeneration: set2.MembershipSetGeneration,
			Members:                      upgradeSnapshotMembers(groupID, []string{hosts[0], hosts[3]}, 3, "operator-race"),
		})
		raceErrors <- publishErr
	}()
	for range 2 {
		if err := <-raceErrors; err != nil {
			t.Fatal(err)
		}
	}
	var mixed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE member.membership_set_generation<>snapshot.membership_set_generation)
		FROM kim.host_group_membership_snapshot_evidence snapshot JOIN kim.host_group_membership_snapshot_members member
		USING(snapshot_id,host_group_id) WHERE snapshot.snapshot_id=$1`, racedSnapshot.SnapshotID).Scan(&mixed); err != nil || mixed != 0 {
		t.Fatalf("mixed generation snapshot=%+v mixed=%d err=%v", racedSnapshot, mixed, err)
	}

	claim1, err := ClaimMaintenanceCoordinator(ctx, pool, MaintenanceCoordinatorClaimRequest{MaintenanceID: request.MaintenanceID,
		Owner: "maintenance-coordinator-1", Lease: 60 * time.Millisecond, MaximumLifetime: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	claim2, err := ClaimMaintenanceCoordinator(ctx, pool, MaintenanceCoordinatorClaimRequest{MaintenanceID: request.MaintenanceID,
		Owner: "maintenance-coordinator-2", Lease: 60 * time.Millisecond, MaximumLifetime: time.Second})
	if err != nil || claim2.ClaimMode != "RECOVER_FROM_DB" || claim2.PlanRevision != claim1.PlanRevision {
		t.Fatalf("maintenance recovery=%+v err=%v", claim2, err)
	}
	assertMaintenanceTargets(t, ctx, pool, request.MaintenanceID, maintenanceSnapshot, hosts[:3])
	if err := PauseMaintenancePlan(ctx, pool, request.MaintenanceID, claim2.Owner, claim2.ClaimGeneration); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := ResumeMaintenancePlan(ctx, pool, MaintenanceResumeRequest{MaintenanceID: request.MaintenanceID,
		ResumeID: "maintenance-resume-" + suffix, Actor: "operator"}); err != nil {
		t.Fatal(err)
	}
	claim3, err := ClaimMaintenanceCoordinator(ctx, pool, MaintenanceCoordinatorClaimRequest{MaintenanceID: request.MaintenanceID,
		Owner: "maintenance-coordinator-3", Lease: time.Second, MaximumLifetime: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	assertMaintenanceTargets(t, ctx, pool, request.MaintenanceID, maintenanceSnapshot, hosts[:3])

	for _, hostID := range hosts[:3] {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_operation_authorities_current(
			host_id,authority_generation,authority_state,session_generation,credential_binding_revision,
			enrollment_decision_revision,capability_generation,baseline_assignment_generation,preflight_generation,
			compliance_generation,policy_id,policy_generation,armed_by,reason_code
		) VALUES($1,1,'ARMED',1,1,1,1,1,1,1,'maintenance-test',1,'fixture','qualified')`, hostID); err != nil {
			t.Fatal(err)
		}
	}
	var targetA, targetB, targetC string
	if err := pool.QueryRow(ctx, `SELECT target_id FROM kim.maintenance_target_evidence WHERE maintenance_id=$1 AND host_id=$2`,
		request.MaintenanceID, hosts[0]).Scan(&targetA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT target_id FROM kim.maintenance_target_evidence WHERE maintenance_id=$1 AND host_id=$2`,
		request.MaintenanceID, hosts[1]).Scan(&targetB); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT target_id FROM kim.maintenance_target_evidence WHERE maintenance_id=$1 AND host_id=$2`,
		request.MaintenanceID, hosts[2]).Scan(&targetC); err != nil {
		t.Fatal(err)
	}
	maintenanceTargetClaim, err := ClaimMaintenanceTarget(ctx, pool, MaintenanceTargetClaimRequest{MaintenanceID: request.MaintenanceID,
		TargetID: targetA, Owner: "maintenance-executor", Lease: time.Second, MaximumLifetime: 2 * time.Second})
	if err != nil || maintenanceTargetClaim.OperationType != "HOST_DRAIN" || maintenanceTargetClaim.OperationSchemaVersion != "v1" {
		t.Fatalf("typed maintenance claim=%+v err=%v", maintenanceTargetClaim, err)
	}
	if _, err := ClaimMaintenanceTarget(ctx, pool, MaintenanceTargetClaimRequest{MaintenanceID: request.MaintenanceID,
		TargetID: targetC, Owner: "maintenance-executor-2", Lease: time.Second, MaximumLifetime: 2 * time.Second}); !errors.Is(err, ErrMaintenanceTargetClaimUnavailable) {
		t.Fatalf("maintenance concurrency budget=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_operation_authorities_current SET authority_state='FENCED' WHERE host_id=$1`, hosts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimMaintenanceTarget(ctx, pool, MaintenanceTargetClaimRequest{MaintenanceID: request.MaintenanceID,
		TargetID: targetB, Owner: "maintenance-executor", Lease: time.Second, MaximumLifetime: 2 * time.Second}); !errors.Is(err, ErrMaintenanceTargetClaimUnavailable) {
		t.Fatalf("fenced Host maintenance claim=%v", err)
	}
	var fencedState string
	if err := pool.QueryRow(ctx, `SELECT target_state FROM kim.maintenance_targets_current WHERE target_id=$1`, targetB).Scan(&fencedState); err != nil || fencedState != "FENCED" {
		t.Fatalf("fenced target state=%s err=%v", fencedState, err)
	}
	var immutableTargets, claimedTargets, pendingTargets int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.maintenance_target_evidence WHERE maintenance_id=$1),
		count(*) FILTER (WHERE current.target_state='CLAIMED'),
		count(*) FILTER (WHERE current.target_state='PENDING')
		FROM kim.maintenance_targets_current current JOIN kim.maintenance_target_evidence target USING(target_id)
		WHERE target.maintenance_id=$1`, request.MaintenanceID).Scan(&immutableTargets, &claimedTargets, &pendingTargets); err != nil {
		t.Fatal(err)
	}
	if immutableTargets != 3 || claimedTargets != 1 || pendingTargets != 1 {
		t.Fatalf("Host fence changed unrelated Targets immutable=%d claimed=%d pending=%d", immutableTargets, claimedTargets, pendingTargets)

	}
	// UPGRADE and MAINTENANCE are separate Snapshot consumers, but disruptive
	// Host mutation authority is shared and exclusive.
	sourceRelease, targetRelease := "maint-source-"+suffix, "maint-target-"+suffix
	artifact := digestReleaseBytes([]byte("maint-upgrade-artifact-" + suffix))
	for _, manifest := range []ReleaseManifest{
		{ReleaseID: sourceRelease, ProductVersion: "1.0.0", Channel: "DEVELOPER_PREVIEW", ManifestRevision: 1,
			ManifestDigest: digestReleaseBytes([]byte(sourceRelease)), CertificationState: "VALIDATED",
			OVNWorkerArtifactDigest:          digestReleaseBytes([]byte(sourceRelease + "-worker")),
			OVNWorkerComponentContractDigest: digestReleaseBytes([]byte(sourceRelease + "-contract")),
			OVNWorkerWorkSchemas:             []string{OVNRuntimeWorkSchemaV1}},
		{ReleaseID: targetRelease, ProductVersion: "1.1.0", Channel: "DEVELOPER_PREVIEW", ManifestRevision: 1,
			ManifestDigest: digestReleaseBytes([]byte(targetRelease)), CertificationState: "VALIDATED",
			OVNWorkerArtifactDigest: artifact, OVNWorkerComponentContractDigest: digestReleaseBytes([]byte(targetRelease + "-contract")),
			OVNWorkerWorkSchemas: []string{OVNRuntimeWorkSchemaV1}},
	} {
		if err := PublishReleaseManifest(ctx, pool, manifest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := PublishUpgradeCampaignPlan(ctx, pool,
		upgradeSnapshotPlanRequest("wrong-upgrade-purpose-"+suffix, sourceRelease, targetRelease, artifact, maintenanceSnapshot)); !errors.Is(err, ErrUpgradeCampaignEvidenceConflict) {
		t.Fatalf("MAINTENANCE snapshot accepted by upgrade: %v", err)
	}
	upgradePlanRequest := upgradeSnapshotPlanRequest("maintenance-conflict-upgrade-"+suffix, sourceRelease, targetRelease, artifact, upgradeSnapshot)
	if _, err := PublishUpgradeCampaignPlan(ctx, pool, upgradePlanRequest); err != nil {
		t.Fatal(err)
	}
	upgradeCoordinator, err := ClaimUpgradeCampaign(ctx, pool, UpgradeCampaignClaimRequest{CampaignID: upgradePlanRequest.CampaignID,
		Owner: "upgrade-coordinator", Lease: time.Second, MaximumLifetime: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var upgradeTargetA string
	if err := pool.QueryRow(ctx, `SELECT target_id FROM kim.upgrade_target_evidence WHERE campaign_id=$1 AND component_id=$2`,
		upgradePlanRequest.CampaignID, hosts[0]).Scan(&upgradeTargetA); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimUpgradeTarget(ctx, pool, UpgradeTargetClaimRequest{CampaignID: upgradePlanRequest.CampaignID,
		TargetID: upgradeTargetA, Owner: "upgrade-executor", Lease: time.Second, MaximumLifetime: 2 * time.Second}); !errors.Is(err, ErrHostDisruptiveOperationConflict) {
		t.Fatalf("same Host upgrade/maintenance conflict=%v", err)
	}
	_ = upgradeCoordinator
	_ = claim3

	// Different snapshots racing for one Maintenance revision must leave one
	// complete Plan/Wave/Target authority and no partial loser rows.
	raceMaintenanceID := "maintenance-plan-race-" + suffix
	requests := []MaintenancePlanRequest{
		maintenancePlanFixture(raceMaintenanceID, maintenanceSnapshot),
		maintenancePlanFixture(raceMaintenanceID, maintenanceSnapshot2),
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range requests {
		wg.Add(1)
		go func(planRequest MaintenancePlanRequest) {
			defer wg.Done()
			_, err := PublishMaintenancePlan(ctx, pool, planRequest)
			results <- err
		}(requests[i])
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrMaintenanceEvidenceConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("maintenance plan race successes=%d conflicts=%d", successes, conflicts)
	}
	var plans, waves, targets int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.maintenance_plan_evidence WHERE maintenance_id=$1),
		(SELECT count(*) FROM kim.maintenance_wave_evidence WHERE maintenance_id=$1),
		(SELECT count(*) FROM kim.maintenance_target_evidence WHERE maintenance_id=$1)`, raceMaintenanceID).Scan(
		&plans, &waves, &targets); err != nil {
		t.Fatal(err)
	}
	if plans != 1 || waves != 1 || targets != 3 {
		t.Fatalf("partial maintenance publication plans=%d waves=%d targets=%d", plans, waves, targets)
	}
}

func maintenancePlanFixture(maintenanceID string, snapshot HostGroupSnapshot) MaintenancePlanRequest {
	return MaintenancePlanRequest{MaintenanceID: maintenanceID, PlanRevision: 1,
		SnapshotID: snapshot.SnapshotID, SnapshotDigest: snapshot.SnapshotDigest, WaveID: "wave-1",
		OperationType: "HOST_DRAIN", OperationSchemaVersion: "v1", ProfileID: "safe-drain",
		ProfileRevision: 1, ProfileDigest: digestReleaseBytes([]byte("safe-drain/v1")),
		MaximumConcurrent: 1, FailureDomainMaximumUnavailable: 1}
}

func assertMaintenanceTargets(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	maintenanceID string, snapshot HostGroupSnapshot, expected []string,
) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT target.host_id FROM kim.maintenance_target_evidence target
		JOIN kim.maintenance_target_host_group_member_evidence provenance USING(target_id)
		JOIN kim.maintenance_plan_evidence plan
		 ON plan.maintenance_id=target.maintenance_id AND plan.plan_revision=target.plan_revision
		WHERE target.maintenance_id=$1 AND provenance.source_snapshot_id=$2
		AND provenance.source_snapshot_digest=$3 ORDER BY target.host_id`, maintenanceID,
		snapshot.SnapshotID, snapshot.SnapshotDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, host)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if fmt.Sprint(actual) != fmt.Sprint(want) {
		t.Fatalf("maintenance targets=%v want=%v", actual, want)
	}
}
