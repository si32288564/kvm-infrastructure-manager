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
	authority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "placement-integration", PolicyGeneration: 1, ActorID: "placement-integration", ReasonCode: "placement_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if authority.AuthorityGeneration != 1 {
		t.Fatalf("expected Host authority generation 1, got %d", authority.AuthorityGeneration)
	}
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "default", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: hostID, PoolID: poolID, Generation: 1, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	checksum := digestBytes([]byte("placement-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "https://images.invalid/image.raw", Visibility: "PRIVATE"}); err != nil {
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
			BindingType: "OVS", RequiredMTU: 1500,
		}},
		Storage: []placement.StorageRequirement{{
			VolumeID: "volume-" + suffix, AttachmentID: "attachment-" + suffix,
			BackendID: storageBackendID, BackendGeneration: 1, VGUUID: vgUUID,
			StorageClassID: storageClassID, StorageClassRevision: 1,
			CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1,
			SizeBytes: 8 << 30, AccessMode: "SINGLE_WRITER", Bootable: true,
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
	winnerBindingID := "storage-binding:" + winner.request.RequestID + ":" + winnerVolumeID
	var winnerResourceKey string
	if err := pool.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, winnerBindingID).Scan(&winnerResourceKey); err != nil {
		t.Fatal(err)
	}
	localLVMCommandID := "lvm-command-" + suffix
	localLVMVerificationID := "lvm-verification-" + suffix
	localLVMObservationDigest := digestBytes([]byte("lvm-observation-" + suffix))
	localLVMVerifierDigest := digestBytes([]byte("lvm-verifier"))
	localLVUUID := "lv-uuid-" + suffix
	if err := seedLocalLVMVerification(ctx, pool, localLVMVerificationFixture{
		JobID: "lvm-job-" + suffix, VolumeID: winnerVolumeID, HostID: hostID,
		CommandID: localLVMCommandID, VerificationID: localLVMVerificationID,
		ObservationDigest: localLVMObservationDigest, VerifierDigest: localLVMVerifierDigest,
		VGUUID: vgUUID, LVUUID: localLVUUID, ResourceKey: winnerResourceKey,
		SizeBytes: winner.request.Storage[0].SizeBytes,
	}); err != nil {
		t.Fatal(err)
	}
	acceptedBindingObservation := LocalLVMBindingObservation{
		EvidenceID: "binding-evidence-" + suffix, BindingID: winnerBindingID,
		VolumeID: winnerVolumeID, BackendID: storageBackendID, HostID: hostID,
		VGUUID: vgUUID, LVUUID: localLVUUID,
		BackendResourceKey: winnerResourceKey, BindingGeneration: 1,
		CommandID: localLVMCommandID, VerificationID: localLVMVerificationID, AttemptIndex: 1,
		ObservationGeneration: 1, ObservationDigest: localLVMObservationDigest,
		VerifierDigest: localLVMVerifierDigest, EvidenceState: "MATCHED",
		ObservedSizeBytes: winner.request.Storage[0].SizeBytes,
	}
	if err := AcceptLocalLVMBindingObservation(ctx, pool, acceptedBindingObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptLocalLVMBindingObservation(ctx, pool, acceptedBindingObservation); err != nil {
		t.Fatalf("idempotent Local LVM binding evidence replay: %v", err)
	}
	var bindingState, currentLVUUID string
	if err := pool.QueryRow(ctx, `
		SELECT current.binding_state, current.lv_uuid
		FROM kim.volume_backend_bindings_current current
		WHERE current.binding_id=$1
	`, winnerBindingID).Scan(&bindingState, &currentLVUUID); err != nil {
		t.Fatal(err)
	}
	if bindingState != "BOUND" || currentLVUUID != localLVUUID {
		t.Fatalf("current Local LVM binding = %s/%s", bindingState, currentLVUUID)
	}
	vmMaterialization := VMMaterializationRequest{
		VMID: "55555555-5555-4555-8555-555555555555", AdmissionID: winner.admission.AdmissionID,
		PlanID: "vm-plan-" + suffix, JobID: "vm-define-job-" + suffix,
		CommandID: "vm-define-command-" + suffix,
	}
	vmDecision, err := PrepareVMMaterialization(ctx, pool, vmMaterialization)
	if err != nil || vmDecision.HostID != hostID || len(vmDecision.PlanDigest) != 64 {
		t.Fatalf("VM materialization decision/error = %#v/%v", vmDecision, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='ALLOCATED' WHERE admission_id=$1`, winner.admission.AdmissionID); err != nil {
		t.Fatal(err)
	}
	replayedVMDecision, err := PrepareVMMaterialization(ctx, pool, vmMaterialization)
	if err != nil || replayedVMDecision != vmDecision {
		t.Fatalf("idempotent VM materialization = %#v/%v, want %#v", replayedVMDecision, err, vmDecision)
	}
	var vmLifecycle, commandType, commandState, imageMaterialization, networkRealization string
	if err := pool.QueryRow(ctx, `
		SELECT vm.lifecycle_state, command.command_type, current.command_state,
		       plan.plan_payload->>'image_materialization_state',
		       plan.plan_payload->>'network_realization_state'
		FROM kim.virtual_machines_current vm
		JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id
		JOIN kim.execution_jobs job ON job.resource_id=vm.vm_id::text
		JOIN kim.execution_commands command ON command.job_id=job.job_id
		JOIN kim.execution_commands_current current USING (command_id)
		WHERE vm.vm_id=$1
	`, vmMaterialization.VMID).Scan(&vmLifecycle, &commandType, &commandState,
		&imageMaterialization, &networkRealization); err != nil {
		t.Fatal(err)
	}
	if vmLifecycle != "MATERIALIZATION_PENDING" || commandType != "VIRTUAL_MACHINE_DEFINE" || commandState != "PENDING" || imageMaterialization != "PENDING" || networkRealization != "PENDING" {
		t.Fatalf("VM materialization authority = %s/%s/%s/%s/%s", vmLifecycle, commandType, commandState, imageMaterialization, networkRealization)
	}
	vmDefinitionDigest := digestBytes([]byte("vm-definition-observation-" + suffix))
	vmVerifierDigest := digestBytes([]byte("vm-definition-verifier"))
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.command_lease_grants (
			command_id,lease_generation,attempt_index,host_id,
			host_authority_generation,session_generation,token_digest,not_before,expires_at
		) VALUES ($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')
	`, vmMaterialization.CommandID, hostID, digestBytes([]byte("vm-definition-lease"))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.command_attempts (
			command_id,attempt_index,lease_generation,host_authority_generation,session_generation
		) VALUES ($1,1,1,1,1)
	`, vmMaterialization.CommandID); err != nil {
		t.Fatal(err)
	}
	vmVerificationID := "vm-definition-verification-" + suffix
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.command_verification_evidence (
			verification_id,command_id,attempt_index,observation_generation,
			observation_digest,verification_state,verifier_artifact_digest,evidence_payload
		) VALUES ($1,$2,1,1,$3,'MATCHED',$4,jsonb_build_object(
			'domain_uuid',$5::text,'materialization_generation',1,
			'plan_digest',$6::text,'domain_present',true,
			'domain_identity_matches',true,'plan_identity_matches',true,
			'compute_shape_matches',true,'root_volume_identity_matches',true,
			'image_materialization_state','PENDING','network_realization_state','PENDING'
		))
	`, vmVerificationID, vmMaterialization.CommandID, vmDefinitionDigest,
		vmVerifierDigest, vmMaterialization.VMID, vmDecision.PlanDigest); err != nil {
		t.Fatal(err)
	}
	vmDefinition := VMDefinitionObservation{
		EvidenceID: "vm-definition-evidence-" + suffix, VMID: vmMaterialization.VMID,
		VMGeneration: 1, PlanID: vmMaterialization.PlanID, PlanDigest: vmDecision.PlanDigest,
		HostID: hostID, CommandID: vmMaterialization.CommandID, AttemptIndex: 1,
		VerificationID: vmVerificationID, ObservationGeneration: 1,
		ObservationDigest: vmDefinitionDigest, VerifierDigest: vmVerifierDigest,
		EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true,
		PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true,
	}
	if err := AcceptVMDefinitionObservation(ctx, pool, vmDefinition); err != nil {
		t.Fatal(err)
	}
	if err := AcceptVMDefinitionObservation(ctx, pool, vmDefinition); err != nil {
		t.Fatalf("idempotent VM definition observation: %v", err)
	}
	var domainState, imageState, networkState, storageState, bootReadiness string
	var blockingReasons []string
	if err := pool.QueryRow(ctx, `
		SELECT domain_state,image_state,network_state,storage_state,boot_readiness,blocking_reasons
		FROM kim.vm_materialization_readiness_current WHERE vm_id=$1
	`, vmMaterialization.VMID).Scan(&domainState, &imageState, &networkState,
		&storageState, &bootReadiness, &blockingReasons); err != nil {
		t.Fatal(err)
	}
	if domainState != "DEFINED" || imageState != "PENDING" || networkState != "PENDING" || storageState != "BOUND" || bootReadiness != "BLOCKED" || len(blockingReasons) != 2 {
		t.Fatalf("VM readiness = %s/%s/%s/%s/%s/%v", domainState, imageState, networkState, storageState, bootReadiness, blockingReasons)
	}
	imageRequest := VMImageMaterializationRequest{
		VMID: vmMaterialization.VMID, PlanID: vmMaterialization.PlanID,
		JobID: "vm-image-job-" + suffix, CommandID: "vm-image-command-" + suffix,
	}
	imageDecision, err := PrepareVMImageMaterialization(ctx, pool, imageRequest)
	if err != nil || imageDecision.HostID != hostID || len(imageDecision.PayloadDigest) != 64 {
		t.Fatalf("VM Image materialization decision/error = %#v/%v", imageDecision, err)
	}
	var commandPayload map[string]any
	if err := pool.QueryRow(ctx, `SELECT payload FROM kim.execution_commands WHERE command_id=$1`, imageRequest.CommandID).Scan(&commandPayload); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"source_uri", "source_path", "target_path", "argv", "flags"} {
		if _, exists := commandPayload[forbidden]; exists {
			t.Fatalf("Image Command exposed forbidden %s", forbidden)
		}
	}
	replayedImageDecision, err := PrepareVMImageMaterialization(ctx, pool, imageRequest)
	if err != nil || replayedImageDecision != imageDecision {
		t.Fatalf("idempotent VM Image preparation = %#v/%v, want %#v", replayedImageDecision, err, imageDecision)
	}
	imageObservationDigest := digestBytes([]byte("vm-image-observation-" + suffix))
	imageVerifierDigest := digestBytes([]byte("vm-image-verifier"))
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.command_lease_grants (command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at)
		VALUES ($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')
	`, imageRequest.CommandID, hostID, digestBytes([]byte("vm-image-lease"))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_attempts (command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES ($1,1,1,1,1)`, imageRequest.CommandID); err != nil {
		t.Fatal(err)
	}
	imageVerificationID := "vm-image-verification-" + suffix
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.command_verification_evidence (verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload)
		VALUES ($1,$2,1,1,$3,'MATCHED',$4,jsonb_build_object(
			'domain_uuid',$5::text,'materialization_generation',1,
			'image_id',$6::text,'image_revision',1,
			'expected_content_digest',$7::text,'observed_content_digest',$7::text,
			'image_size_bytes',4096,'volume_id',$8::text,
			'observed_vg_uuid',$9::text,'observed_lv_uuid',$10::text,
			'backend_resource_key',$11::text,'holder_open',false,
			'content_identity_matches',true
		))
	`, imageVerificationID, imageRequest.CommandID, imageObservationDigest,
		imageVerifierDigest, vmMaterialization.VMID, imageID, checksum,
		winnerVolumeID, vgUUID, localLVUUID, winnerResourceKey); err != nil {
		t.Fatal(err)
	}
	imageObservation := VMImageRealizationObservation{
		EvidenceID: "vm-image-evidence-" + suffix, VMID: vmMaterialization.VMID,
		VMGeneration: 1, PlanID: vmMaterialization.PlanID, PlanDigest: vmDecision.PlanDigest,
		HostID: hostID, ImageID: imageID, ImageRevision: 1,
		ExpectedDigest: checksum, ObservedDigest: checksum, ImageSizeBytes: 4096,
		VolumeID: winnerVolumeID, BindingID: winnerBindingID, BindingGeneration: 1,
		VGUUID: vgUUID, LVUUID: localLVUUID, BackendResourceKey: winnerResourceKey,
		CommandID: imageRequest.CommandID, AttemptIndex: 1,
		VerificationID: imageVerificationID, ObservationGeneration: 1,
		ObservationDigest: imageObservationDigest, VerifierDigest: imageVerifierDigest,
		EvidenceState: "MATCHED", ContentIdentityMatches: true,
	}
	if err := AcceptVMImageRealizationObservation(ctx, pool, imageObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptVMImageRealizationObservation(ctx, pool, imageObservation); err != nil {
		t.Fatalf("idempotent VM Image realization observation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT image_state,network_state,boot_readiness,blocking_reasons
		FROM kim.vm_materialization_readiness_current WHERE vm_id=$1
	`, vmMaterialization.VMID).Scan(&imageState, &networkState, &bootReadiness, &blockingReasons); err != nil {
		t.Fatal(err)
	}
	if imageState != "REALIZED" || networkState != "PENDING" || bootReadiness != "BLOCKED" || len(blockingReasons) != 1 || blockingReasons[0] != "network_pending" {
		t.Fatalf("post-Image VM readiness = %s/%s/%s/%v", imageState, networkState, bootReadiness, blockingReasons)
	}
	ovsRequest := OVSPortRealizationRequest{VMID: vmMaterialization.VMID, PlanID: vmMaterialization.PlanID, PortID: winner.request.Network[0].PortID, JobID: "ovs-job-" + suffix, CommandID: "ovs-command-" + suffix}
	ovsDecision, err := PrepareOVSPortRealization(ctx, pool, ovsRequest)
	if err != nil || ovsDecision.HostID != hostID || len(ovsDecision.PayloadDigest) != 64 {
		t.Fatalf("OVS decision=%#v err=%v", ovsDecision, err)
	}
	ovsObservationDigest := digestBytes([]byte("ovs-observation-" + suffix))
	ovsVerifierDigest := digestBytes([]byte("ovs-verifier"))
	ovsVerificationID := "ovs-verification-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')`, ovsRequest.CommandID, hostID, digestBytes([]byte("ovs-lease"))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1)`, ovsRequest.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($1,$2,1,1,$3,'MATCHED',$4,jsonb_build_object('domain_uuid',$5::text,'vm_generation',1,'port_id',$6::text,'port_generation',1,'network_id',$7::text,'network_generation',1,'segment_claim_id',$8::text,'segment_generation',1,'host_mapping_generation',1,'binding_generation',1,'binding_type','OVS','mac_address',$9::text,'mtu',1500,'bridge_observed',true,'domain_nic_present',true,'domain_nic_identity_matches',true))`, ovsVerificationID, ovsRequest.CommandID, ovsObservationDigest, ovsVerifierDigest, vmMaterialization.VMID, winner.request.Network[0].PortID, networkID, segmentClaimID, winner.request.Network[0].MACAddress); err != nil {
		t.Fatal(err)
	}
	ovsObservation := OVSPortRealizationObservation{EvidenceID: "ovs-evidence-" + suffix, VMID: vmMaterialization.VMID, VMGeneration: 1, PlanID: vmMaterialization.PlanID, HostID: hostID, PortID: winner.request.Network[0].PortID, PortGeneration: 1, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentClaimID, SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, CommandID: ovsRequest.CommandID, AttemptIndex: 1, VerificationID: ovsVerificationID, ObservationGeneration: 1, ObservationDigest: ovsObservationDigest, VerifierDigest: ovsVerifierDigest, PowerJobID: "power-job-" + suffix, PowerCommandID: "power-command-" + suffix}
	if err := AcceptOVSPortRealizationAndMaybeArmPower(ctx, pool, ovsObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptOVSPortRealizationAndMaybeArmPower(ctx, pool, ovsObservation); err != nil {
		t.Fatalf("idempotent OVS realization/power authority replay: %v", err)
	}
	var desiredPower, powerType string
	if err := pool.QueryRow(ctx, `SELECT ready.network_state,ready.boot_readiness,vm.desired_power_state,command.command_type FROM kim.vm_materialization_readiness_current ready JOIN kim.virtual_machines_current vm USING(vm_id) JOIN kim.execution_jobs job ON job.job_id=$2 JOIN kim.execution_commands command ON command.job_id=job.job_id WHERE ready.vm_id=$1`, vmMaterialization.VMID, ovsObservation.PowerJobID).Scan(&networkState, &bootReadiness, &desiredPower, &powerType); err != nil {
		t.Fatal(err)
	}
	if networkState != "REALIZED" || bootReadiness != "READY" || desiredPower != "RUNNING" || powerType != "VIRTUAL_MACHINE_POWER_STATE_ENSURE" {
		t.Fatalf("READY/power=%s/%s/%s/%s", networkState, bootReadiness, desiredPower, powerType)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_network_port_realization_evidence SET preboot_state='UNKNOWN' WHERE evidence_id=$1`, ovsObservation.EvidenceID); err == nil {
		t.Fatal("immutable OVS realization evidence accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_image_realization_evidence SET observed_content_digest=$2 WHERE evidence_id=$1`, imageObservation.EvidenceID, digestBytes([]byte("forged-image"))); err == nil {
		t.Fatal("immutable VM Image evidence accepted UPDATE")
	}
	imageConflict := imageObservation
	imageConflict.ObservedDigest = digestBytes([]byte("different-image"))
	if err := AcceptVMImageRealizationObservation(ctx, pool, imageConflict); !errors.Is(err, ErrVMMaterializationConflict) {
		t.Fatalf("same VM Image evidence/different digest error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_definition_observation_evidence SET domain_present=false WHERE evidence_id=$1`, vmDefinition.EvidenceID); err == nil {
		t.Fatal("immutable VM definition evidence accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_materialization_plan_evidence SET plan_digest=$2 WHERE plan_id=$1`, vmMaterialization.PlanID, digestBytes([]byte("forged-plan"))); err == nil {
		t.Fatal("immutable VM materialization plan accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.volume_backend_binding_evidence SET lv_uuid='forged' WHERE binding_id=$1`, winnerBindingID); err == nil {
		t.Fatal("immutable Local LVM binding evidence accepted UPDATE")
	}
	digestConflict := acceptedBindingObservation
	digestConflict.ObservationDigest = digestBytes([]byte("different-observation"))
	if err := AcceptLocalLVMBindingObservation(ctx, pool, digestConflict); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("same Local LVM evidence identity/different digest error = %v", err)
	}
	otherVerificationID := "lvm-verification-other-" + suffix
	otherObservationDigest := digestBytes([]byte("lvm-observation-other-" + suffix))
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.command_verification_evidence (
			verification_id, command_id, attempt_index, observation_generation,
			observation_digest, verification_state, verifier_artifact_digest, evidence_payload
		) VALUES ($1,$2,1,2,$3,'MATCHED',$4,jsonb_build_object(
			'vg_uuid',$5::text,'observed_vg_uuid',$5::text,'observed_lv_uuid','different-lv',
			'backend_resource_key',$6::text,'observed_size_bytes',$7::bigint
		))
	`, otherVerificationID, localLVMCommandID, otherObservationDigest,
		localLVMVerifierDigest, vgUUID, winnerResourceKey,
		winner.request.Storage[0].SizeBytes); err != nil {
		t.Fatal(err)
	}
	identityConflict := acceptedBindingObservation
	identityConflict.EvidenceID = "binding-evidence-other-" + suffix
	identityConflict.VerificationID = otherVerificationID
	identityConflict.ObservationGeneration = 2
	identityConflict.ObservationDigest = otherObservationDigest
	identityConflict.LVUUID = "different-lv"
	if err := AcceptLocalLVMBindingObservation(ctx, pool, identityConflict); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("same Binding generation/different LV UUID error = %v", err)
	}
	mismatched := LocalLVMBindingObservation{
		EvidenceID: "binding-evidence-conflict-" + suffix, BindingID: winnerBindingID,
		VolumeID: winnerVolumeID, BackendID: storageBackendID, HostID: hostID,
		VGUUID: vgUUID, LVUUID: "other-lv", BackendResourceKey: winnerResourceKey,
		BindingGeneration: 1, CommandID: "lvm-command-conflict-" + suffix, VerificationID: "missing-verification", AttemptIndex: 2,
		ObservationGeneration: 2, ObservationDigest: digestBytes([]byte("lvm-conflict")),
		VerifierDigest: digestBytes([]byte("lvm-verifier")), EvidenceState: "MATCHED",
		ObservedSizeBytes: winner.request.Storage[0].SizeBytes / 2,
	}
	if err := AcceptLocalLVMBindingObservation(ctx, pool, mismatched); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("mismatched Local LVM evidence error = %v", err)
	}
	winnerAttachmentID := winner.request.Storage[0].AttachmentID
	domainUUID := "44444444-4444-4444-8444-444444444444"
	attachVerification := localLVMAttachmentVerificationFixture{
		JobID: "attach-job-" + suffix, CommandID: "attach-command-" + suffix,
		VerificationID: "attach-verification-" + suffix, AttachmentID: winnerAttachmentID,
		VolumeID: winnerVolumeID, HostID: hostID, DomainUUID: domainUUID,
		TargetDevice: "vdb", LVUUID: localLVUUID, DesiredState: "ATTACHED",
		DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true,
		HolderOpen: true, ObservationDigest: digestBytes([]byte("attach-observation-" + suffix)),
		VerifierDigest: localLVMVerifierDigest, ObservationGeneration: 1,
	}
	if err := seedLocalLVMAttachmentVerification(ctx, pool, attachVerification); err != nil {
		t.Fatal(err)
	}
	attachObservation := LocalLVMAttachmentObservation{
		EvidenceID: "attach-evidence-" + suffix, AttachmentID: winnerAttachmentID,
		VolumeID: winnerVolumeID, BindingID: winnerBindingID, HostID: hostID,
		DomainUUID: domainUUID, TargetDevice: "vdb", ObservedLVUUID: localLVUUID,
		DesiredState: "ATTACHED", CommandID: attachVerification.CommandID,
		VerificationID: attachVerification.VerificationID, ObservationDigest: attachVerification.ObservationDigest,
		VerifierDigest: localLVMVerifierDigest, EvidenceState: "MATCHED",
		AttachmentGeneration: 1, BindingGeneration: 1, ObservationGeneration: 1, AttemptIndex: 1,
		DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true, HolderOpen: true,
	}
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, attachObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, attachObservation); err != nil {
		t.Fatalf("idempotent Attachment evidence replay: %v", err)
	}
	var attachmentState, attachmentClaimState string
	if err := pool.QueryRow(ctx, `
		SELECT observation.attachment_state, claim.claim_state
		FROM kim.volume_attachment_observations_current observation
		JOIN kim.volume_attachment_claims claim USING (attachment_id)
		WHERE observation.attachment_id=$1
	`, winnerAttachmentID).Scan(&attachmentState, &attachmentClaimState); err != nil {
		t.Fatal(err)
	}
	if attachmentState != "ATTACHED" || attachmentClaimState != "ACTIVE" {
		t.Fatalf("Attachment state/Claim = %s/%s", attachmentState, attachmentClaimState)
	}
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
	detachVerification := attachVerification
	detachVerification.JobID = "detach-job-" + suffix
	detachVerification.CommandID = "detach-command-" + suffix
	detachVerification.VerificationID = "detach-verification-" + suffix
	detachVerification.DesiredState = "DETACHED"
	detachVerification.DevicePresent = false
	detachVerification.DeviceIdentityMatches = false
	detachVerification.SourceIdentityMatches = false
	detachVerification.HolderOpen = false
	detachVerification.ObservationGeneration = 2
	detachVerification.ObservationDigest = digestBytes([]byte("detach-observation-" + suffix))
	if err := seedLocalLVMAttachmentVerification(ctx, pool, detachVerification); err != nil {
		t.Fatal(err)
	}
	detachObservation := attachObservation
	detachObservation.EvidenceID = "detach-evidence-" + suffix
	detachObservation.CommandID = detachVerification.CommandID
	detachObservation.VerificationID = detachVerification.VerificationID
	detachObservation.DesiredState = "DETACHED"
	detachObservation.ObservationGeneration = 2
	detachObservation.ObservationDigest = detachVerification.ObservationDigest
	detachObservation.DevicePresent = false
	detachObservation.DeviceIdentityMatches = false
	detachObservation.SourceIdentityMatches = false
	detachObservation.HolderOpen = false
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, detachObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, detachObservation); err != nil {
		t.Fatalf("idempotent detached Attachment evidence replay: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT observation.attachment_state, claim.claim_state
		FROM kim.volume_attachment_observations_current observation
		JOIN kim.volume_attachment_claims claim USING (attachment_id)
		WHERE observation.attachment_id=$1
	`, winnerAttachmentID).Scan(&attachmentState, &attachmentClaimState); err != nil {
		t.Fatal(err)
	}
	if attachmentState != "DETACHED" || attachmentClaimState != "RELEASED" {
		t.Fatalf("detached Attachment state/Claim = %s/%s", attachmentState, attachmentClaimState)
	}
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, attachObservation); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("stale attached observation replay error = %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT observation.attachment_state, claim.claim_state
		FROM kim.volume_attachment_observations_current observation
		JOIN kim.volume_attachment_claims claim USING (attachment_id)
		WHERE observation.attachment_id=$1
	`, winnerAttachmentID).Scan(&attachmentState, &attachmentClaimState); err != nil {
		t.Fatal(err)
	}
	if attachmentState != "DETACHED" || attachmentClaimState != "RELEASED" {
		t.Fatalf("stale replay changed Attachment state/Claim = %s/%s", attachmentState, attachmentClaimState)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.volume_attachment_observation_evidence SET holder_open=true WHERE evidence_id=$1`, detachObservation.EvidenceID); err == nil {
		t.Fatal("immutable Attachment evidence accepted UPDATE")
	}
}

func cloneStorageRequirements(requirements []placement.StorageRequirement) []placement.StorageRequirement {
	result := make([]placement.StorageRequirement, len(requirements))
	copy(result, requirements)
	return result
}

type localLVMVerificationFixture struct {
	JobID, VolumeID, HostID, CommandID, VerificationID string
	ObservationDigest, VerifierDigest                  string
	VGUUID, LVUUID, ResourceKey                        string
	SizeBytes                                          uint64
}

func seedLocalLVMVerification(ctx context.Context, pool *pgxpool.Pool, fixture localLVMVerificationFixture) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs (job_id, resource_type, resource_id, desired_revision, job_state) VALUES ($1,'VOLUME',$2,1,'SUCCEEDED')`, fixture.JobID, fixture.VolumeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_commands (
				command_id, job_id, host_id, command_type, schema_version,
				target_resource_id, payload, payload_digest
			) VALUES ($1,$2,$3,'LOCAL_LVM_VOLUME_ENSURE','kim.command.local-lvm-volume-ensure/v1',$4,'{}'::jsonb,$5)
		`, fixture.CommandID, fixture.JobID, fixture.HostID, "volume:"+fixture.VolumeID, digestBytes([]byte("local-lvm-command"))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current (command_id, command_state, current_attempt_index) VALUES ($1,'SUCCEEDED',1)`, fixture.CommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, fixture.JobID, fixture.CommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.command_lease_grants (
				command_id, lease_generation, attempt_index, host_id,
				host_authority_generation, session_generation, token_digest,
				not_before, expires_at
			) VALUES ($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')
		`, fixture.CommandID, fixture.HostID, digestBytes([]byte("local-lvm-lease"))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.command_attempts (command_id, attempt_index, lease_generation, host_authority_generation, session_generation) VALUES ($1,1,1,1,1)`, fixture.CommandID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.command_verification_evidence (
				verification_id, command_id, attempt_index, observation_generation,
				observation_digest, verification_state, verifier_artifact_digest, evidence_payload
			) VALUES ($1,$2,1,1,$3,'MATCHED',$4,jsonb_build_object(
				'vg_uuid',$5::text,'observed_vg_uuid',$5::text,'observed_lv_uuid',$6::text,
				'backend_resource_key',$7::text,'observed_size_bytes',$8::bigint
			))
		`, fixture.VerificationID, fixture.CommandID, fixture.ObservationDigest,
			fixture.VerifierDigest, fixture.VGUUID, fixture.LVUUID,
			fixture.ResourceKey, fixture.SizeBytes)
		return err
	})
}

type localLVMAttachmentVerificationFixture struct {
	JobID, CommandID, VerificationID, AttachmentID, VolumeID, HostID string
	DomainUUID, TargetDevice, LVUUID, DesiredState                   string
	ObservationDigest, VerifierDigest                                string
	ObservationGeneration                                            uint64
	DevicePresent, DeviceIdentityMatches, SourceIdentityMatches      bool
	HolderOpen, ReadOnly                                             bool
}

func seedLocalLVMAttachmentVerification(ctx context.Context, pool *pgxpool.Pool, fixture localLVMAttachmentVerificationFixture) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs (job_id, resource_type, resource_id, desired_revision, job_state) VALUES ($1,'VOLUME_ATTACHMENT',$2,1,'SUCCEEDED')`, fixture.JobID, fixture.AttachmentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_commands (
				command_id, job_id, host_id, command_type, schema_version,
				target_resource_id, payload, payload_digest
			) VALUES ($1,$2,$3,'LOCAL_LVM_VOLUME_ATTACHMENT_ENSURE','kim.command.local-lvm-volume-attachment/v1',$4,'{}'::jsonb,$5)
		`, fixture.CommandID, fixture.JobID, fixture.HostID, "attachment:"+fixture.AttachmentID, digestBytes([]byte(fixture.CommandID))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current (command_id, command_state, current_attempt_index) VALUES ($1,'SUCCEEDED',1)`, fixture.CommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, fixture.JobID, fixture.CommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.command_lease_grants (
				command_id, lease_generation, attempt_index, host_id,
				host_authority_generation, session_generation, token_digest,
				not_before, expires_at
			) VALUES ($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')
		`, fixture.CommandID, fixture.HostID, digestBytes([]byte("attachment-lease-"+fixture.CommandID))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.command_attempts (command_id, attempt_index, lease_generation, host_authority_generation, session_generation) VALUES ($1,1,1,1,1)`, fixture.CommandID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.command_verification_evidence (
				verification_id, command_id, attempt_index, observation_generation,
				observation_digest, verification_state, verifier_artifact_digest, evidence_payload
			) VALUES ($1,$2,1,$3,$4,'MATCHED',$5,jsonb_build_object(
				'attachment_id',$6::text,'volume_id',$7::text,'domain_uuid',$8::text,
				'target_device',$9::text,'observed_lv_uuid',$10::text,'desired_state',$11::text,
				'device_present',$12::boolean,'device_identity_matches',$13::boolean,
				'source_identity_matches',$14::boolean,'holder_open',$15::boolean,'read_only',$16::boolean
			))
		`, fixture.VerificationID, fixture.CommandID, fixture.ObservationGeneration,
			fixture.ObservationDigest, fixture.VerifierDigest, fixture.AttachmentID,
			fixture.VolumeID, fixture.DomainUUID, fixture.TargetDevice, fixture.LVUUID,
			fixture.DesiredState, fixture.DevicePresent, fixture.DeviceIdentityMatches,
			fixture.SourceIdentityMatches, fixture.HolderOpen, fixture.ReadOnly)
		return err
	})
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
