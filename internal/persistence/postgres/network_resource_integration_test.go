package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

func TestNetworkResourceAuthorityPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('network-resource-integration',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%012x", uint64(time.Now().UnixNano())&0xffffffffffff)
	projectID := "00000000-0000-4000-8000-" + suffix
	projectDigest := digestBytes([]byte("network-project-" + suffix))
	if _, err := pool.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'integration','network','network-project')`, projectID, "network-project-"+suffix, projectDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "network-project-"+suffix, projectDigest); err != nil {
		t.Fatal(err)
	}

	networkID := "network-authority-" + suffix
	desired := NetworkResourceRequest{NetworkID: networkID, ProjectID: projectID, Name: "tenant-overlay", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"}
	created, err := CreateNetworkResource(ctx, pool, desired)
	if err != nil || created.Revision != 1 || created.SegmentID < 10000 || created.RealizationState != "PENDING" {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	replayed, err := CreateNetworkResource(ctx, pool, desired)
	if err != nil || replayed.NetworkID != created.NetworkID || replayed.SegmentID != created.SegmentID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	collision := desired
	collision.NetworkID, collision.Name, collision.SegmentPolicy, collision.RequestedSegmentID = "network-collision-"+suffix, "collision", "EXPLICIT", created.SegmentID
	if _, err := CreateNetworkResource(ctx, pool, collision); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("segment collision err=%v", err)
	}

	var ready bool
	if err := pool.QueryRow(ctx, networkPlacementReadySQL, networkID).Scan(&ready); err != nil || ready {
		t.Fatalf("pending Network placement readiness=%v err=%v", ready, err)
	}
	claim1, err := ClaimNetworkRealization(ctx, pool, created.OperationID, "network-worker", time.Minute)
	if err != nil || claim1.ClaimMode != "APPLY_ALLOWED" || claim1.ClaimGeneration != 1 {
		t.Fatalf("claim1=%+v err=%v", claim1, err)
	}
	_, plan1, err := ovnadapter.RestoreStoredNetworkPlan(claim1.CanonicalPlan, claim1.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeNetworkRealizationApply(ctx, pool, claim1); err != nil {
		t.Fatal(err)
	}
	terminal, err := AcceptNetworkRealizationObservation(ctx, pool, claim1, matchedNetworkObservation(claim1, plan1, "backend-a", "LOST"))
	if err != nil || terminal != "VERIFIED" {
		t.Fatalf("realize terminal=%q err=%v", terminal, err)
	}
	if err := pool.QueryRow(ctx, networkPlacementReadySQL, networkID).Scan(&ready); err != nil || !ready {
		t.Fatalf("verified Network placement readiness=%v err=%v", ready, err)
	}

	updated, err := UpdateNetworkResource(ctx, pool, networkID, NetworkResourcePatch{ExpectedRevision: 1, Name: "tenant-overlay-renamed", MTU: 1500})
	if err != nil || updated.Revision != 2 || updated.RealizationGeneration != 2 || updated.RealizationState != "PENDING" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	claim2, err := ClaimNetworkRealization(ctx, pool, updated.OperationID, "network-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, plan2, _ := ovnadapter.RestoreStoredNetworkPlan(claim2.CanonicalPlan, claim2.PlanDigest)
	if err := AuthorizeNetworkRealizationApply(ctx, pool, claim2); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptNetworkRealizationObservation(ctx, pool, claim2, matchedNetworkObservation(claim2, plan2, "backend-b", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	var terminalCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_realization_terminal_evidence WHERE network_id=$1`, networkID).Scan(&terminalCount); err != nil || terminalCount != 2 {
		t.Fatalf("terminal history count=%d err=%v", terminalCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.network_resource_revision_evidence SET network_name='forged' WHERE network_id=$1 AND network_revision=1`, networkID); err == nil {
		t.Fatal("immutable Network revision accepted UPDATE")
	}

	// The legacy foundation is a compatibility adapter: it may add the existing
	// Subnet projection but cannot replace the new Network or segment authority.
	foundation := NetworkFoundation{NetworkID: networkID, ProjectID: projectID, NetworkState: "ACTIVE", NetworkGeneration: 2, MTU: 1500,
		SubnetID: "subnet-" + suffix, SubnetState: "ACTIVE", SubnetGeneration: 1, CIDR: "10.77.0.0/24", AllocationStart: "10.77.0.10", AllocationEnd: "10.77.0.200",
		SegmentClaimID: "network-segment:" + networkID, SegmentType: "VNI", ScopeID: "standard-overlay", SegmentState: "ACTIVE", SegmentID: uint64(created.SegmentID), SegmentGeneration: 1, ProviderMappingRevision: 1}
	if err := UpsertNetworkFoundation(ctx, pool, foundation); err != nil {
		t.Fatalf("legacy compatibility adapter: %v", err)
	}
	var authoritySource string
	if err := pool.QueryRow(ctx, `SELECT authority_source FROM kim.networks_current WHERE network_id=$1`, networkID).Scan(&authoritySource); err != nil || authoritySource != "NETWORK_RESOURCE" {
		t.Fatalf("authority source=%q err=%v", authoritySource, err)
	}

	if _, err := RequestNetworkRetirement(ctx, pool, networkID, 2); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("dependent subnet retirement err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.network_subnets_current SET lifecycle_state='DISABLED' WHERE subnet_id=$1`, foundation.SubnetID); err != nil {
		t.Fatal(err)
	}
	retiring, err := RequestNetworkRetirement(ctx, pool, networkID, 2)
	if err != nil || retiring.Lifecycle != "RETIRE_PENDING" || retiring.Revision != 3 {
		t.Fatalf("retiring=%+v err=%v", retiring, err)
	}
	if _, err := CreateNetworkResource(ctx, pool, collision); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("segment reused before absence terminal err=%v", err)
	}
	retireClaim1, err := ClaimNetworkRealization(ctx, pool, retiring.OperationID, "network-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, retirePlan, _ := ovnadapter.RestoreStoredNetworkPlan(retireClaim1.CanonicalPlan, retireClaim1.PlanDigest)
	if err := AuthorizeNetworkRealizationApply(ctx, pool, retireClaim1); err != nil {
		t.Fatal(err)
	}
	present := matchedNetworkObservation(retireClaim1, retirePlan, "backend-b", "LOST")
	present.Observation.PlanDigestMatches = true
	if state, err := AcceptNetworkRealizationObservation(ctx, pool, retireClaim1, present); err != nil || state != "" {
		t.Fatalf("non-absent retirement state=%q err=%v", state, err)
	}
	retireClaim2, err := ClaimNetworkRealization(ctx, pool, retiring.OperationID, "network-worker-successor", time.Minute)
	if err != nil || retireClaim2.ClaimMode != "READ_BACK_FIRST" || retireClaim2.ClaimGeneration != 2 {
		t.Fatalf("read-back-first claim=%+v err=%v", retireClaim2, err)
	}
	if err := AuthorizeNetworkRealizationApply(ctx, pool, retireClaim2); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("successor apply authorized before read-back evidence: %v", err)
	}
	if err := RecordNetworkRealizationReadBackStarted(ctx, pool, retireClaim2); err != nil {
		t.Fatal(err)
	}
	absent := matchedNetworkObservation(retireClaim2, retirePlan, "", "UNKNOWN")
	absent.Observation.LogicalSwitchPresent = false
	absent.Observation.OwnershipMarkerMatches = false
	absent.Observation.PlanDigestMatches = false
	if state, err := AcceptNetworkRealizationObservation(ctx, pool, retireClaim2, absent); err != nil || state != "ABSENT" {
		t.Fatalf("absence terminal state=%q err=%v", state, err)
	}
	deleted, err := GetNetworkResource(ctx, pool, networkID)
	if err != nil || deleted.Lifecycle != "DELETED" || deleted.AllocationID != "" {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	reused, err := CreateNetworkResource(ctx, pool, collision)
	if err != nil || reused.SegmentID != created.SegmentID {
		t.Fatalf("post-terminal segment reuse=%+v err=%v", reused, err)
	}
	if _, err := AcceptNetworkRealizationObservation(ctx, pool, retireClaim2, absent); err == nil {
		t.Fatal("stale retirement evidence replay mutated a released allocation")
	}
	var reusedState string
	if err := pool.QueryRow(ctx, `SELECT allocation_state FROM kim.network_segment_allocations_current WHERE network_id=$1`, reused.NetworkID).Scan(&reusedState); err != nil || reusedState != "ALLOCATED" {
		t.Fatalf("reused allocation state=%q err=%v", reusedState, err)
	}

	protected := desired
	protected.NetworkID, protected.Name, protected.DeleteProtection = "network-protected-"+suffix, "protected", true
	protectedResource, err := CreateNetworkResource(ctx, pool, protected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequestNetworkRetirement(ctx, pool, protectedResource.NetworkID, 1); err == nil {
		t.Fatal("delete-protected Network accepted retirement")
	}

	concurrent := make(chan NetworkResource, 2)
	failures := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			request := desired
			request.NetworkID = fmt.Sprintf("network-concurrent-%d-%s", index, suffix)
			request.Name = fmt.Sprintf("concurrent-%d", index)
			resource, err := CreateNetworkResource(ctx, pool, request)
			concurrent <- resource
			failures <- err
		}()
	}
	first, second := <-concurrent, <-concurrent
	if err1, err2 := <-failures, <-failures; err1 != nil || err2 != nil || first.SegmentID == second.SegmentID {
		t.Fatalf("concurrent allocations=%+v/%+v errors=%v/%v", first, second, err1, err2)
	}
}

const networkPlacementReadySQL = `SELECT EXISTS(SELECT 1 FROM kim.networks_current n JOIN kim.network_realizations_current r ON r.network_id=n.network_id AND r.network_revision=n.network_revision WHERE n.network_id=$1 AND n.lifecycle_state='ACTIVE' AND n.authority_source='NETWORK_RESOURCE' AND r.realization_state='VERIFIED' AND r.terminal_evidence_id IS NOT NULL)`

func matchedNetworkObservation(claim NetworkRealizationClaim, plan ovnadapter.NetworkPlan, backendUUID, response string) NetworkRealizationObservation {
	return NetworkRealizationObservation{
		ObservationID:         fmt.Sprintf("network-observation:%s:%d", claim.OperationID, claim.ClaimGeneration),
		OperationID:           claim.OperationID,
		OperationGeneration:   claim.OperationGeneration,
		ObservationGeneration: claim.ClaimGeneration,
		ApplyResponseState:    response,
		LogicalSwitchName:     plan.LogicalSwitchName,
		BackendUUID:           backendUUID,
		ObservationDigest:     digestBytes([]byte(fmt.Sprintf("observation:%s:%d", claim.OperationID, claim.ClaimGeneration))),
		AdapterArtifactDigest: digestBytes([]byte("network-adapter-v1")),
		Observation: ovnadapter.NetworkObservation{LogicalSwitchPresent: true, OwnershipMarkerMatches: true, PlanDigestMatches: true,
			LogicalSwitchName: plan.LogicalSwitchName, BackendUUID: backendUUID},
	}
}
