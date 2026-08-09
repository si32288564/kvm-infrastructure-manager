package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	qualificationID := hostID + "-vf-qualification"
	qualifyPlacementVF(t, ctx, pool, hostID, qualificationID)
	networkID, subnetID, segmentClaimID := "network-"+suffix, "subnet-"+suffix, "segment-"+suffix
	if err := UpsertNetworkFoundation(ctx, pool, NetworkFoundation{
		NetworkID: networkID, ProjectID: "project", NetworkGeneration: 1,
		NetworkState: "ACTIVE", MTU: 1500,
		SubnetID: subnetID, SubnetGeneration: 1, SubnetState: "ACTIVE",
		CIDR: "192.0.2.0/24", AllocationStart: "192.0.2.10", AllocationEnd: "192.0.2.200",
		ExcludedAddresses: []string{"192.0.2.1"},
		SegmentClaimID:    segmentClaimID, SegmentType: "VLAN", ScopeID: "physnet-a-" + suffix,
		SegmentID: 120, SegmentGeneration: 1, ProviderMappingRevision: 1, SegmentState: "ACTIVE",
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostNetworkMapping(ctx, pool, HostNetworkMapping{
		HostID: hostID, SegmentClaimID: segmentClaimID, Generation: 1,
		State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS", "SRIOV_DIRECT"},
	}); err != nil {
		t.Fatal(err)
	}
	storageBackendID, storageClassID, vgUUID := "storage-"+suffix, "local-lvm-"+suffix, "vg-uuid-"+suffix
	if err := RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{
		BackendID: storageBackendID, HostID: hostID, VGUUID: vgUUID,
		BackendState: "ACTIVE", BackendGeneration: 1,
		CapabilityState: "CURRENT", HostCapabilityGeneration: 1, SupportTier: "VALIDATED",
		StorageClassID: storageClassID, StorageClassRevision: 1, ClassState: "ACTIVE",
		FencingPolicyRevision: 1, ThinProvisioning: false, EncryptionRequired: false,
		CapacityObservationID: "storage-observation-" + suffix,
		CapacityGeneration:    1, CapacityState: "CURRENT", HealthState: "HEALTHY",
		TotalBytes: 40 << 30, ObservedFreeBytes: 36 << 30,
		ExternalOrUnknownBytes: 0, HardReserveBytes: 4 << 30, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
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

	numaNode := 1
	request := PlacementAdmissionRequest{
		RequestID: "request-" + suffix, ProjectID: "project", WorkloadID: "vm-" + suffix,
		ImageID: imageID, FlavorID: flavorID, PoolID: poolID,
		PCI: []placement.PCIRequirement{{
			DeviceAddress: "0000:03:00.1", PolicyID: "vf-policy", PolicyGeneration: 1,
			QualificationID: qualificationID, QualificationRevision: 1,
			RequiredNUMANodeID: &numaNode, RequiredIOMMUGroup: "13",
		}},
		Network: []placement.NetworkRequirement{{
			PortID: "port-" + suffix, NetworkID: networkID, NetworkGeneration: 1,
			SubnetID: subnetID, SubnetGeneration: 1,
			SegmentClaimID: segmentClaimID, SegmentGeneration: 1, HostMappingGeneration: 1,
			IPAddress: "192.0.2.10", MACAddress: "02:00:00:00:00:10",
			BindingType: "SRIOV_DIRECT", DeviceAddress: "0000:03:00.1", RequiredMTU: 1500,
		}},
		Storage: []placement.StorageRequirement{{
			VolumeID: "volume-" + suffix, AttachmentID: "attachment-" + suffix,
			BackendID: storageBackendID, BackendGeneration: 1, VGUUID: vgUUID,
			StorageClassID: storageClassID, StorageClassRevision: 1,
			CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1,
			SizeBytes: 8 << 30, AccessMode: "SINGLE_WRITER",
		}},
	}
	excludedRequest := request
	excludedRequest.RequestID, excludedRequest.WorkloadID = "request-excluded-"+suffix, "vm-excluded-"+suffix
	excludedRequest.Network = append([]placement.NetworkRequirement(nil), request.Network...)
	excludedRequest.Network[0].PortID = "port-excluded-" + suffix
	excludedRequest.Network[0].IPAddress = "192.0.2.1"
	excludedRequest.Network[0].MACAddress = "02:00:00:00:00:01"
	excluded, err := DryEvaluatePlacement(ctx, pool, excludedRequest, hostID)
	if err != nil || excluded.Eligible || !containsReason(excluded.ReasonCodes, "network:"+excludedRequest.Network[0].PortID+":ip_not_allowed") {
		t.Fatalf("excluded IP dry evaluation/error = %#v/%v", excluded, err)
	}
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
	competingRequest.Network = append([]placement.NetworkRequirement(nil), request.Network...)
	competingRequest.Network[0].PortID = "port-competing-" + suffix
	competingRequest.Storage = cloneStorageRequirements(request.Storage)
	competingRequest.Storage[0].VolumeID = "volume-competing-" + suffix
	competingRequest.Storage[0].AttachmentID = "attachment-competing-" + suffix
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
	changedPCI := winner.request
	changedPCI.PCI = append([]placement.PCIRequirement(nil), winner.request.PCI...)
	changedPCI.PCI[0].PolicyGeneration++
	if _, err := FinalAdmitPlacement(ctx, pool, changedPCI, winner.evaluation); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("request identity reused with different PCI requirements: %v", err)
	}
	changedNetwork := winner.request
	changedNetwork.Network = append([]placement.NetworkRequirement(nil), winner.request.Network...)
	changedNetwork.Network[0].HostMappingGeneration++
	if _, err := FinalAdmitPlacement(ctx, pool, changedNetwork, winner.evaluation); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("request identity reused with different Network requirements: %v", err)
	}
	changedStorage := winner.request
	changedStorage.Storage = cloneStorageRequirements(winner.request.Storage)
	changedStorage.Storage[0].CapacityGeneration++
	if _, err := FinalAdmitPlacement(ctx, pool, changedStorage, winner.evaluation); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("request identity reused with different Storage requirements: %v", err)
	}

	// A second concurrent pair has distinct Ports and no PCI requirement. Its
	// only shared scarce authority is the IP/MAC identity, proving Network
	// conflict serialization independently from VF claim serialization.
	networkOnlyA := request
	networkOnlyA.RequestID, networkOnlyA.WorkloadID = "request-network-a-"+suffix, "vm-network-a-"+suffix
	networkOnlyA.PCI = nil
	networkOnlyA.Network = []placement.NetworkRequirement{{
		PortID: "port-network-a-" + suffix, NetworkID: networkID, NetworkGeneration: 1,
		SubnetID: subnetID, SubnetGeneration: 1,
		SegmentClaimID: segmentClaimID, SegmentGeneration: 1, HostMappingGeneration: 1,
		IPAddress: "192.0.2.11", MACAddress: "02:00:00:00:00:11", BindingType: "OVS", RequiredMTU: 1500,
	}}
	networkOnlyA.Storage = cloneStorageRequirements(request.Storage)
	networkOnlyA.Storage[0].VolumeID = "volume-network-a-" + suffix
	networkOnlyA.Storage[0].AttachmentID = "attachment-network-a-" + suffix
	networkOnlyB := networkOnlyA
	networkOnlyB.RequestID, networkOnlyB.WorkloadID = "request-network-b-"+suffix, "vm-network-b-"+suffix
	networkOnlyB.Network = append([]placement.NetworkRequirement(nil), networkOnlyA.Network...)
	networkOnlyB.Network[0].PortID = "port-network-b-" + suffix
	networkOnlyB.Storage = cloneStorageRequirements(networkOnlyA.Storage)
	networkOnlyB.Storage[0].VolumeID = "volume-network-b-" + suffix
	networkOnlyB.Storage[0].AttachmentID = "attachment-network-b-" + suffix
	dryNetworkA, err := DryEvaluatePlacement(ctx, pool, networkOnlyA, hostID)
	if err != nil || !dryNetworkA.Eligible {
		t.Fatalf("network-only A dry evaluation/error = %#v/%v", dryNetworkA, err)
	}
	dryNetworkB, err := DryEvaluatePlacement(ctx, pool, networkOnlyB, hostID)
	if err != nil || !dryNetworkB.Eligible {
		t.Fatalf("network-only B dry evaluation/error = %#v/%v", dryNetworkB, err)
	}
	startNetwork := make(chan struct{})
	networkResults := make(chan error, 2)
	for _, candidate := range []struct {
		request    PlacementAdmissionRequest
		evaluation placement.Evaluation
	}{{networkOnlyA, dryNetworkA}, {networkOnlyB, dryNetworkB}} {
		candidate := candidate
		go func() {
			<-startNetwork
			_, err := FinalAdmitPlacement(ctx, pool, candidate.request, candidate.evaluation)
			networkResults <- err
		}()
	}
	close(startNetwork)
	networkSuccesses, networkRejected := 0, 0
	for range 2 {
		switch err := <-networkResults; {
		case err == nil:
			networkSuccesses++
		case errors.Is(err, ErrPlacementIneligible):
			networkRejected++
		default:
			t.Fatalf("concurrent Network Final Admission error = %v", err)
		}
	}
	if networkSuccesses != 1 || networkRejected != 1 {
		t.Fatalf("concurrent Network Final Admission success/rejected = %d/%d", networkSuccesses, networkRejected)
	}

	// With distinct Network and Volume identities, two 12 GiB requests are
	// independently eligible against 20 GiB remaining. Backend-scoped locking
	// and current capacity re-evaluation permit exactly one to commit.
	storageOnlyA := request
	storageOnlyA.RequestID, storageOnlyA.WorkloadID = "request-storage-a-"+suffix, "vm-storage-a-"+suffix
	storageOnlyA.PCI = nil
	storageOnlyA.Network = []placement.NetworkRequirement{{
		PortID: "port-storage-a-" + suffix, NetworkID: networkID, NetworkGeneration: 1,
		SubnetID: subnetID, SubnetGeneration: 1,
		SegmentClaimID: segmentClaimID, SegmentGeneration: 1, HostMappingGeneration: 1,
		IPAddress: "192.0.2.12", MACAddress: "02:00:00:00:00:12", BindingType: "OVS", RequiredMTU: 1500,
	}}
	storageOnlyA.Storage = []placement.StorageRequirement{{
		VolumeID: "volume-storage-a-" + suffix, AttachmentID: "attachment-storage-a-" + suffix,
		BackendID: storageBackendID, BackendGeneration: 1, VGUUID: vgUUID,
		StorageClassID: storageClassID, StorageClassRevision: 1,
		CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1,
		SizeBytes: 12 << 30, AccessMode: "SINGLE_WRITER",
	}}
	storageOnlyB := storageOnlyA
	storageOnlyB.RequestID, storageOnlyB.WorkloadID = "request-storage-b-"+suffix, "vm-storage-b-"+suffix
	storageOnlyB.Network = append([]placement.NetworkRequirement(nil), storageOnlyA.Network...)
	storageOnlyB.Network[0].PortID = "port-storage-b-" + suffix
	storageOnlyB.Network[0].IPAddress = "192.0.2.13"
	storageOnlyB.Network[0].MACAddress = "02:00:00:00:00:13"
	storageOnlyB.Storage = cloneStorageRequirements(storageOnlyA.Storage)
	storageOnlyB.Storage[0].VolumeID = "volume-storage-b-" + suffix
	storageOnlyB.Storage[0].AttachmentID = "attachment-storage-b-" + suffix
	dryStorageA, err := DryEvaluatePlacement(ctx, pool, storageOnlyA, hostID)
	if err != nil || !dryStorageA.Eligible {
		t.Fatalf("storage-only A dry evaluation/error = %#v/%v", dryStorageA, err)
	}
	dryStorageB, err := DryEvaluatePlacement(ctx, pool, storageOnlyB, hostID)
	if err != nil || !dryStorageB.Eligible {
		t.Fatalf("storage-only B dry evaluation/error = %#v/%v", dryStorageB, err)
	}
	startStorage := make(chan struct{})
	storageResults := make(chan error, 2)
	for _, candidate := range []struct {
		request    PlacementAdmissionRequest
		evaluation placement.Evaluation
	}{{storageOnlyA, dryStorageA}, {storageOnlyB, dryStorageB}} {
		candidate := candidate
		go func() {
			<-startStorage
			_, err := FinalAdmitPlacement(ctx, pool, candidate.request, candidate.evaluation)
			storageResults <- err
		}()
	}
	close(startStorage)
	storageSuccesses, storageRejected := 0, 0
	for range 2 {
		switch err := <-storageResults; {
		case err == nil:
			storageSuccesses++
		case errors.Is(err, ErrPlacementIneligible):
			storageRejected++
		default:
			t.Fatalf("concurrent Storage Final Admission error = %v", err)
		}
	}
	if storageSuccesses != 1 || storageRejected != 1 {
		t.Fatalf("concurrent Storage Final Admission success/rejected = %d/%d", storageSuccesses, storageRejected)
	}
	var decisions, claims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.placement_admission_decisions WHERE host_id=$1`, hostID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.compute_allocation_claims WHERE host_id=$1`, hostID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if decisions != 3 || claims != 3 {
		t.Fatalf("decision/claim count = %d/%d", decisions, claims)
	}
	var vfClaims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.pci_vf_allocation_claims WHERE placement_admission_id=$1 AND claim_state='ACTIVE'`, winner.admission.AdmissionID).Scan(&vfClaims); err != nil {
		t.Fatal(err)
	}
	if vfClaims != 1 {
		t.Fatalf("atomic Placement VF claims = %d", vfClaims)
	}
	var ports, identities, bindings int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM kim.network_ports_current WHERE network_id=$1),
		       (SELECT count(*) FROM kim.network_identity_claims WHERE network_id=$1),
		       (SELECT count(*) FROM kim.port_bindings_current binding JOIN kim.network_ports_current port USING (port_id) WHERE port.network_id=$1)
	`, networkID).Scan(&ports, &identities, &bindings); err != nil {
		t.Fatal(err)
	}
	if ports != 3 || identities != 6 || bindings != 3 {
		t.Fatalf("atomic Network Port/identity/binding claims = %d/%d/%d", ports, identities, bindings)
	}
	var volumes, capacityClaims, storageBindings, attachments, attachmentClaims, prematureLVUUIDs int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM kim.volumes_current),
		       (SELECT count(*) FROM kim.storage_capacity_claims),
		       (SELECT count(*) FROM kim.volume_backend_binding_intents),
		       (SELECT count(*) FROM kim.volume_attachments_current),
		       (SELECT count(*) FROM kim.volume_attachment_claims),
		       (SELECT count(*) FROM kim.volume_backend_binding_intents WHERE observed_lv_uuid IS NOT NULL)
	`).Scan(&volumes, &capacityClaims, &storageBindings, &attachments, &attachmentClaims, &prematureLVUUIDs); err != nil {
		t.Fatal(err)
	}
	if volumes != 3 || capacityClaims != 3 || storageBindings != 3 || attachments != 3 || attachmentClaims != 3 || prematureLVUUIDs != 0 {
		t.Fatalf("atomic Storage Volume/capacity/binding/attachment claims = %d/%d/%d/%d/%d premature-lv=%d", volumes, capacityClaims, storageBindings, attachments, attachmentClaims, prematureLVUUIDs)
	}
	winnerVolumeID := winner.request.Storage[0].VolumeID
	secondWriterErr := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		secondAttachmentID := "attachment-second-writer-" + suffix
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.volume_attachments_current (
				attachment_id, placement_admission_id, volume_id, workload_id,
				desired_host_id, attachment_generation, access_mode, desired_state
			) VALUES ($1,$2,$3,$4,$5,2,'SINGLE_WRITER','RESERVED')
		`, secondAttachmentID, winner.admission.AdmissionID, winnerVolumeID,
			"vm-second-writer-"+suffix, hostID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.volume_attachment_claims (
				attachment_claim_id, placement_admission_id, attachment_id, volume_id,
				workload_id, host_id, attachment_generation, access_mode,
				fencing_policy_revision, claim_state
			) VALUES ($1,$2,$3,$4,$5,$6,2,'SINGLE_WRITER',1,'RESERVED')
		`, "claim-second-writer-"+suffix, winner.admission.AdmissionID,
			secondAttachmentID, winnerVolumeID, "vm-second-writer-"+suffix, hostID)
		return err
	})
	if secondWriterErr == nil {
		t.Fatal("PostgreSQL accepted a second active SINGLE_WRITER Attachment Claim")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.volume_attachment_claims WHERE volume_id=$1`, winnerVolumeID).Scan(&attachmentClaims); err != nil {
		t.Fatal(err)
	}
	if attachmentClaims != 1 {
		t.Fatalf("single-writer Attachment Claim count = %d", attachmentClaims)
	}
}

func cloneStorageRequirements(requirements []placement.StorageRequirement) []placement.StorageRequirement {
	result := make([]placement.StorageRequirement, len(requirements))
	copy(result, requirements)
	return result
}

func containsReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}

func acceptPlacementInventory(t *testing.T, ctx context.Context, db TxBeginner, hostID string) {
	t.Helper()
	threads := make([]agentinventory.CPUThread, 24)
	for index := range threads {
		threads[index] = agentinventory.CPUThread{LinuxID: index, CoreID: index / 2, SocketID: 0, NUMANodeID: index % 2, Online: true, Isolated: true}
	}
	snapshot := agentinventory.Snapshot{SchemaVersion: agentinventory.SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: 1, CollectionStatus: "COMPLETE", Fragments: []agentinventory.Fragment{
		{Domain: agentinventory.DomainCompute, Source: agentinventory.Source{ModuleName: "linux-compute", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestBytes([]byte("compute-module"))}, Capabilities: []agentinventory.Capability{{Name: "kim.host.cpu-topology.v1", Version: "v1", State: agentinventory.AvailabilityAvailable}}, Compute: &agentinventory.Compute{Architecture: "x86_64", CPUModel: "fixture", Threads: threads}},
		{Domain: agentinventory.DomainMemory, Source: agentinventory.Source{ModuleName: "linux-memory", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestBytes([]byte("memory-module"))}, Capabilities: []agentinventory.Capability{{Name: "kim.host.memory.v1", Version: "v1", State: agentinventory.AvailabilityAvailable}}, Memory: &agentinventory.Memory{TotalBytes: 32 * 1024 * 1024 * 1024, AvailableBytes: 32 * 1024 * 1024 * 1024, NUMANodes: []agentinventory.NUMANode{{LinuxID: 0, CPUThreadIDs: []int{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22}, MemoryTotalBytes: 16 * 1024 * 1024 * 1024}, {LinuxID: 1, CPUThreadIDs: []int{1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23}, MemoryTotalBytes: 16 * 1024 * 1024 * 1024}}, HugePagePools: []agentinventory.HugePagePool{{PageSizeBytes: 1024 * 1024 * 1024, TotalPages: 24, FreePages: 24}}}},
		{Domain: agentinventory.DomainPCI, Source: agentinventory.Source{ModuleName: "linux-pci", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestBytes([]byte("pci-module"))}, Capabilities: []agentinventory.Capability{
			{Name: "kim.host.iommu-observation.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.pci-numa-locality.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.pci-observation.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.sriov-observation.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
		}, PCI: &agentinventory.PCI{IOMMUEnabled: true, Devices: []agentinventory.PCIDevice{
			{Address: "0000:03:00.0", VendorID: "8086", DeviceID: "10fb", Driver: "ixgbe", NUMANodeID: 1, IOMMUGroup: "12", SRIOVTotalVFs: 2, SRIOVEnabledVFs: 1, RelationshipState: agentinventory.AvailabilityAvailable},
			{Address: "0000:03:00.1", VendorID: "8086", DeviceID: "10ed", Driver: "ixgbevf", NUMANodeID: 1, IOMMUGroup: "13", PFAddress: "0000:03:00.0", VFIndex: func() *uint32 { value := uint32(0); return &value }(), RelationshipState: agentinventory.AvailabilityAvailable},
		}}},
	}}
	envelope, err := agentinventory.NewEnvelope(snapshot, 1, hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, db, envelope, 1<<20); err != nil {
		t.Fatal(err)
	}
}

func qualifyPlacementVF(t *testing.T, ctx context.Context, db *pgxpool.Pool, hostID, qualificationID string) {
	t.Helper()
	var observationDigest string
	if err := db.QueryRow(ctx, `SELECT observation_digest FROM kim.host_pci_device_projections WHERE host_id=$1 AND device_address='0000:03:00.1'`, hostID).Scan(&observationDigest); err != nil {
		t.Fatal(err)
	}
	fingerprint := map[string]string{"device": "8086:10ed", "driver": "ixgbevf/6", "firmware": "A", "kernel": "K1", "iommu": "strict", "libvirt_qemu": "L1/Q1"}
	if err := RecordPCIQualificationEvidence(ctx, db, PCIQualificationEvidence{
		QualificationID: qualificationID, Revision: 1, HostID: hostID, DeviceAddress: "0000:03:00.1",
		ProfileRevision: "sriov-profile/v1", TestArtifactDigest: digestBytes([]byte("test-artifact")),
		EvaluatorDigest: digestBytes([]byte("evaluator")), ObservedGeneration: 1,
		ObservationDigest: observationDigest, BindingFingerprint: fingerprint,
		ValidatedOperations: []string{"VF_DISCOVER", "VF_ASSIGN", "VF_READ_BACK"}, EvidenceState: "QUALIFIED",
	}); err != nil {
		t.Fatal(err)
	}
	state, err := RefreshPCIQualificationBinding(ctx, db, PCIQualificationBindingRequest{
		HostID: hostID, DeviceAddress: "0000:03:00.1", QualificationID: qualificationID,
		Revision: 1, CurrentGeneration: 1, CurrentObservationDigest: observationDigest,
		CurrentBindingFingerprint: fingerprint,
	})
	if err != nil || state != "CURRENT" {
		t.Fatalf("PCI qualification binding = %s/%v", state, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO kim.pci_allocation_policy_bindings (host_id, policy_id, policy_generation, policy_state, qualification_profile_revision) VALUES ($1,'vf-policy',1,'ALLOWED','sriov-profile/v1')`, hostID); err != nil {
		t.Fatal(err)
	}
}

func placementMutationCounts(t *testing.T, ctx context.Context, db QueryRower) string {
	t.Helper()
	var decisions, claims, vfClaims, ports, identities, bindings, volumes, capacityClaims, attachmentClaims int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.placement_admission_decisions), (SELECT count(*) FROM kim.compute_allocation_claims), (SELECT count(*) FROM kim.pci_vf_allocation_claims), (SELECT count(*) FROM kim.network_ports_current), (SELECT count(*) FROM kim.network_identity_claims), (SELECT count(*) FROM kim.port_bindings_current), (SELECT count(*) FROM kim.volumes_current), (SELECT count(*) FROM kim.storage_capacity_claims), (SELECT count(*) FROM kim.volume_attachment_claims)`).Scan(&decisions, &claims, &vfClaims, &ports, &identities, &bindings, &volumes, &capacityClaims, &attachmentClaims); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d/%d/%d/%d", decisions, claims, vfClaims, ports, identities, bindings, volumes, capacityClaims, attachmentClaims)
}
