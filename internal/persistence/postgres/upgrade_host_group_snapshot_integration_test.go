package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUpgradeHostGroupSnapshotConsumerPostgreSQLIntegration(t *testing.T) {
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
		VALUES('upgrade-host-group-snapshot',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	hosts := []string{"host-a-" + suffix, "host-b-" + suffix, "host-c-" + suffix, "host-d-" + suffix}
	for _, hostID := range hosts {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID); err != nil {
			t.Fatal(err)
		}
	}
	groupID := "upgrade-cohort-" + suffix
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 1,
		GroupType: "OPERATIONAL_COHORT", Dimension: "upgrade-ring", Level: "ring", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	set1, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "upgrade-set-1-" + suffix, HostGroupID: groupID, SourceType: "EXPLICIT",
		SourceRevision: "operator-1", BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		Members: upgradeSnapshotMembers(groupID, hosts[:3], 1, "operator-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "upgrade-snapshot-1-" + suffix, HostGroupID: groupID, Purpose: "UPGRADE",
	})
	if err != nil || !validDigest(snapshot1.SnapshotDigest) || snapshot1.MembershipSetGeneration != set1.MembershipSetGeneration {
		t.Fatalf("snapshot1=%+v err=%v", snapshot1, err)
	}

	sourceRelease, targetRelease := "snapshot-source-"+suffix, "snapshot-target-"+suffix
	artifact := digestReleaseBytes([]byte("snapshot-target-artifact-" + suffix))
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
	plan := upgradeSnapshotPlanRequest("snapshot-campaign-"+suffix, sourceRelease, targetRelease, artifact, snapshot1)
	published, err := PublishUpgradeCampaignPlan(ctx, pool, plan)
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := PublishUpgradeCampaignPlan(ctx, pool, plan); err != nil || replayed.PlanDigest != published.PlanDigest {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}

	set2, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "upgrade-set-2-" + suffix, HostGroupID: groupID, SourceType: "EXPLICIT",
		SourceRevision: "operator-2", BasedOnHostGroupGeneration: 1,
		ExpectedCurrentSetGeneration: set1.MembershipSetGeneration,
		Members:                      upgradeSnapshotMembers(groupID, []string{hosts[0], hosts[2], hosts[3]}, 2, "operator-2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "upgrade-snapshot-2-" + suffix, HostGroupID: groupID, Purpose: "UPGRADE",
	})
	if err != nil || set2.MembershipSetGeneration == set1.MembershipSetGeneration {
		t.Fatalf("snapshot2=%+v set2=%+v err=%v", snapshot2, set2, err)
	}
	var racedSnapshot HostGroupSnapshot
	raceErrors := make(chan error, 2)
	go func() {
		var createErr error
		racedSnapshot, createErr = CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
			SnapshotID: "upgrade-snapshot-race-" + suffix, HostGroupID: groupID, Purpose: "UPGRADE"})
		raceErrors <- createErr
	}()
	go func() {
		_, publishErr := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
			PublishRequestID: "upgrade-set-race-" + suffix, HostGroupID: groupID, SourceType: "EXPLICIT",
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
	var racedMembers, mismatchedMembers int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE member.membership_set_generation<>snapshot.membership_set_generation)
		FROM kim.host_group_membership_snapshot_evidence snapshot
		JOIN kim.host_group_membership_snapshot_members member USING(snapshot_id,host_group_id)
		WHERE snapshot.snapshot_id=$1`, racedSnapshot.SnapshotID).Scan(&racedMembers, &mismatchedMembers); err != nil {
		t.Fatal(err)
	}
	if mismatchedMembers != 0 || racedMembers != racedSnapshot.MemberCount ||
		(racedSnapshot.MembershipSetGeneration != set2.MembershipSetGeneration && racedSnapshot.MembershipSetGeneration != set2.MembershipSetGeneration+1) {
		t.Fatalf("mixed snapshot=%+v members=%d mismatched=%d", racedSnapshot, racedMembers, mismatchedMembers)
	}
	assertSnapshotUpgradeTargets(t, ctx, pool, plan.CampaignID, snapshot1, hosts[:3])

	claim1, err := ClaimUpgradeCampaign(ctx, pool, UpgradeCampaignClaimRequest{CampaignID: plan.CampaignID,
		Owner: "coordinator-1", Lease: 80 * time.Millisecond, MaximumLifetime: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	claim2, err := ClaimUpgradeCampaign(ctx, pool, UpgradeCampaignClaimRequest{CampaignID: plan.CampaignID,
		Owner: "coordinator-2", Lease: 80 * time.Millisecond, MaximumLifetime: time.Second})
	if err != nil || claim2.ClaimMode != "RECOVER_FROM_DB" || claim2.PlanRevision != claim1.PlanRevision {
		t.Fatalf("recovery claim=%+v err=%v", claim2, err)
	}
	assertSnapshotUpgradeTargets(t, ctx, pool, plan.CampaignID, snapshot1, hosts[:3])

	var failedTarget string
	if err := pool.QueryRow(ctx, `SELECT target_id FROM kim.upgrade_target_evidence WHERE campaign_id=$1 AND component_id=$2`,
		plan.CampaignID, hosts[0]).Scan(&failedTarget); err != nil {
		t.Fatal(err)
	}
	if err := RecordUpgradeTargetOutcome(ctx, pool, UpgradeTargetOutcomeRequest{CampaignID: plan.CampaignID,
		TargetID: failedTarget, Owner: claim2.Owner, ClaimGeneration: claim2.ClaimGeneration,
		Outcome: "FAILED", ResultDigest: digestReleaseBytes([]byte("canary-failed"))}); err != nil {
		t.Fatal(err)
	}
	decision, err := EvaluateUpgradeCanary(ctx, pool, UpgradeCanaryDecisionRequest{CampaignID: plan.CampaignID,
		Owner: claim2.Owner, ClaimGeneration: claim2.ClaimGeneration, EvaluatorArtifactDigest: digestReleaseBytes([]byte("snapshot-evaluator"))})
	if err != nil || decision.Decision != "PAUSE" {
		t.Fatalf("pause=%+v err=%v", decision, err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := ResumeUpgradeCampaign(ctx, pool, UpgradeCampaignResumeRequest{CampaignID: plan.CampaignID,
		ResumeID: "resume-" + suffix, Actor: "operator"}); err != nil {
		t.Fatal(err)
	}
	assertSnapshotUpgradeTargets(t, ctx, pool, plan.CampaignID, snapshot1, hosts[:3])

	claim3, err := ClaimUpgradeCampaign(ctx, pool, UpgradeCampaignClaimRequest{CampaignID: plan.CampaignID,
		Owner: "coordinator-3", Lease: time.Second, MaximumLifetime: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, hostID := range hosts[1:3] {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_operation_authorities_current(
			host_id,authority_generation,authority_state,session_generation,credential_binding_revision,
			enrollment_decision_revision,capability_generation,baseline_assignment_generation,preflight_generation,
			compliance_generation,policy_id,policy_generation,armed_by,reason_code
		) VALUES($1,1,'ARMED',1,1,1,1,1,1,1,'upgrade-test',1,'fixture','qualified')`, hostID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_operation_authorities_current SET authority_state='FENCED' WHERE host_id=$1`, hosts[1]); err != nil {
		t.Fatal(err)
	}
	var fencedTarget string
	if err := pool.QueryRow(ctx, `SELECT target_id FROM kim.upgrade_target_evidence WHERE campaign_id=$1 AND component_id=$2`,
		plan.CampaignID, hosts[1]).Scan(&fencedTarget); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimUpgradeTarget(ctx, pool, UpgradeTargetClaimRequest{CampaignID: plan.CampaignID,
		TargetID: fencedTarget, Owner: "executor", Lease: time.Second, MaximumLifetime: 2 * time.Second}); !errors.Is(err, ErrUpgradeTargetClaimUnavailable) {
		t.Fatalf("fenced Host target claim=%v", err)
	}
	var targetState string
	if err := pool.QueryRow(ctx, `SELECT target_state FROM kim.upgrade_targets_current WHERE target_id=$1`, fencedTarget).Scan(&targetState); err != nil || targetState != "FENCED" {
		t.Fatalf("fenced target state=%s err=%v", targetState, err)
	}
	_ = claim3

	// Two different immutable snapshots racing for the same Campaign/revision
	// must yield one complete current Plan and no partial second publication.
	raceCampaign := "snapshot-race-" + suffix
	requests := []UpgradeCampaignPlanRequest{
		upgradeSnapshotPlanRequest(raceCampaign, sourceRelease, targetRelease, artifact, snapshot1),
		upgradeSnapshotPlanRequest(raceCampaign, sourceRelease, targetRelease, artifact, snapshot2),
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range requests {
		wg.Add(1)
		go func(request UpgradeCampaignPlanRequest) {
			defer wg.Done()
			_, err := PublishUpgradeCampaignPlan(ctx, pool, request)
			results <- err
		}(requests[i])
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrUpgradeCampaignEvidenceConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("plan race successes=%d conflicts=%d", successes, conflicts)
	}
	var plans, bindings, targets int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.upgrade_plan_evidence WHERE campaign_id=$1),
		(SELECT count(*) FROM kim.upgrade_plan_host_group_snapshot_evidence WHERE campaign_id=$1),
		(SELECT count(*) FROM kim.upgrade_target_evidence WHERE campaign_id=$1)`, raceCampaign).Scan(&plans, &bindings, &targets); err != nil {
		t.Fatal(err)
	}
	if plans != 1 || bindings != 1 || targets != 3 {
		t.Fatalf("partial plan race plans=%d bindings=%d targets=%d", plans, bindings, targets)
	}
	_ = claim3
}

func upgradeSnapshotMembers(groupID string, hosts []string, generation uint64, revision string) []HostGroupMembership {
	members := make([]HostGroupMembership, 0, len(hosts))
	for _, hostID := range hosts {
		members = append(members, HostGroupMembership{HostGroupID: groupID, HostID: hostID, Generation: generation,
			State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: revision})
	}
	return members
}

func upgradeSnapshotPlanRequest(campaignID, sourceRelease, targetRelease, artifact string, snapshot HostGroupSnapshot) UpgradeCampaignPlanRequest {
	graph, _ := json.Marshal(map[string]any{"nodes": []string{"HOST_AGENT"}, "edges": []any{}})
	provenance, _ := json.Marshal(map[string]any{"builder": "kim-qualified-builder",
		"bundle_digest": digestReleaseBytes([]byte(campaignID + "-bundle")), "signature_state": "VERIFIED",
		"artifacts": map[string]string{"HOST_AGENT": artifact}})
	return UpgradeCampaignPlanRequest{CampaignID: campaignID, PlanRevision: 1, SourceReleaseID: sourceRelease,
		SourceManifestRevision: 1, TargetReleaseID: targetRelease, TargetManifestRevision: 1,
		Strategy: "CANARY_ROLLING", ComponentGraph: graph, ProvenanceSnapshot: provenance,
		SBOMSnapshotDigest: digestReleaseBytes([]byte(campaignID + "-sbom")),
		Waves:              []UpgradeWavePlan{{WaveID: "canary", WaveOrdinal: 1, WaveType: "CANARY", MaximumUnavailable: 1}},
		HostGroupSnapshots: []UpgradeHostGroupSnapshotPlan{{SnapshotID: snapshot.SnapshotID,
			SnapshotDigest: snapshot.SnapshotDigest, WaveID: "canary", ComponentType: "HOST_AGENT",
			TargetReleaseID: targetRelease, TargetManifestRevision: 1, TargetArtifactDigest: artifact}}}
}

func assertSnapshotUpgradeTargets(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID string, snapshot HostGroupSnapshot, expected []string) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT target.component_id FROM kim.upgrade_target_evidence target
		JOIN kim.upgrade_target_host_group_member_evidence provenance USING(target_id)
		JOIN kim.upgrade_plan_host_group_snapshot_evidence binding
		 ON binding.campaign_id=provenance.campaign_id AND binding.plan_revision=provenance.plan_revision
		AND binding.wave_id=provenance.wave_id AND binding.source_snapshot_id=provenance.source_snapshot_id
		WHERE target.campaign_id=$1 AND provenance.source_snapshot_id=$2 AND provenance.source_snapshot_digest=$3
		ORDER BY target.component_id`, campaignID, snapshot.SnapshotID, snapshot.SnapshotDigest)
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
	sort.Strings(expected)
	if fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("snapshot targets=%v want=%v", actual, expected)
	}
}
