package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPlacementScopePublicationRacesPostgreSQLIntegration(t *testing.T) {
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
		VALUES('placement-scope-race',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	hostA, hostB := "scope-race-a-"+suffix, "scope-race-b-"+suffix
	for _, hostID := range []string{hostA, hostB} {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID); err != nil {
			t.Fatal(err)
		}
	}
	groupA, groupB := "scope-race-pool-a-"+suffix, "scope-race-pool-b-"+suffix
	for _, groupID := range []string{groupA, groupB} {
		if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: groupID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "default", PolicyGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	setA, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "scope-race-set-a-1-" + suffix, HostGroupID: groupA, SourceType: "EXPLICIT", SourceRevision: "race-1", BasedOnHostGroupGeneration: 1, Members: upgradeSnapshotMembers(groupA, []string{hostA}, 1, "race-1")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "scope-race-set-b-1-" + suffix, HostGroupID: groupB, SourceType: "EXPLICIT", SourceRevision: "race-1", BasedOnHostGroupGeneration: 1, Members: upgradeSnapshotMembers(groupB, []string{hostB}, 1, "race-1")}); err != nil {
		t.Fatal(err)
	}
	imageID, flavorID := "scope-race-image-"+suffix, "scope-race-flavor-"+suffix
	checksum := digestBytes([]byte("scope-race-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "https://images.invalid/scope-race.raw", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "scope.race", VCPUs: 1, MemoryMiB: 512, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	request := func(id, scopeID string) PlacementAdmissionRequest {
		return PlacementAdmissionRequest{RequestID: id, ProjectID: "project", WorkloadID: "vm-" + id, ImageID: imageID, FlavorID: flavorID, PlacementScopeID: scopeID}
	}
	if noScope, err := DryEvaluatePlacementScope(ctx, pool, request("no-scope-"+suffix, "")); err != nil || noScope.Status != "NO_SCOPE" {
		t.Fatalf("no Scope=%+v err=%v", noScope, err)
	}
	emptyScopeID := "scope-empty-" + suffix
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "scope-empty-publish-" + suffix, PlacementScopeID: emptyScopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if empty, err := DryEvaluatePlacementScope(ctx, pool, request("empty-"+suffix, emptyScopeID)); err != nil || empty.Status != "NO_VISIBLE_HOST" || len(empty.Candidates) != 0 {
		t.Fatalf("empty Scope=%+v err=%v", empty, err)
	}

	scopeID := "scope-publish-race-" + suffix
	currentGroup := groupA
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "scope-race-publish-1-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: groupA, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}
	if visibleOnly, err := DryEvaluatePlacementScope(ctx, pool, request("visible-ineligible-"+suffix, scopeID)); err != nil || visibleOnly.Status != "VISIBLE_BUT_NO_ELIGIBLE_HOST" || len(visibleOnly.Candidates) != 1 || !visibleOnly.Candidates[0].Visible || visibleOnly.Candidates[0].Eligible {
		t.Fatalf("visible but ineligible=%+v err=%v", visibleOnly, err)
	}
	for generation := uint64(2); generation <= 21; generation++ {
		nextGroup := groupA
		if currentGroup == groupA {
			nextGroup = groupB
		}
		dryCh := make(chan PlacementScopeDryResult, 1)
		errCh := make(chan error, 2)
		go func(g uint64) {
			dry, err := DryEvaluatePlacementScope(ctx, pool, request(fmt.Sprintf("scope-race-dry-%d-%s", g, suffix), scopeID))
			dryCh <- dry
			errCh <- err
		}(generation)
		go func(g uint64, groupID string) {
			_, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: fmt.Sprintf("scope-race-publish-%d-%s", g, suffix), PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", ExpectedCurrentGeneration: g - 1, Exposures: []PlacementScopeExposure{{HostGroupID: groupID, HostGroupGeneration: 1}}})
			errCh <- err
		}(generation, nextGroup)
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		dry := <-dryCh
		if dry.ScopeGeneration != generation-1 && dry.ScopeGeneration != generation {
			t.Fatalf("mixed Scope generation %d during %d", dry.ScopeGeneration, generation)
		}
		expectedHost := hostA
		if (dry.ScopeGeneration == generation-1 && currentGroup == groupB) || (dry.ScopeGeneration == generation && nextGroup == groupB) {
			expectedHost = hostB
		}
		if len(dry.Candidates) != 1 || dry.Candidates[0].HostID != expectedHost || len(dry.Candidates[0].Provenance) != 1 {
			t.Fatalf("mixed Scope exposure generation=%d candidates=%+v", dry.ScopeGeneration, dry.Candidates)
		}
		currentGroup = nextGroup
	}

	// Scope stays fixed while complete Membership Sets switch. Repeatable-read
	// dry evaluation must expose either the old complete Set or the new one.
	setScopeID := "scope-set-race-" + suffix
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "scope-set-race-publish-1-" + suffix, PlacementScopeID: setScopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: groupA, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}
	currentSetGeneration := setA.MembershipSetGeneration
	currentHost := hostA
	for nextSetGeneration := currentSetGeneration + 1; nextSetGeneration <= currentSetGeneration+20; nextSetGeneration++ {
		nextHost := hostA
		if currentHost == hostA {
			nextHost = hostB
		}
		dryCh := make(chan PlacementScopeDryResult, 1)
		errCh := make(chan error, 2)
		go func(g uint64) {
			dry, err := DryEvaluatePlacementScope(ctx, pool, request(fmt.Sprintf("set-race-dry-%d-%s", g, suffix), setScopeID))
			dryCh <- dry
			errCh <- err
		}(nextSetGeneration)
		go func(g uint64, hostID string) {
			_, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: fmt.Sprintf("scope-set-race-%d-%s", g, suffix), HostGroupID: groupA, SourceType: "EXPLICIT", SourceRevision: fmt.Sprintf("race-%d", g), BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: g - 1, Members: upgradeSnapshotMembers(groupA, []string{hostID}, g, fmt.Sprintf("race-%d", g))})
			errCh <- err
		}(nextSetGeneration, nextHost)
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		dry := <-dryCh
		if len(dry.Candidates) != 1 || len(dry.Candidates[0].Provenance) != 1 {
			t.Fatalf("mixed Membership Set candidates=%+v", dry.Candidates)
		}
		provenance := dry.Candidates[0].Provenance[0]
		if provenance.MembershipSetGeneration != nextSetGeneration-1 && provenance.MembershipSetGeneration != nextSetGeneration {
			t.Fatalf("mixed Membership Set generation=%d next=%d", provenance.MembershipSetGeneration, nextSetGeneration)
		}
		expectedHost := nextHost
		if provenance.MembershipSetGeneration == nextSetGeneration-1 {
			expectedHost = currentHost
		}
		if dry.Candidates[0].HostID != expectedHost {
			t.Fatalf("Set generation/member mismatch generation=%d host=%s expected=%s", provenance.MembershipSetGeneration, dry.Candidates[0].HostID, expectedHost)
		}
		currentHost = nextHost
	}
}
