package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

func TestDryAndFinalPlacementAdmissionPostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('placement-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	hostID, imageID, flavorID, poolID := "host-"+suffix, "image-"+suffix, "flavor-"+suffix, "pool-"+suffix
	certificateFingerprint := digestBytes([]byte("placement-certificate"))
	prepareSessionIdentityFixture(t, ctx, pool, hostID, 1, certificateFingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: hostID + "-attempt", HostID: hostID, ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: certificateFingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, hostID)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "default", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: hostID, PoolID: poolID, Generation: 1, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	checksum := digestBytes([]byte("placement-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "QCOW2", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "https://images.invalid/image.qcow2", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	numa, huge := uint32(2), uint64(1048576)
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "nfv.small", VCPUs: 4, MemoryMiB: 4096, RootDiskGiB: 20, NUMAPolicy: "REQUIRED", NUMANodes: &numa, HugePageSizeKiB: &huge, CPUAllocation: "DEDICATED", CPUPinning: true}); err != nil {
		t.Fatal(err)
	}

	request := PlacementAdmissionRequest{RequestID: "request-" + suffix, ProjectID: "project", WorkloadID: "vm-" + suffix, ImageID: imageID, FlavorID: flavorID, PoolID: poolID}
	before := placementMutationCounts(t, ctx, pool)
	dry, err := DryEvaluatePlacement(ctx, pool, request, hostID)
	if err != nil || !dry.Eligible {
		t.Fatalf("dry evaluation/error = %#v/%v", dry, err)
	}
	after := placementMutationCounts(t, ctx, pool)
	if before != after {
		t.Fatalf("dry evaluation mutated authority: before=%v after=%v", before, after)
	}

	// A generation change after dry evaluation must be detected by the same
	// rules inside Final Admission, without leaving a partial claim.
	if err := AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: hostID, PoolID: poolID, Generation: 2, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacement(ctx, pool, request, dry); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("stale dry evaluation admission error = %v", err)
	}
	if counts := placementMutationCounts(t, ctx, pool); counts != before {
		t.Fatalf("stale Final Admission left partial authority: %v", counts)
	}

	current, err := DryEvaluatePlacement(ctx, pool, request, hostID)
	if err != nil || !current.Eligible {
		t.Fatalf("current dry evaluation/error = %#v/%v", current, err)
	}
	competingRequest := request
	competingRequest.RequestID = "request-competing-" + suffix
	competingRequest.WorkloadID = "vm-competing-" + suffix
	competing, err := DryEvaluatePlacement(ctx, pool, competingRequest, hostID)
	if err != nil || !competing.Eligible {
		t.Fatalf("competing dry evaluation/error = %#v/%v", competing, err)
	}
	type admissionResult struct {
		request    PlacementAdmissionRequest
		evaluation placement.Evaluation
		admission  PlacementAdmission
		err        error
	}
	start := make(chan struct{})
	results := make(chan admissionResult, 2)
	for _, candidate := range []struct {
		request    PlacementAdmissionRequest
		evaluation placement.Evaluation
	}{{request, current}, {competingRequest, competing}} {
		candidate := candidate
		go func() {
			<-start
			admission, err := FinalAdmitPlacement(ctx, pool, candidate.request, candidate.evaluation)
			results <- admissionResult{request: candidate.request, evaluation: candidate.evaluation, admission: admission, err: err}
		}()
	}
	close(start)
	var winner admissionResult
	successes, rejected := 0, 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, ErrPlacementIneligible):
			rejected++
		default:
			t.Fatalf("concurrent Final Admission error = %v", result.err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("concurrent Final Admission success/rejected = %d/%d", successes, rejected)
	}
	if winner.admission.HostID != hostID || winner.admission.RequestDigest != winner.evaluation.RequestDigest {
		t.Fatalf("winning admission = %#v", winner.admission)
	}
	// Stable request identity returns the original decision even though the
	// reservation has consumed the candidate capacity.
	replayed, err := FinalAdmitPlacement(ctx, pool, winner.request, winner.evaluation)
	if err != nil || replayed.AdmissionID != winner.admission.AdmissionID || replayed.AllocationID != winner.admission.AllocationID {
		t.Fatalf("idempotent Final Admission replay = %#v/%v", replayed, err)
	}
	var decisions, claims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.placement_admission_decisions WHERE host_id=$1`, hostID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.compute_allocation_claims WHERE host_id=$1`, hostID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 || claims != 1 {
		t.Fatalf("decision/claim count = %d/%d", decisions, claims)
	}
}

func acceptPlacementInventory(t *testing.T, ctx context.Context, db TxBeginner, hostID string) {
	t.Helper()
	threads := make([]agentinventory.CPUThread, 4)
	for index := range threads {
		threads[index] = agentinventory.CPUThread{LinuxID: index, CoreID: index / 2, SocketID: 0, NUMANodeID: index % 2, Online: true, Isolated: true}
	}
	snapshot := agentinventory.Snapshot{SchemaVersion: agentinventory.SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: 1, CollectionStatus: "COMPLETE", Fragments: []agentinventory.Fragment{
		{Domain: agentinventory.DomainCompute, Source: agentinventory.Source{ModuleName: "linux-compute", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestBytes([]byte("compute-module"))}, Capabilities: []agentinventory.Capability{{Name: "kim.host.cpu-topology.v1", Version: "v1", State: agentinventory.AvailabilityAvailable}}, Compute: &agentinventory.Compute{Architecture: "x86_64", CPUModel: "fixture", Threads: threads}},
		{Domain: agentinventory.DomainMemory, Source: agentinventory.Source{ModuleName: "linux-memory", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestBytes([]byte("memory-module"))}, Capabilities: []agentinventory.Capability{{Name: "kim.host.memory.v1", Version: "v1", State: agentinventory.AvailabilityAvailable}}, Memory: &agentinventory.Memory{TotalBytes: 8 * 1024 * 1024 * 1024, AvailableBytes: 8 * 1024 * 1024 * 1024, NUMANodes: []agentinventory.NUMANode{{LinuxID: 0, CPUThreadIDs: []int{0, 2}, MemoryTotalBytes: 4 * 1024 * 1024 * 1024}, {LinuxID: 1, CPUThreadIDs: []int{1, 3}, MemoryTotalBytes: 4 * 1024 * 1024 * 1024}}, HugePagePools: []agentinventory.HugePagePool{{PageSizeBytes: 1024 * 1024 * 1024, TotalPages: 4, FreePages: 4}}}},
	}}
	envelope, err := agentinventory.NewEnvelope(snapshot, 1, hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, db, envelope, 1<<20); err != nil {
		t.Fatal(err)
	}
}

func placementMutationCounts(t *testing.T, ctx context.Context, db QueryRower) string {
	t.Helper()
	var decisions, claims int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.placement_admission_decisions), (SELECT count(*) FROM kim.compute_allocation_claims)`).Scan(&decisions, &claims); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d/%d", decisions, claims)
}
