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
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

type nestedTestTxBeginner struct {
	pgx.Tx
}

func (beginner nestedTestTxBeginner) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return beginner.Tx.Begin(ctx)
}

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
	var storageBaseline [6]int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.volumes_current),
		(SELECT count(*) FROM kim.storage_capacity_claims),
		(SELECT count(*) FROM kim.volume_backend_binding_intents),
		(SELECT count(*) FROM kim.volume_attachments_current),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.volume_backend_binding_intents WHERE observed_lv_uuid IS NOT NULL)`).Scan(&storageBaseline[0], &storageBaseline[1], &storageBaseline[2], &storageBaseline[3], &storageBaseline[4], &storageBaseline[5]); err != nil {
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
	networkID, subnetID, autoSubnetID, segmentClaimID := "network-"+suffix, "subnet-"+suffix, "subnet-auto-"+suffix, "segment-"+suffix
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.network_subnets_current(subnet_id,network_id,subnet_generation,lifecycle_state,cidr,allocation_start,allocation_end,excluded_addresses) VALUES($1,$2,1,'ACTIVE','192.0.2.0/24','192.0.2.220','192.0.2.230',ARRAY[]::inet[])`, autoSubnetID, networkID); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostNetworkMapping(ctx, pool, HostNetworkMapping{
		HostID: hostID, SegmentClaimID: segmentClaimID, Generation: 1,
		State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS", "SRIOV_DIRECT"},
		OVNChassisName: "ovn-chassis-" + suffix,
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
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "default", PolicyGeneration: 1}); err != nil {
		t.Fatalf("idempotent Placement Pool replay: %v", err)
	}
	if err := AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: hostID, PoolID: poolID, Generation: 1, State: "ACTIVE"}); err != nil {
		t.Fatalf("idempotent Placement Pool membership replay: %v", err)
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
			BindingType: "SRIOV_DIRECT", DeviceAddress: "0000:03:00.1", RequiredMTU: 1500,
		}},
		Storage: []placement.StorageRequirement{{
			VolumeID: "volume-" + suffix, AttachmentID: "attachment-" + suffix,
			BackendID: storageBackendID, BackendGeneration: 1, VGUUID: vgUUID,
			StorageClassID: storageClassID, StorageClassRevision: 1,
			CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1,
			SizeBytes: 8 << 30, AccessMode: "SINGLE_WRITER", Bootable: true,
		}},
	}
	// Automatic IPAM is evaluated without a mutation, then assigns concrete IP
	// and MAC identities only inside the same Final Admission transaction.
	automaticRequest := request
	automaticRequest.RequestID = "request-automatic-" + suffix
	automaticRequest.WorkloadID = "vm-automatic-" + suffix
	automaticRequest.PCI = nil
	automaticRequest.Storage = nil
	automaticRequest.Network = []placement.NetworkRequirement{{
		PortID: "port-automatic-" + suffix, NetworkID: networkID, NetworkGeneration: 1,
		SubnetID: autoSubnetID, SubnetGeneration: 1,
		SegmentClaimID: segmentClaimID, SegmentGeneration: 1, HostMappingGeneration: 1,
		AllocationSource: "AUTOMATIC", BindingType: "OVS", RequiredMTU: 1500,
	}}
	automaticBefore := placementMutationCounts(t, ctx, pool)
	automaticDry, err := DryEvaluatePlacement(ctx, pool, automaticRequest, hostID)
	if err != nil || !automaticDry.Eligible {
		t.Fatalf("automatic IPAM dry evaluation/error = %#v/%v", automaticDry, err)
	}
	if afterDry := placementMutationCounts(t, ctx, pool); afterDry != automaticBefore {
		t.Fatalf("automatic IPAM dry evaluation mutated authority: before=%v after=%v", automaticBefore, afterDry)
	}
	automaticAdmission, err := FinalAdmitPlacement(ctx, pool, automaticRequest, automaticDry)
	if err != nil {
		t.Fatal(err)
	}
	var automaticIP, automaticMAC, ipSource, macSource string
	if err := pool.QueryRow(ctx, `
		SELECT host(ip.ip_address), mac.mac_address::text, ip.allocation_source, mac.allocation_source
		FROM kim.network_identity_claims ip
		JOIN kim.network_identity_claims mac ON mac.port_id=ip.port_id AND mac.claim_type='MAC'
		WHERE ip.port_id=$1 AND ip.claim_type='IP'
	`, automaticRequest.Network[0].PortID).Scan(&automaticIP, &automaticMAC, &ipSource, &macSource); err != nil {
		t.Fatal(err)
	}
	if ipSource != "AUTOMATIC" || macSource != "AUTOMATIC" || automaticIP == "" || automaticMAC == "" || automaticIP == "192.0.2.1" {
		t.Fatalf("automatic identities = %s/%s source=%s/%s", automaticIP, automaticMAC, ipSource, macSource)
	}
	replayedAutomatic, err := FinalAdmitPlacement(ctx, pool, automaticRequest, automaticDry)
	if err != nil || replayedAutomatic.AdmissionID != automaticAdmission.AdmissionID {
		t.Fatalf("automatic IPAM idempotent replay = %#v/%v", replayedAutomatic, err)
	}

	verifierDigest := digestBytes([]byte("network-release-verifier"))
	_, err = RecordNetworkIdentityReleaseObservation(ctx, pool, NetworkIdentityReleaseObservation{
		ObservationID:   "release-before-request-" + suffix,
		IdentityClaimID: "ip:" + automaticRequest.RequestID + ":" + automaticRequest.Network[0].PortID,
		PortID:          automaticRequest.Network[0].PortID,
		ClaimGeneration: 1, PortGeneration: 1, BindingGeneration: 1,
		ObservationGeneration: 1, EvidenceState: "MATCHED",
		PortAbsent: true, BindingAbsent: true, OVNNBAbsent: true,
		OVNSBAbsent: true, HostAbsent: true,
		VerifierArtifactDigest: verifierDigest, ObservedAt: time.Unix(1, 0).UTC(),
	})
	if !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("release evidence before release request error = %v", err)
	}
	if err := BeginNetworkPortRelease(ctx, pool, automaticRequest.Network[0].PortID, 1); err != nil {
		t.Fatal(err)
	}
	recordRelease := func(claimType string, generation uint64, state string, absent bool) string {
		t.Helper()
		claimID := claimType + ":" + automaticRequest.RequestID + ":" + automaticRequest.Network[0].PortID
		result, err := RecordNetworkIdentityReleaseObservation(ctx, pool, NetworkIdentityReleaseObservation{
			ObservationID:   "release-" + claimType + "-" + fmt.Sprint(generation) + "-" + suffix,
			IdentityClaimID: claimID, PortID: automaticRequest.Network[0].PortID,
			ClaimGeneration: 1, PortGeneration: 1, BindingGeneration: 1,
			ObservationGeneration: generation, EvidenceState: state,
			PortAbsent: absent, BindingAbsent: absent, OVNNBAbsent: absent,
			OVNSBAbsent: absent, HostAbsent: absent,
			VerifierArtifactDigest: verifierDigest, ObservedAt: time.Unix(int64(generation), 0).UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	reuseRequest := automaticRequest
	reuseRequest.RequestID = "request-reuse-" + suffix
	reuseRequest.WorkloadID = "vm-reuse-" + suffix
	reuseRequest.Network = append([]placement.NetworkRequirement(nil), automaticRequest.Network...)
	reuseRequest.Network[0].PortID = "port-reuse-" + suffix
	reuseRequest.Network[0].AllocationSource = "EXPLICIT"
	reuseRequest.Network[0].IPAddress = automaticIP
	reuseRequest.Network[0].MACAddress = automaticMAC
	for _, claimType := range []string{"ip", "mac"} {
		if state := recordRelease(claimType, 1, "UNKNOWN", false); state != "QUARANTINED" {
			t.Fatalf("%s UNKNOWN release state = %s", claimType, state)
		}
	}
	quarantinedDry, err := DryEvaluatePlacement(ctx, pool, reuseRequest, hostID)
	if err != nil || quarantinedDry.Eligible || !containsReason(quarantinedDry.ReasonCodes, "network:"+reuseRequest.Network[0].PortID+":identity_claim_conflict") {
		t.Fatalf("quarantined identity reuse evaluation/error = %#v/%v", quarantinedDry, err)
	}
	for _, claimType := range []string{"ip", "mac"} {
		if state := recordRelease(claimType, 2, "MATCHED", true); state != "QUARANTINED" {
			t.Fatalf("%s first clean release state = %s", claimType, state)
		}
		_, err := RecordNetworkIdentityReleaseObservation(ctx, pool, NetworkIdentityReleaseObservation{
			ObservationID:   "release-duplicate-generation-" + claimType + "-" + suffix,
			IdentityClaimID: claimType + ":" + automaticRequest.RequestID + ":" + automaticRequest.Network[0].PortID,
			PortID:          automaticRequest.Network[0].PortID,
			ClaimGeneration: 1, PortGeneration: 1, BindingGeneration: 1,
			ObservationGeneration: 2, EvidenceState: "MATCHED",
			PortAbsent: true, BindingAbsent: true, OVNNBAbsent: true,
			OVNSBAbsent: true, HostAbsent: true,
			VerifierArtifactDigest: verifierDigest, ObservedAt: time.Unix(22, 0).UTC(),
		})
		if !errors.Is(err, ErrPlacementStale) {
			t.Fatalf("%s duplicate observation generation error = %v", claimType, err)
		}
		if state := recordRelease(claimType, 3, "MATCHED", true); state != "RELEASED" {
			t.Fatalf("%s second clean release state = %s", claimType, state)
		}
		if state := recordRelease(claimType, 3, "MATCHED", true); state != "RELEASED" {
			t.Fatalf("%s terminal observation replay state = %s", claimType, state)
		}
		_, err = RecordNetworkIdentityReleaseObservation(ctx, pool, NetworkIdentityReleaseObservation{
			ObservationID:   "release-post-terminal-" + claimType + "-" + suffix,
			IdentityClaimID: claimType + ":" + automaticRequest.RequestID + ":" + automaticRequest.Network[0].PortID,
			PortID:          automaticRequest.Network[0].PortID,
			ClaimGeneration: 1, PortGeneration: 1, BindingGeneration: 1,
			ObservationGeneration: 4, EvidenceState: "UNKNOWN",
			VerifierArtifactDigest: verifierDigest, ObservedAt: time.Unix(4, 0).UTC(),
		})
		if !errors.Is(err, ErrPlacementStale) {
			t.Fatalf("%s post-terminal observation error = %v", claimType, err)
		}
	}
	var releasedPort, releasedBinding string
	if err := pool.QueryRow(ctx, `
		SELECT port.desired_state, binding.binding_state
		FROM kim.network_ports_current port
		JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id
		WHERE port.port_id=$1
	`, automaticRequest.Network[0].PortID).Scan(&releasedPort, &releasedBinding); err != nil {
		t.Fatal(err)
	}
	if releasedPort != "RELEASED" || releasedBinding != "RELEASED" {
		t.Fatalf("released Port/Binding = %s/%s", releasedPort, releasedBinding)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='RELEASED' WHERE admission_id=$1`, automaticAdmission.AdmissionID); err != nil {
		t.Fatal(err)
	}

	reuseDry, err := DryEvaluatePlacement(ctx, pool, reuseRequest, hostID)
	if err != nil || !reuseDry.Eligible {
		t.Fatalf("released identity reuse dry evaluation/error = %#v/%v", reuseDry, err)
	}
	reuseAdmission, err := FinalAdmitPlacement(ctx, pool, reuseRequest, reuseDry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='RELEASED' WHERE admission_id=$1`, reuseAdmission.AdmissionID); err != nil {
		t.Fatal(err)
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

	membershipCurrent, err := DryEvaluatePlacement(ctx, pool, request, hostID)
	if err != nil || !membershipCurrent.Eligible {
		t.Fatalf("post-membership dry evaluation/error = %#v/%v", membershipCurrent, err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{
		HostGroupID: poolID, Generation: 2, GroupType: "PLACEMENT_POOL",
		Dimension: "service-class", Level: "pool", LifecycleState: "ACTIVE",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacement(ctx, pool, request, membershipCurrent); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("stale HostGroup generation admission error = %v", err)
	}
	if counts := placementMutationCounts(t, ctx, pool); counts != before {
		t.Fatalf("stale HostGroup Final Admission left partial authority: %v", counts)
	}
	if err := AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: hostID, PoolID: poolID, Generation: 3, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}

	hierarchyTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hierarchyDB := nestedTestTxBeginner{Tx: hierarchyTx}
	preHierarchy, err := DryEvaluatePlacement(ctx, hierarchyDB, request, hostID)
	if err != nil || !preHierarchy.Eligible {
		t.Fatalf("pre-hierarchy dry evaluation/error = %#v/%v", preHierarchy, err)
	}
	hierarchyRequest := HostGroupHierarchyRequest{
		PublishRequestID: "placement-hierarchy-1-" + suffix,
		HierarchyID:      "placement-hierarchy-" + suffix,
		GroupType:        "PLACEMENT_POOL", Dimension: "service-class",
		ScopeType: "SYSTEM", ScopeID: "system", GraphMode: "TREE",
		ExpectedCurrentGeneration: 0, Levels: []string{"pool"}, NodeGroupIDs: []string{poolID},
	}
	hierarchy1, err := PublishHostGroupHierarchy(ctx, hierarchyDB, hierarchyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacement(ctx, hierarchyDB, request, preHierarchy); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("unbound membership set after hierarchy publish admission error = %v", err)
	}
	if counts := placementMutationCounts(t, ctx, hierarchyTx); counts != before {
		t.Fatalf("hierarchy publish stale admission left partial authority: %v", counts)
	}
	if err := AssignHostPlacementPool(ctx, hierarchyDB, HostPlacementMembership{HostID: hostID, PoolID: poolID, Generation: 3, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	hierarchyCurrent, err := DryEvaluatePlacement(ctx, hierarchyDB, request, hostID)
	if err != nil || !hierarchyCurrent.Eligible || hierarchyCurrent.HierarchyGeneration != 1 {
		t.Fatalf("hierarchy-bound dry evaluation/error = %#v/%v", hierarchyCurrent, err)
	}
	hierarchyRequest.PublishRequestID = "placement-hierarchy-2-" + suffix
	hierarchyRequest.HierarchyID = "placement-hierarchy-2-" + suffix
	hierarchyRequest.ExpectedCurrentGeneration = hierarchy1.HierarchyGeneration
	if _, err := PublishHostGroupHierarchy(ctx, hierarchyDB, hierarchyRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacement(ctx, hierarchyDB, request, hierarchyCurrent); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("stale hierarchy generation admission error = %v", err)
	}
	if counts := placementMutationCounts(t, ctx, hierarchyTx); counts != before {
		t.Fatalf("stale hierarchy Final Admission left partial authority: %v", counts)
	}
	if err := hierarchyTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	current, err := DryEvaluatePlacement(ctx, pool, request, hostID)
	if err != nil || !current.Eligible {
		t.Fatalf("current dry evaluation/error = %#v/%v", current, err)
	}
	setPeerHost := "host-set-peer-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, setPeerHost); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "placement-set-only-switch-" + suffix,
		HostGroupID:      poolID, BasedOnHostGroupGeneration: 2,
		ExpectedCurrentSetGeneration: current.MembershipSetGeneration,
		SourceType:                   "PLACEMENT_POOL_COMPAT", SourceRevision: "set-only-switch",
		Members: []HostGroupMembership{
			{HostGroupID: poolID, HostID: hostID, Generation: 3, State: "ACTIVE", SourceType: "PLACEMENT_POOL_COMPAT", SourceRevision: "3"},
			{HostGroupID: poolID, HostID: setPeerHost, Generation: 1, State: "ACTIVE", SourceType: "PLACEMENT_POOL_COMPAT", SourceRevision: "set-only-switch"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacement(ctx, pool, request, current); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("stale membership-set-only admission error = %v", err)
	}
	if counts := placementMutationCounts(t, ctx, pool); counts != before {
		t.Fatalf("stale membership-set Final Admission left partial authority: %v", counts)
	}
	current, err = DryEvaluatePlacement(ctx, pool, request, hostID)
	if err != nil || !current.Eligible {
		t.Fatalf("post-set-switch dry evaluation/error = %#v/%v", current, err)
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
	if decisions != 5 || claims != 5 {
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
	if ports != 5 || identities != 10 || bindings != 5 {
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
	if volumes != storageBaseline[0]+3 || capacityClaims != storageBaseline[1]+3 || storageBindings != storageBaseline[2]+3 || attachments != storageBaseline[3]+3 || attachmentClaims != storageBaseline[4]+3 || prematureLVUUIDs != storageBaseline[5] {
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
	sriovRequest := SRIOVPortRealizationRequest{VMID: vmMaterialization.VMID, PlanID: vmMaterialization.PlanID, PortID: winner.request.Network[0].PortID, JobID: "sriov-job-" + suffix, CommandID: "sriov-command-" + suffix}
	sriovDecision, err := PrepareSRIOVPortRealization(ctx, pool, sriovRequest)
	if err != nil || sriovDecision.HostID != hostID || len(sriovDecision.PayloadDigest) != 64 {
		t.Fatalf("SR-IOV decision=%#v err=%v", sriovDecision, err)
	}
	sriovObservationDigest := digestBytes([]byte("sriov-observation-" + suffix))
	sriovVerifierDigest := digestBytes([]byte("sriov-verifier"))
	sriovVerificationID := "sriov-verification-" + suffix
	var vfClaimID string
	if err := pool.QueryRow(ctx, `SELECT claim_id FROM kim.pci_vf_allocation_claims WHERE placement_admission_id=$1 AND device_address='0000:03:00.1'`, winner.admission.AdmissionID).Scan(&vfClaimID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')`, sriovRequest.CommandID, hostID, digestBytes([]byte("sriov-lease"))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1)`, sriovRequest.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($1,$2,1,1,$3,'MATCHED',$4,jsonb_build_object('domain_uuid',$5::text,'vm_generation',1,'port_id',$6::text,'network_id',$7::text,'segment_claim_id',$8::text,'mac_address',$9::text,'device_address','0000:03:00.1','vf_claim_id',$10::text,'qualification_id',$11::text,'policy_id','vf-policy','domain_hostdev_identity_matches',true))`, sriovVerificationID, sriovRequest.CommandID, sriovObservationDigest, sriovVerifierDigest, vmMaterialization.VMID, winner.request.Network[0].PortID, networkID, segmentClaimID, winner.request.Network[0].MACAddress, vfClaimID, qualificationID); err != nil {
		t.Fatal(err)
	}
	sriovObservation := SRIOVPortRealizationObservation{EvidenceID: "sriov-evidence-" + suffix, VMID: vmMaterialization.VMID, VMGeneration: 1, PlanID: vmMaterialization.PlanID, HostID: hostID, PortID: winner.request.Network[0].PortID, PortGeneration: 1, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentClaimID, SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, DeviceAddress: "0000:03:00.1", VFClaimID: vfClaimID, PCIObservationGeneration: 1, QualificationID: qualificationID, QualificationRevision: 1, PolicyID: "vf-policy", PolicyGeneration: 1, CommandID: sriovRequest.CommandID, AttemptIndex: 1, VerificationID: sriovVerificationID, ObservationGeneration: 1, ObservationDigest: sriovObservationDigest, VerifierDigest: sriovVerifierDigest, PowerJobID: "power-job-" + suffix, PowerCommandID: "power-command-" + suffix}
	if err := AcceptSRIOVPortRealizationAndMaybeArmPower(ctx, pool, sriovObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptSRIOVPortRealizationAndMaybeArmPower(ctx, pool, sriovObservation); err != nil {
		t.Fatalf("idempotent SR-IOV realization/power authority replay: %v", err)
	}
	var desiredPower, powerType string
	if err := pool.QueryRow(ctx, `SELECT ready.network_state,ready.boot_readiness,vm.desired_power_state,command.command_type FROM kim.vm_materialization_readiness_current ready JOIN kim.virtual_machines_current vm USING(vm_id) JOIN kim.execution_jobs job ON job.job_id=$2 JOIN kim.execution_commands command ON command.job_id=job.job_id WHERE ready.vm_id=$1`, vmMaterialization.VMID, sriovObservation.PowerJobID).Scan(&networkState, &bootReadiness, &desiredPower, &powerType); err != nil {
		t.Fatal(err)
	}
	if networkState != "REALIZED" || bootReadiness != "READY" || desiredPower != "RUNNING" || powerType != "VIRTUAL_MACHINE_POWER_STATE_ENSURE" {
		t.Fatalf("READY/power=%s/%s/%s/%s", networkState, bootReadiness, desiredPower, powerType)
	}
	powerObservationDigest := digestBytes([]byte("power-observation-" + suffix))
	powerVerifierDigest := digestBytes([]byte("power-verifier"))
	powerVerificationID := "power-verification-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')`, sriovObservation.PowerCommandID, hostID, digestBytes([]byte("power-lease"))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1)`, sriovObservation.PowerCommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state='UNKNOWN',current_attempt_index=1 WHERE command_id=$1`, sriovObservation.PowerCommandID); err != nil {
		t.Fatal(err)
	}
	if err := RecordCommandVerification(ctx, pool, CommandVerification{
		VerificationID: powerVerificationID, CommandID: sriovObservation.PowerCommandID, AttemptIndex: 1,
		ObservationGeneration: 1, ObservationDigest: powerObservationDigest, State: "MATCHED",
		VerifierArtifactDigest: powerVerifierDigest,
		Evidence:               map[string]any{"domain_uuid": vmMaterialization.VMID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"},
	}); err != nil {
		t.Fatal(err)
	}
	var observedPower, convergenceState string
	if err := pool.QueryRow(ctx, `SELECT observed_power_state,convergence_state FROM kim.vm_power_state_current WHERE vm_id=$1`, vmMaterialization.VMID).Scan(&observedPower, &convergenceState); err != nil {
		t.Fatal(err)
	}
	if observedPower != "RUNNING" || convergenceState != "MATCHED" {
		t.Fatalf("VM runtime power projection=%s/%s", observedPower, convergenceState)
	}

	// Post-boot OVS convergence is separate from pre-boot realization and VM
	// RUNNING. Seed a second, already-realized OVS Port, then require a fresh
	// typed active-domain/OVS read-back before advancing its dataplane state.
	ovsPortID := "ovs-dataplane-port-" + suffix
	prebootCommandID := "ovs-preboot-command-" + suffix
	prebootVerificationID := "ovs-preboot-verification-" + suffix
	prebootEvidenceID := "ovs-preboot-evidence-" + suffix
	prebootJobID := "ovs-preboot-job-" + suffix
	prebootObservationDigest := digestBytes([]byte("ovs-preboot-observation"))
	prebootVerifierDigest := digestBytes([]byte("ovs-preboot-verifier"))
	prebootBatch := &pgx.Batch{}
	prebootBatch.Queue(`INSERT INTO kim.network_ports_current(port_id,placement_admission_id,project_id,workload_id,network_id,subnet_id,port_generation,desired_state) VALUES($1,$2,'project',$3,$4,$5,1,'RESERVED')`, ovsPortID, winner.admission.AdmissionID, vmMaterialization.VMID, networkID, subnetID)
	prebootBatch.Queue(`INSERT INTO kim.network_identity_claims(identity_claim_id,placement_admission_id,port_id,project_id,network_id,subnet_id,claim_type,mac_address,allocation_source,claim_generation,claim_state) VALUES($1,$2,$3,'project',$4,$5,'MAC','02:00:00:00:00:22','EXPLICIT',1,'RESERVED')`, ovsPortID+"/mac", winner.admission.AdmissionID, ovsPortID, networkID, subnetID)
	prebootBatch.Queue(`INSERT INTO kim.network_identity_claims(identity_claim_id,placement_admission_id,port_id,project_id,network_id,subnet_id,claim_type,ip_address,allocation_source,claim_generation,claim_state) VALUES($1,$2,$3,'project',$4,$5,'IP','192.0.2.22','EXPLICIT',1,'RESERVED')`, ovsPortID+"/ip", winner.admission.AdmissionID, ovsPortID, networkID, subnetID)
	prebootBatch.Queue(`INSERT INTO kim.port_bindings_current(port_id,placement_admission_id,host_id,segment_claim_id,binding_generation,binding_type,binding_state) VALUES($1,$2,$3,$4,1,'OVS','RESERVED')`, ovsPortID, winner.admission.AdmissionID, hostID, segmentClaimID)
	prebootBatch.Queue(`INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VM_NETWORK_PORT',$2,1,'SUCCEEDED')`, prebootJobID, ovsPortID)
	prebootBatch.Queue(`INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,'NETWORK_PORT_OVS_REALIZE','kim.command.network-port-ovs-realize/v1',$4,'{}',$5)`, prebootCommandID, prebootJobID, hostID, "port:"+ovsPortID, digestBytes([]byte("ovs-preboot-payload")))
	prebootBatch.Queue(`INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')`, prebootCommandID, hostID, digestBytes([]byte("ovs-preboot-lease")))
	prebootBatch.Queue(`INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1)`, prebootCommandID)
	prebootBatch.Queue(`INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($1,$2,1,1,$3,'MATCHED',$4,'{}')`, prebootVerificationID, prebootCommandID, prebootObservationDigest, prebootVerifierDigest)
	prebootBatch.Queue(`INSERT INTO kim.vm_network_port_realization_evidence(evidence_id,vm_id,vm_generation,plan_id,host_id,port_id,port_generation,network_id,network_generation,segment_claim_id,segment_generation,host_mapping_generation,binding_generation,binding_type,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,preboot_state) VALUES($1,$2,1,$3,$4,$5,1,$6,1,$7,1,1,1,'OVS',$8,1,$9,1,$10,$11,'REALIZED')`, prebootEvidenceID, vmMaterialization.VMID, vmMaterialization.PlanID, hostID, ovsPortID, networkID, segmentClaimID, prebootCommandID, prebootVerificationID, prebootObservationDigest, prebootVerifierDigest)
	prebootBatch.Queue(`INSERT INTO kim.vm_network_port_realizations_current(vm_id,vm_generation,port_id,port_generation,binding_generation,observation_generation,evidence_id,preboot_state) VALUES($1,1,$2,1,1,1,$3,'REALIZED')`, vmMaterialization.VMID, ovsPortID, prebootEvidenceID)
	prebootResults := pool.SendBatch(ctx, prebootBatch)
	for range 11 {
		if _, err := prebootResults.Exec(); err != nil {
			_ = prebootResults.Close()
			t.Fatal(err)
		}
	}
	if err := prebootResults.Close(); err != nil {
		t.Fatal(err)
	}
	var dataplaneExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_port_dataplane_state_current WHERE vm_id=$1 AND port_id=$2)`, vmMaterialization.VMID, ovsPortID).Scan(&dataplaneExists); err != nil || dataplaneExists {
		t.Fatalf("RUNNING/pre-boot REALIZED implicitly converged dataplane: %v/%v", dataplaneExists, err)
	}
	dataplaneRequest := OVSDataplaneRequest{VMID: vmMaterialization.VMID, PlanID: vmMaterialization.PlanID, PortID: ovsPortID, JobID: "ovs-dataplane-job-" + suffix, CommandID: "ovs-dataplane-command-" + suffix}
	dataplaneDecision, err := PrepareOVSDataplaneObservation(ctx, pool, dataplaneRequest)
	if err != nil || dataplaneDecision.HostID != hostID || len(dataplaneDecision.PayloadDigest) != 64 {
		t.Fatalf("OVS dataplane decision=%#v err=%v", dataplaneDecision, err)
	}
	if replay, err := PrepareOVSDataplaneObservation(ctx, pool, dataplaneRequest); err != nil || replay != dataplaneDecision {
		t.Fatalf("idempotent OVS dataplane preparation=%#v/%v want %#v", replay, err, dataplaneDecision)
	}
	dataplaneObservationDigest := digestBytes([]byte("ovs-dataplane-observation-" + suffix))
	dataplaneVerifierDigest := digestBytes([]byte("ovs-dataplane-verifier"))
	dataplaneVerificationID := "ovs-dataplane-verification-" + suffix
	dataplaneBatch := &pgx.Batch{}
	dataplaneBatch.Queue(`INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')`, dataplaneRequest.CommandID, hostID, digestBytes([]byte("ovs-dataplane-lease")))
	dataplaneBatch.Queue(`INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1)`, dataplaneRequest.CommandID)
	dataplaneBatch.Queue(`INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload)
		VALUES($1,$2,1,1,$3,'MATCHED',$4,jsonb_build_object(
		 'domain_uuid',$5::text,'vm_generation',1,'port_id',$6::text,'port_generation',1,
		 'network_id',$7::text,'network_generation',1,'segment_claim_id',$8::text,'segment_generation',1,
		 'host_mapping_generation',1,'binding_generation',1,'binding_type','OVS','mac_address','02:00:00:00:00:22',
		 'domain_running',true,'interface_present',true,'target_device','vnet42',
		 'bridge_observed','br-int','bridge_matches',true,'link_state','up','interface_id',$6::text
		))
	`, dataplaneVerificationID, dataplaneRequest.CommandID, dataplaneObservationDigest, dataplaneVerifierDigest, vmMaterialization.VMID, ovsPortID, networkID, segmentClaimID)
	dataplaneResults := pool.SendBatch(ctx, dataplaneBatch)
	for range 3 {
		if _, err := dataplaneResults.Exec(); err != nil {
			_ = dataplaneResults.Close()
			t.Fatal(err)
		}
	}
	if err := dataplaneResults.Close(); err != nil {
		t.Fatal(err)
	}
	dataplaneObservation := OVSDataplaneObservation{
		EvidenceID: "ovs-dataplane-evidence-" + suffix, VMID: vmMaterialization.VMID, VMGeneration: 1,
		PlanID: vmMaterialization.PlanID, HostID: hostID, PortID: ovsPortID, PortGeneration: 1,
		NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentClaimID, SegmentGeneration: 1,
		HostMappingGeneration: 1, BindingGeneration: 1, CommandID: dataplaneRequest.CommandID,
		AttemptIndex: 1, VerificationID: dataplaneVerificationID, ObservationGeneration: 1,
		ObservationDigest: dataplaneObservationDigest, VerifierDigest: dataplaneVerifierDigest,
	}
	if err := AcceptOVSDataplaneObservation(ctx, pool, dataplaneObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptOVSDataplaneObservation(ctx, pool, dataplaneObservation); err != nil {
		t.Fatalf("idempotent OVS dataplane observation: %v", err)
	}
	var dataplaneState, targetDevice string
	if err := pool.QueryRow(ctx, `SELECT current.convergence_state,evidence.target_device FROM kim.vm_port_dataplane_state_current current JOIN kim.vm_port_dataplane_observation_evidence evidence USING(evidence_id) WHERE current.vm_id=$1 AND current.port_id=$2`, vmMaterialization.VMID, ovsPortID).Scan(&dataplaneState, &targetDevice); err != nil || dataplaneState != "CONVERGED" || targetDevice != "vnet42" {
		t.Fatalf("OVS dataplane projection=%s/%s err=%v", dataplaneState, targetDevice, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_port_dataplane_observation_evidence SET target_device='forged' WHERE evidence_id=$1`, dataplaneObservation.EvidenceID); err == nil {
		t.Fatal("immutable OVS dataplane evidence accepted UPDATE")
	}
	dataplaneConflict := dataplaneObservation
	dataplaneConflict.ObservationDigest = digestBytes([]byte("different-ovs-dataplane"))
	if err := AcceptOVSDataplaneObservation(ctx, pool, dataplaneConflict); !errors.Is(err, ErrVMMaterializationConflict) {
		t.Fatalf("same OVS dataplane evidence/different digest error = %v", err)
	}
	ovnIntentRequest := OVNPortIntentRequest{IntentID: "ovn-intent-" + suffix, IntentGeneration: 1, PortID: ovsPortID}
	ovnIntent, err := CommitOVNPortIntent(ctx, pool, ovnIntentRequest)
	if err != nil || ovnIntent.PortID != ovsPortID || len(ovnIntent.ObjectSetDigest) != 64 || len(ovnIntent.CanonicalObjectSet) == 0 {
		t.Fatalf("OVN Port intent=%#v err=%v", ovnIntent, err)
	}
	if replay, err := CommitOVNPortIntent(ctx, pool, ovnIntentRequest); err != nil || replay.ObjectSetDigest != ovnIntent.ObjectSetDigest {
		t.Fatalf("idempotent OVN Port intent=%#v/%v want %#v", replay, err, ovnIntent)
	}
	var ovnLayerStatus string
	if err := pool.QueryRow(ctx, `SELECT layer_status FROM kim.network_ovn_state_current WHERE port_id=$1`, ovsPortID).Scan(&ovnLayerStatus); err != nil || ovnLayerStatus != "INTENT_COMMITTED" {
		t.Fatalf("OVN intent projection=%s err=%v", ovnLayerStatus, err)
	}
	ovnObservation := OVNPortObservation{
		NBObservationID: "ovn-nb-observation-" + suffix, SBObservationID: "ovn-sb-observation-" + suffix,
		IntentID: ovnIntent.IntentID, IntentGeneration: 1, PortID: ovsPortID, PortGeneration: 1, BindingGeneration: 1,
		NBObservationGeneration: 2, SBObservationGeneration: 2,
		NBObservationDigest:   digestBytes([]byte("ovn-nb-observation-" + suffix)),
		SBObservationDigest:   digestBytes([]byte("ovn-sb-observation-" + suffix)),
		AdapterArtifactDigest: digestBytes([]byte("ovn-adapter")), ChassisIdentityDigest: digestBytes([]byte("ovn-chassis")),
		ApplyResponseState: "LOST",
		Observation: ovnadapter.Observation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true,
			LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, PortBindingPresent: true,
			DatapathPresent: true, ExpectedChassisMatches: true},
	}
	firstRuntimeClaims, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-worker-a", Limit: 1, Lease: time.Minute, MaximumLifetime: 2 * time.Minute})
	if err != nil || len(firstRuntimeClaims) != 1 || firstRuntimeClaims[0].ClaimMode != "APPLY_ALLOWED" || firstRuntimeClaims[0].ClaimGeneration != 1 {
		t.Fatalf("first OVN runtime claim=%#v err=%v", firstRuntimeClaims, err)
	}
	if competing, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-worker-competing", Limit: 1, Lease: time.Minute}); err != nil || len(competing) != 0 {
		t.Fatalf("competing OVN runtime claim=%#v err=%v", competing, err)
	}
	firstClaim := OVNRuntimeClaim{WorkID: firstRuntimeClaims[0].WorkID, Owner: "ovn-worker-a", ClaimGeneration: 1}
	renewal, err := RenewOVNRuntimeClaim(ctx, pool, firstClaim, 90*time.Second)
	if err != nil || renewal.RenewalGeneration != 1 || !renewal.RenewedExpiresAt.After(renewal.PriorExpiresAt) || renewal.RenewedExpiresAt.After(renewal.MaximumAt) {
		t.Fatalf("OVN runtime renewal=%#v err=%v", renewal, err)
	}
	if _, err := RenewOVNRuntimeClaim(ctx, pool, OVNRuntimeClaim{WorkID: firstClaim.WorkID, Owner: "foreign-worker", ClaimGeneration: 1}, 90*time.Second); !errors.Is(err, ErrStaleOVNRuntimeClaim) {
		t.Fatalf("foreign OVN runtime renewal error=%v", err)
	}
	maximumRenewal, err := RenewOVNRuntimeClaim(ctx, pool, firstClaim, 2*time.Minute)
	if err != nil || maximumRenewal.RenewalGeneration != 2 || !maximumRenewal.RenewedExpiresAt.Equal(maximumRenewal.MaximumAt) {
		t.Fatalf("maximum OVN runtime renewal=%#v err=%v", maximumRenewal, err)
	}
	if _, err := RenewOVNRuntimeClaim(ctx, pool, firstClaim, 2*time.Minute); !errors.Is(err, ErrOVNRuntimeClaimMaximumLifetime) {
		t.Fatalf("OVN runtime maximum lifetime error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.ovn_runtime_work_current SET claim_expires_at=statement_timestamp()-interval '1 second' WHERE work_id=$1`, firstRuntimeClaims[0].WorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := RenewOVNRuntimeClaim(ctx, pool, firstClaim, 90*time.Second); !errors.Is(err, ErrStaleOVNRuntimeClaim) {
		t.Fatalf("expired OVN runtime renewal error=%v", err)
	}
	secondRuntimeClaims, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-worker-b", Limit: 1, Lease: time.Minute})
	if err != nil || len(secondRuntimeClaims) != 1 || secondRuntimeClaims[0].ClaimMode != "READ_BACK_FIRST" || secondRuntimeClaims[0].ClaimGeneration != 2 {
		t.Fatalf("reclaimed OVN runtime work=%#v err=%v", secondRuntimeClaims, err)
	}
	staleRuntimeClaim := OVNRuntimeClaim{WorkID: firstRuntimeClaims[0].WorkID, Owner: "ovn-worker-a", ClaimGeneration: 1}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, staleRuntimeClaim); !errors.Is(err, ErrStaleOVNRuntimeClaim) {
		t.Fatalf("stale OVN worker apply authorization error=%v", err)
	}
	currentRuntimeClaim := OVNRuntimeClaim{WorkID: secondRuntimeClaims[0].WorkID, Owner: "ovn-worker-b", ClaimGeneration: 2}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, currentRuntimeClaim); !errors.Is(err, ErrStaleOVNRuntimeClaim) {
		t.Fatalf("read-back-first claim authorized without read-back evidence: %v", err)
	}
	if err := RecordOVNRuntimeReadBackStarted(ctx, pool, currentRuntimeClaim); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, currentRuntimeClaim); err != nil {
		t.Fatal(err)
	}
	if err := CompleteOVNRuntimeWork(ctx, pool, currentRuntimeClaim, ovnObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptOVNPortObservation(ctx, pool, ovnObservation); err != nil {
		t.Fatalf("idempotent OVN observation: %v", err)
	}
	var runtimeWorkState string
	var runtimeAttempts, runtimeRenewals, runtimeUnknownEvents int
	if err := pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, currentRuntimeClaim.WorkID).Scan(&runtimeWorkState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, currentRuntimeClaim.WorkID).Scan(&runtimeAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1 AND event_type='DISPATCH_UNKNOWN'`, currentRuntimeClaim.WorkID).Scan(&runtimeUnknownEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_renewal_evidence WHERE work_id=$1`, currentRuntimeClaim.WorkID).Scan(&runtimeRenewals); err != nil {
		t.Fatal(err)
	}
	if runtimeWorkState != "OBSERVED" || runtimeAttempts != 2 || runtimeRenewals != 2 || runtimeUnknownEvents != 1 {
		t.Fatalf("OVN runtime convergence state=%s attempts=%d renewals=%d unknown=%d", runtimeWorkState, runtimeAttempts, runtimeRenewals, runtimeUnknownEvents)
	}
	var nbState, sbState string
	if err := pool.QueryRow(ctx, `SELECT nb_state,sb_state,layer_status FROM kim.network_ovn_state_current WHERE port_id=$1`, ovsPortID).Scan(&nbState, &sbState, &ovnLayerStatus); err != nil || nbState != "MATCHED" || sbState != "MATCHED" || ovnLayerStatus != "SB_REALIZED" {
		t.Fatalf("OVN layer projection=%s/%s/%s err=%v", nbState, sbState, ovnLayerStatus, err)
	}
	controlPlaneObservation := OVNControlPlaneObservation{
		LogicalFlowObservationID:  "ovn-flow-observation-" + suffix,
		ChassisEncapObservationID: "ovn-chassis-observation-" + suffix,
		IntentID:                  ovnIntent.IntentID, IntentGeneration: 1, PortID: ovsPortID, PortGeneration: 1,
		BindingGeneration: 1, SBObservationID: ovnObservation.SBObservationID, SBObservationGeneration: 2,
		LogicalFlowObservationGeneration: 1, ChassisObservationGeneration: 1,
		LogicalFlowObservationDigest:   digestBytes([]byte("ovn-flow-observation-" + suffix)),
		ChassisObservationDigest:       digestBytes([]byte("ovn-chassis-observation-" + suffix)),
		ExpectedDatapathIdentityDigest: digestBytes([]byte("ovn-datapath-" + suffix)),
		ObservedDatapathIdentityDigest: digestBytes([]byte("ovn-datapath-" + suffix)),
		LogicalFlowSetDigest:           digestBytes([]byte("ovn-flow-set-" + suffix)),
		ExpectedChassisIdentityDigest:  digestBytes([]byte("ovn-chassis")),
		ObservedChassisIdentityDigest:  digestBytes([]byte("ovn-chassis")),
		TunnelEndpointDigest:           digestBytes([]byte("ovn-tunnel-endpoint")),
		EncapOptionsDigest:             digestBytes([]byte("ovn-encap-options")),
		EvaluatorArtifactDigest:        digestBytes([]byte("ovn-control-plane-evaluator")),
		IngressFlowCount:               12, EgressFlowCount: 9, LogicalDatapathPresent: true,
		RequiredPortIdentityFlowsPresent: true,
		ChassisRegistered:                true, EncapPresent: true, TunnelEndpointObserved: true, EncapType: "GENEVE",
	}
	if err := AcceptOVNControlPlaneObservation(ctx, pool, controlPlaneObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptOVNControlPlaneObservation(ctx, pool, controlPlaneObservation); err != nil {
		t.Fatalf("idempotent OVN control-plane observation: %v", err)
	}
	var controlPlaneStatus, logicalFlowState, chassisEncapState string
	if err := pool.QueryRow(ctx, `SELECT control_plane_status,logical_flow_state,chassis_encap_state
		FROM kim.network_ovn_control_plane_state_current WHERE port_id=$1`, ovsPortID).
		Scan(&controlPlaneStatus, &logicalFlowState, &chassisEncapState); err != nil ||
		controlPlaneStatus != "CONTROL_PLANE_CONVERGED" || logicalFlowState != "MATCHED" || chassisEncapState != "MATCHED" {
		t.Fatalf("OVN control-plane projection=%s/%s/%s err=%v", controlPlaneStatus, logicalFlowState, chassisEncapState, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.ovn_logical_flow_observation_evidence SET ingress_flow_count=0 WHERE observation_id=$1`, controlPlaneObservation.LogicalFlowObservationID); err == nil {
		t.Fatal("immutable OVN logical-flow evidence accepted UPDATE")
	}
	controlPlaneConflict := controlPlaneObservation
	controlPlaneConflict.LogicalFlowObservationDigest = digestBytes([]byte("different-ovn-flow-observation"))
	if err := AcceptOVNControlPlaneObservation(ctx, pool, controlPlaneConflict); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("same OVN control-plane evidence/different digest error=%v", err)
	}
	staleControlPlane := controlPlaneObservation
	staleControlPlane.LogicalFlowObservationID = "stale-ovn-flow-" + suffix
	staleControlPlane.ChassisEncapObservationID = "stale-ovn-chassis-" + suffix
	staleControlPlane.LogicalFlowObservationGeneration = 2
	staleControlPlane.ChassisObservationGeneration = 2
	staleControlPlane.PortGeneration = 2
	if err := AcceptOVNControlPlaneObservation(ctx, pool, staleControlPlane); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("stale OVN control-plane generation error=%v", err)
	}
	destinationHostID, destinationPortID := "tunnel-host-"+suffix, "tunnel-port-"+suffix
	destinationIntentID := "tunnel-intent-" + suffix
	destinationNBID, destinationSBID := "tunnel-nb-"+suffix, "tunnel-sb-"+suffix
	destinationFlowID, destinationChassisID := "tunnel-flow-"+suffix, "tunnel-chassis-"+suffix
	tunnelSeed := &pgx.Batch{}
	tunnelSeed.Queue(`INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, destinationHostID)
	tunnelSeed.Queue(`INSERT INTO kim.host_network_mappings_current(host_id,segment_claim_id,mapping_generation,mapping_state,maximum_mtu,supported_binding_types,ovn_chassis_name) VALUES($1,$2,1,'CURRENT',1500,ARRAY['OVS'],$3)`, destinationHostID, segmentClaimID, "ovn-chassis-destination-"+suffix)
	tunnelSeed.Queue(`INSERT INTO kim.network_ports_current(port_id,placement_admission_id,project_id,workload_id,network_id,subnet_id,port_generation,desired_state) VALUES($1,$2,'project',$3,$4,$5,1,'ACTIVE')`, destinationPortID, winner.admission.AdmissionID, "tunnel-workload-"+suffix, networkID, subnetID)
	tunnelSeed.Queue(`INSERT INTO kim.port_bindings_current(port_id,placement_admission_id,host_id,segment_claim_id,binding_generation,binding_type,binding_state) VALUES($1,$2,$3,$4,1,'OVS','ACTIVE')`, destinationPortID, winner.admission.AdmissionID, destinationHostID, segmentClaimID)
	tunnelSeed.Queue(`INSERT INTO kim.network_intent_revision_evidence(intent_id,intent_generation,aggregate_type,aggregate_id,project_id,network_id,network_generation,port_generation,segment_claim_id,segment_generation,host_mapping_generation,binding_generation,schema_version,canonical_object_set,object_set_digest,intent_state) VALUES($1,1,'PORT',$2,'project',$3,1,1,$4,1,1,1,$5,'{}',$6,'COMMITTED')`, destinationIntentID, destinationPortID, networkID, segmentClaimID, ovnadapter.PortIntentSchema, digestBytes([]byte(destinationIntentID)))
	tunnelSeed.Queue(`INSERT INTO kim.ovn_nb_observation_evidence(observation_id,intent_id,intent_generation,observation_generation,observation_digest,apply_response_state,ownership_marker_matches,object_set_digest_matches,logical_switch_present,logical_switch_port_present,nb_state,adapter_artifact_digest) VALUES($1,$2,1,1,$3,'RECEIVED',true,true,true,true,'MATCHED',$4)`, destinationNBID, destinationIntentID, digestBytes([]byte(destinationNBID)), digestBytes([]byte("ovn-adapter")))
	tunnelSeed.Queue(`INSERT INTO kim.ovn_sb_observation_evidence(observation_id,intent_id,intent_generation,nb_observation_id,observation_generation,observation_digest,port_binding_present,datapath_present,expected_chassis_matches,chassis_identity_digest,sb_state,adapter_artifact_digest) VALUES($1,$2,1,$3,1,$4,true,true,true,$5,'MATCHED',$6)`, destinationSBID, destinationIntentID, destinationNBID, digestBytes([]byte(destinationSBID)), digestBytes([]byte(destinationHostID)), digestBytes([]byte("ovn-adapter")))
	tunnelSeed.Queue(`INSERT INTO kim.network_ovn_state_current(port_id,port_generation,binding_generation,intent_id,intent_generation,nb_observation_id,nb_observation_generation,nb_state,sb_observation_id,sb_observation_generation,sb_state,layer_status) VALUES($1,1,1,$2,1,$3,1,'MATCHED',$4,1,'MATCHED','SB_REALIZED')`, destinationPortID, destinationIntentID, destinationNBID, destinationSBID)
	tunnelSeed.Queue(`INSERT INTO kim.ovn_logical_flow_observation_evidence(observation_id,intent_id,intent_generation,sb_observation_id,observation_generation,observation_digest,expected_datapath_identity_digest,observed_datapath_identity_digest,logical_flow_set_digest,ingress_flow_count,egress_flow_count,required_pipeline_coverage,required_port_identity_coverage,logical_flow_state,evaluator_artifact_digest) VALUES($1,$2,1,$3,1,$4,$5,$5,$6,1,1,true,true,'MATCHED',$7)`, destinationFlowID, destinationIntentID, destinationSBID, digestBytes([]byte(destinationFlowID)), digestBytes([]byte("destination-datapath")), digestBytes([]byte("destination-flows")), digestBytes([]byte("ovn-control-plane-evaluator")))
	tunnelSeed.Queue(`INSERT INTO kim.ovn_chassis_encap_observation_evidence(observation_id,intent_id,intent_generation,sb_observation_id,observation_generation,observation_digest,expected_chassis_identity_digest,observed_chassis_identity_digest,encap_type,tunnel_endpoint_digest,encap_options_digest,chassis_registered,encap_present,tunnel_endpoint_observed,chassis_encap_state,evaluator_artifact_digest) VALUES($1,$2,1,$3,1,$4,$5,$5,'GENEVE',$6,$7,true,true,true,'MATCHED',$8)`, destinationChassisID, destinationIntentID, destinationSBID, digestBytes([]byte(destinationChassisID)), digestBytes([]byte(destinationHostID)), digestBytes([]byte("destination-endpoint")), digestBytes([]byte("destination-options")), digestBytes([]byte("ovn-control-plane-evaluator")))
	tunnelSeed.Queue(`INSERT INTO kim.network_ovn_control_plane_state_current(port_id,port_generation,binding_generation,intent_id,intent_generation,sb_observation_id,sb_observation_generation,logical_flow_observation_id,logical_flow_observation_generation,logical_flow_state,chassis_encap_observation_id,chassis_encap_observation_generation,chassis_encap_state,control_plane_status) VALUES($1,1,1,$2,1,$3,1,$4,1,'MATCHED',$5,1,'MATCHED','CONTROL_PLANE_CONVERGED')`, destinationPortID, destinationIntentID, destinationSBID, destinationFlowID, destinationChassisID)
	tunnelResults := pool.SendBatch(ctx, tunnelSeed)
	for range 11 {
		if _, err := tunnelResults.Exec(); err != nil {
			_ = tunnelResults.Close()
			t.Fatal(err)
		}
	}
	if err := tunnelResults.Close(); err != nil {
		t.Fatal(err)
	}
	tunnelObservation := OVNGeneveTunnelObservation{
		ObservationID: "geneve-tunnel-" + suffix, SourcePortID: ovsPortID, DestinationPortID: destinationPortID,
		SegmentClaimID: segmentClaimID, SourceChassisObservationID: controlPlaneObservation.ChassisEncapObservationID,
		DestinationChassisObservationID: destinationChassisID, SourceMappingGeneration: 1, DestinationMappingGeneration: 1,
		ObservationGeneration: 1, ObservationDigest: digestBytes([]byte("geneve-tunnel-" + suffix)),
		SourceTunnelInterfaceDigest: digestBytes([]byte("source-geneve")), DestinationTunnelInterfaceDigest: digestBytes([]byte("destination-geneve")),
		ProbeProtocol: "ICMP", PacketsSent: 3, PacketsReceived: 3, SourceTunnelPresent: true, DestinationTunnelPresent: true,
		VerifierArtifactDigest: digestBytes([]byte("geneve-verifier")),
	}
	if err := AcceptOVNGeneveTunnelObservation(ctx, pool, tunnelObservation); err != nil {
		t.Fatal(err)
	}
	if err := AcceptOVNGeneveTunnelObservation(ctx, pool, tunnelObservation); err != nil {
		t.Fatalf("idempotent Geneve tunnel observation replay: %v", err)
	}
	var tunnelState string
	if err := pool.QueryRow(ctx, `SELECT tunnel_state FROM kim.network_ovn_tunnel_state_current WHERE source_port_id=$1 AND destination_port_id=$2 AND segment_claim_id=$3`, ovsPortID, destinationPortID, segmentClaimID).Scan(&tunnelState); err != nil || tunnelState != "VERIFIED" {
		t.Fatalf("Geneve tunnel state=%s err=%v", tunnelState, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.ovn_geneve_tunnel_observation_evidence SET packets_received=0 WHERE observation_id=$1`, tunnelObservation.ObservationID); err == nil {
		t.Fatal("immutable Geneve tunnel evidence accepted UPDATE")
	}
	tunnelConflict := tunnelObservation
	tunnelConflict.ObservationDigest = digestBytes([]byte("different-geneve-tunnel"))
	if err := AcceptOVNGeneveTunnelObservation(ctx, pool, tunnelConflict); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("same Geneve evidence/different digest error=%v", err)
	}
	staleTunnel := tunnelObservation
	staleTunnel.ObservationID = "stale-geneve-tunnel-" + suffix
	staleTunnel.ObservationGeneration = 2
	staleTunnel.ObservationDigest = digestBytes([]byte(staleTunnel.ObservationID))
	staleTunnel.DestinationMappingGeneration = 2
	if err := AcceptOVNGeneveTunnelObservation(ctx, pool, staleTunnel); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("stale Geneve mapping generation error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.ovn_nb_observation_evidence SET nb_state='UNKNOWN' WHERE observation_id=$1`, ovnObservation.NBObservationID); err == nil {
		t.Fatal("immutable OVN NB evidence accepted UPDATE")
	}
	staleOVN := ovnObservation
	staleOVN.NBObservationID, staleOVN.SBObservationID = "stale-nb-"+suffix, "stale-sb-"+suffix
	staleOVN.NBObservationGeneration, staleOVN.SBObservationGeneration = 2, 2
	staleOVN.PortGeneration = 2
	if err := AcceptOVNPortObservation(ctx, pool, staleOVN); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("stale OVN generation error=%v", err)
	}
	retirementRequest := OVNPortBindingRetirementRequest{
		OperationID: "ovn-retirement-" + suffix, OperationGeneration: 1,
		IntentID: "ovn-retirement-intent-" + suffix, IntentGeneration: 2,
		PortID: ovsPortID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: hostID,
	}
	staleRetirement := retirementRequest
	staleRetirement.BindingGeneration = 2
	if _, err := CommitOVNPortBindingRetirement(ctx, pool, staleRetirement); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("stale OVN Port retirement generation error=%v", err)
	}
	retirement, err := CommitOVNPortBindingRetirement(ctx, pool, retirementRequest)
	if err != nil || retirement.PortID != ovsPortID || len(retirement.ObjectSetDigest) != 64 {
		t.Fatalf("OVN Port retirement=%#v err=%v", retirement, err)
	}
	if replay, err := CommitOVNPortBindingRetirement(ctx, pool, retirementRequest); err != nil || replay.WorkID != retirement.WorkID || replay.ObjectSetDigest != retirement.ObjectSetDigest {
		t.Fatalf("OVN Port retirement replay=%#v/%v want %#v", replay, err, retirement)
	}
	retirementClaims, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-retirement-worker-a", Limit: 1, Lease: time.Minute})
	if err != nil || len(retirementClaims) != 1 || retirementClaims[0].OperationKind != "UNBIND" || retirementClaims[0].ClaimMode != "APPLY_ALLOWED" {
		t.Fatalf("OVN Port retirement first claim=%#v err=%v", retirementClaims, err)
	}
	retirementClaim1 := OVNRuntimeClaim{WorkID: retirement.WorkID, Owner: "ovn-retirement-worker-a", ClaimGeneration: 1}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, retirementClaim1); err != nil {
		t.Fatalf("authorize OVN Port retirement apply: %v", err)
	}
	verifiedRetirementObservation := ovnadapter.PortBindingRetirementObservation{
		OwnershipMarkerMatches: true, ObjectSetDigestMatches: true,
		LogicalSwitchPresent: true, LogicalSwitchPortPresent: true,
		RequestedChassisAbsent: true, SourceChassisInactive: true, SourceOVSInterfaceAbsent: true,
	}
	retirementObservation1 := OVNPortBindingRetirementObservation{
		EvidenceID: "ovn-retirement-evidence-unknown-" + suffix,
		IntentID:   retirement.IntentID, IntentGeneration: retirement.IntentGeneration,
		PortID: ovsPortID, PortGeneration: 1, BindingGeneration: 1,
		SourceHostID: hostID, OperationGeneration: 1, ApplyResponseState: "LOST",
		NBObservationGeneration: 1, NBObservationDigest: digestBytes([]byte("retirement-nb-unknown-" + suffix)),
		SBObservationGeneration: 1, SBObservationDigest: digestBytes([]byte("retirement-sb-unknown-" + suffix)),
		OVSObservationGeneration: 1, OVSObservationDigest: digestBytes([]byte("retirement-ovs-unknown-" + suffix)),
		AdapterArtifactDigest: digestBytes([]byte("retirement-adapter")), Observation: verifiedRetirementObservation,
	}
	if err := CompleteOVNPortBindingRetirement(ctx, pool, retirementClaim1, retirementObservation1); err != nil {
		t.Fatalf("record response-loss retirement: %v", err)
	}
	retirementClaims, err = ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-retirement-worker-b", Limit: 1, Lease: time.Minute})
	if err != nil || len(retirementClaims) != 1 || retirementClaims[0].OperationKind != "UNBIND" || retirementClaims[0].ClaimMode != "READ_BACK_FIRST" || retirementClaims[0].ClaimGeneration != 2 {
		t.Fatalf("OVN Port retirement recovery claim=%#v err=%v", retirementClaims, err)
	}
	retirementClaim2 := OVNRuntimeClaim{WorkID: retirement.WorkID, Owner: "ovn-retirement-worker-b", ClaimGeneration: 2}
	if err := RecordOVNRuntimeReadBackStarted(ctx, pool, retirementClaim2); err != nil {
		t.Fatalf("record retirement read-back first: %v", err)
	}
	retirementObservation2 := retirementObservation1
	retirementObservation2.EvidenceID = "ovn-retirement-evidence-verified-" + suffix
	retirementObservation2.ApplyResponseState = "UNKNOWN"
	retirementObservation2.NBObservationGeneration = 2
	retirementObservation2.SBObservationGeneration = 2
	retirementObservation2.OVSObservationGeneration = 2
	retirementObservation2.NBObservationDigest = digestBytes([]byte("retirement-nb-verified-" + suffix))
	retirementObservation2.SBObservationDigest = digestBytes([]byte("retirement-sb-verified-" + suffix))
	retirementObservation2.OVSObservationDigest = digestBytes([]byte("retirement-ovs-verified-" + suffix))
	if err := CompleteOVNPortBindingRetirement(ctx, pool, retirementClaim2, retirementObservation2); err != nil {
		t.Fatalf("complete read-back-first retirement: %v", err)
	}
	var retirementState, retirementWorkState string
	var retirementAttemptCount, retirementEvidenceCount, portCount, identityCount int
	if err := pool.QueryRow(ctx, `SELECT r.retirement_state,w.work_state FROM kim.network_port_binding_retirements_current r JOIN kim.ovn_runtime_work_current w ON w.intent_id=r.intent_id AND w.intent_generation=r.intent_generation WHERE r.port_id=$1`, ovsPortID).Scan(&retirementState, &retirementWorkState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, retirement.WorkID).Scan(&retirementAttemptCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_port_binding_retirement_evidence WHERE port_id=$1`, ovsPortID).Scan(&retirementEvidenceCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_ports_current WHERE port_id=$1`, ovsPortID).Scan(&portCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_identity_claims WHERE port_id=$1 AND claim_state IN ('RESERVED','ACTIVE')`, ovsPortID).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if retirementState != "VERIFIED" || retirementWorkState != "OBSERVED" || retirementAttemptCount != 2 || retirementEvidenceCount != 2 || portCount != 1 || identityCount != 2 {
		t.Fatalf("OVN Port retirement convergence=%s/%s attempts=%d evidence=%d port=%d identities=%d", retirementState, retirementWorkState, retirementAttemptCount, retirementEvidenceCount, portCount, identityCount)
	}

	// Qualify a second binding incarnation through the ordinary immutable
	// PortBindingHandoff transaction. The fixture supplies already-accepted
	// source quiescence and destination Admission evidence; it never updates
	// Port/Binding generations directly.
	var retirementEvidenceID string
	if err := pool.QueryRow(ctx, `SELECT terminal_evidence_id FROM kim.network_port_binding_retirements_current
		WHERE port_id=$1 AND port_generation=1 AND binding_generation=1`, ovsPortID).Scan(&retirementEvidenceID); err != nil {
		t.Fatal(err)
	}
	quiescenceID := "repeated-retirement-quiescence-" + suffix
	quiescenceDigest := digestBytes([]byte(quiescenceID))
	if _, err := pool.Exec(ctx, `INSERT INTO kim.network_port_source_quiescence_evidence(
		evidence_id,port_id,port_generation,source_host_id,source_binding_generation,
		vm_id,vm_generation,command_id,verification_id,observation_generation,
		observation_digest,source_vm_not_running,source_interface_absent,quiescence_state,
		evidence_digest,retirement_evidence_id)
		VALUES($1,$2,1,$3,1,$4,1,$5,$6,1,$7,true,true,'QUIESCED',$8,$9)`,
		quiescenceID, ovsPortID, hostID, vmMaterialization.VMID, prebootCommandID,
		prebootVerificationID, digestBytes([]byte("repeated-retirement-quiescence-observation-"+suffix)),
		quiescenceDigest, retirementEvidenceID); err != nil {
		t.Fatal(err)
	}
	destinationAdmissionID := "repeated-retirement-destination-admission-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.placement_admission_decisions
		SELECT (jsonb_populate_record(NULL::kim.placement_admission_decisions,
			to_jsonb(source_decision) || jsonb_build_object(
				'admission_id',$2::text,'request_id',$3::text,'host_id',$4::text))).*
		FROM kim.placement_admission_decisions source_decision WHERE source_decision.admission_id=$1`,
		winner.admission.AdmissionID, destinationAdmissionID,
		"repeated-retirement-destination-request-"+suffix, destinationHostID); err != nil {
		t.Fatal(err)
	}
	handoffID := "repeated-retirement-handoff-" + suffix
	handoffRequirement := placement.NetworkRequirement{
		PortID: ovsPortID, HandoffID: handoffID, SourceHostID: hostID,
		SourcePortGeneration: 1, SourceBindingGeneration: 1,
		DestinationPortGeneration: 2, DestinationBindingGeneration: 2,
		SourceQuiescenceEvidenceID: quiescenceID, SourceQuiescenceEvidenceDigest: quiescenceDigest,
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return claimNetworkPortHandoffTx(ctx, tx, destinationAdmissionID,
			PlacementAdmissionRequest{WorkloadID: vmMaterialization.VMID},
			placement.Evaluation{HostID: destinationHostID}, handoffRequirement)
	}); err != nil {
		t.Fatalf("ordinary PortBindingHandoff 1/1 -> 2/2: %v", err)
	}
	var currentPortGeneration, currentBindingGeneration uint64
	var currentBindingHost string
	if err := pool.QueryRow(ctx, `SELECT p.port_generation,b.binding_generation,b.host_id
		FROM kim.network_ports_current p JOIN kim.port_bindings_current b USING(port_id)
		WHERE p.port_id=$1`, ovsPortID).Scan(&currentPortGeneration, &currentBindingGeneration, &currentBindingHost); err != nil || currentPortGeneration != 2 || currentBindingGeneration != 2 || currentBindingHost != destinationHostID {
		t.Fatalf("handoff current incarnation=%d/%d host=%s err=%v", currentPortGeneration, currentBindingGeneration, currentBindingHost, err)
	}

	destinationIntent, err := CommitOVNPortIntent(ctx, pool, OVNPortIntentRequest{IntentID: "ovn-destination-incarnation-" + suffix, IntentGeneration: 3, PortID: ovsPortID})
	if err != nil || destinationIntent.PortGeneration != 2 || destinationIntent.BindingGeneration != 2 {
		t.Fatalf("destination ordinary OVN intent=%#v err=%v", destinationIntent, err)
	}
	destinationClaims, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-destination-worker", Limit: 1, Lease: time.Minute})
	if err != nil || len(destinationClaims) != 1 || destinationClaims[0].WorkID != "ovn-runtime:"+destinationIntent.IntentID+":3" {
		t.Fatalf("destination ordinary OVN claim=%#v err=%v", destinationClaims, err)
	}
	destinationClaim := OVNRuntimeClaim{WorkID: destinationClaims[0].WorkID, Owner: "ovn-destination-worker", ClaimGeneration: destinationClaims[0].ClaimGeneration}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, destinationClaim); err != nil {
		t.Fatal(err)
	}
	destinationObservation := OVNPortObservation{
		NBObservationID: "ovn-destination-nb-" + suffix, SBObservationID: "ovn-destination-sb-" + suffix,
		IntentID: destinationIntent.IntentID, IntentGeneration: 3, PortID: ovsPortID,
		PortGeneration: 2, BindingGeneration: 2, NBObservationGeneration: 1, SBObservationGeneration: 1,
		NBObservationDigest:   digestBytes([]byte("ovn-destination-nb-" + suffix)),
		SBObservationDigest:   digestBytes([]byte("ovn-destination-sb-" + suffix)),
		AdapterArtifactDigest: digestBytes([]byte("ovn-adapter")),
		ChassisIdentityDigest: digestBytes([]byte("ovn-chassis-destination-" + suffix)),
		ApplyResponseState:    "RECEIVED",
		Observation: ovnadapter.Observation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true,
			LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, PortBindingPresent: true,
			DatapathPresent: true, ExpectedChassisMatches: true},
	}
	if err := CompleteOVNRuntimeWork(ctx, pool, destinationClaim, destinationObservation); err != nil {
		t.Fatalf("complete destination ordinary OVN work: %v", err)
	}

	secondRetirementRequest := OVNPortBindingRetirementRequest{
		OperationID: "ovn-retirement-second-" + suffix, OperationGeneration: 2,
		IntentID: "ovn-retirement-second-intent-" + suffix, IntentGeneration: 4,
		PortID: ovsPortID, PortGeneration: 2, BindingGeneration: 2, SourceHostID: destinationHostID,
	}
	secondRetirement, err := CommitOVNPortBindingRetirement(ctx, pool, secondRetirementRequest)
	if err != nil {
		t.Fatalf("commit second binding incarnation retirement: %v", err)
	}
	secondClaims, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-retirement-worker-second", Limit: 1, Lease: time.Minute})
	if err != nil || len(secondClaims) != 1 || secondClaims[0].WorkID != secondRetirement.WorkID || secondClaims[0].ClaimMode != "APPLY_ALLOWED" {
		t.Fatalf("second incarnation retirement claim=%#v err=%v", secondClaims, err)
	}
	secondClaim := OVNRuntimeClaim{WorkID: secondRetirement.WorkID, Owner: "ovn-retirement-worker-second", ClaimGeneration: secondClaims[0].ClaimGeneration}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, secondClaim); err != nil {
		t.Fatal(err)
	}
	secondRetirementObservation := OVNPortBindingRetirementObservation{
		EvidenceID: "ovn-retirement-second-evidence-" + suffix,
		IntentID:   secondRetirement.IntentID, IntentGeneration: secondRetirement.IntentGeneration,
		PortID: ovsPortID, PortGeneration: 2, BindingGeneration: 2,
		SourceHostID: destinationHostID, OperationGeneration: 2, ApplyResponseState: "RECEIVED",
		NBObservationGeneration: 1, NBObservationDigest: digestBytes([]byte("retirement-second-nb-" + suffix)),
		SBObservationGeneration: 1, SBObservationDigest: digestBytes([]byte("retirement-second-sb-" + suffix)),
		OVSObservationGeneration: 1, OVSObservationDigest: digestBytes([]byte("retirement-second-ovs-" + suffix)),
		AdapterArtifactDigest: digestBytes([]byte("retirement-adapter")), Observation: verifiedRetirementObservation,
	}
	if err := CompleteOVNPortBindingRetirement(ctx, pool, secondClaim, secondRetirementObservation); err != nil {
		t.Fatalf("complete second binding incarnation retirement: %v", err)
	}
	if replay, err := CommitOVNPortBindingRetirement(ctx, pool, retirementRequest); err != nil || replay.WorkID != retirement.WorkID {
		t.Fatalf("historical first-incarnation response-loss replay=%#v err=%v", replay, err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		stale := handoffRequirement
		stale.HandoffID = "repeated-retirement-stale-handoff-" + suffix
		stale.SourceHostID = destinationHostID
		stale.SourcePortGeneration, stale.SourceBindingGeneration = 2, 2
		stale.DestinationPortGeneration, stale.DestinationBindingGeneration = 3, 3
		return claimNetworkPortHandoffTx(ctx, tx, winner.admission.AdmissionID,
			PlacementAdmissionRequest{WorkloadID: vmMaterialization.VMID},
			placement.Evaluation{HostID: hostID}, stale)
	}); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("old generation-1 quiescence authorized generation-2 handoff: %v", err)
	}
	var exactRetirements, firstEvidenceCount int
	var firstState, secondState, latestState string
	var latestPortGeneration, latestBindingGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_port_binding_retirements_current WHERE port_id=$1`, ovsPortID).Scan(&exactRetirements); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT retirement_state FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND port_generation=1 AND binding_generation=1`, ovsPortID).Scan(&firstState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT retirement_state FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND port_generation=2 AND binding_generation=2`, ovsPortID).Scan(&secondState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT port_generation,binding_generation,retirement_state FROM kim.network_port_binding_retirement_latest_current WHERE port_id=$1`, ovsPortID).Scan(&latestPortGeneration, &latestBindingGeneration, &latestState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_port_binding_retirement_evidence WHERE operation_id=$1`, retirement.OperationID).Scan(&firstEvidenceCount); err != nil {
		t.Fatal(err)
	}
	if exactRetirements != 2 || firstState != "VERIFIED" || secondState != "VERIFIED" || latestPortGeneration != 2 || latestBindingGeneration != 2 || latestState != "VERIFIED" || firstEvidenceCount != 2 {
		t.Fatalf("repeated retirement projections count=%d first=%s second=%s latest=%d/%d/%s firstEvidence=%d", exactRetirements, firstState, secondState, latestPortGeneration, latestBindingGeneration, latestState, firstEvidenceCount)
	}
	abaRollback := errors.New("rollback repeated-retirement ABA qualification")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := CommitOVNPortIntent(ctx, nestedTestTxBeginner{tx}, OVNPortIntentRequest{IntentID: "ovn-source-revival-second-" + suffix, IntentGeneration: 5, PortID: ovsPortID}); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT retirement_state FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND port_generation=1 AND binding_generation=1`, ovsPortID).Scan(&firstState); err != nil || firstState != "VERIFIED" {
			return fmt.Errorf("second-incarnation ABA corrupted first history: state=%s err=%v", firstState, err)
		}
		if err := tx.QueryRow(ctx, `SELECT retirement_state FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND port_generation=2 AND binding_generation=2`, ovsPortID).Scan(&secondState); err != nil || secondState != "STALE" {
			return fmt.Errorf("second-incarnation ABA did not stale exact proof: state=%s err=%v", secondState, err)
		}
		if err := tx.QueryRow(ctx, `SELECT retirement_state FROM kim.network_port_binding_retirement_latest_current WHERE port_id=$1`, ovsPortID).Scan(&latestState); err != nil || latestState != "STALE" {
			return fmt.Errorf("second-incarnation ABA did not stale latest projection: state=%s err=%v", latestState, err)
		}
		return abaRollback
	})
	if !errors.Is(err, abaRollback) {
		t.Fatalf("second-incarnation ABA rollback qualification: %v", err)
	}

	var secondRetirementEvidenceID string
	if err := pool.QueryRow(ctx, `SELECT terminal_evidence_id FROM kim.network_port_binding_retirements_current
		WHERE port_id=$1 AND port_generation=2 AND binding_generation=2`, ovsPortID).Scan(&secondRetirementEvidenceID); err != nil {
		t.Fatal(err)
	}
	secondQuiescenceID := "repeated-retirement-quiescence-second-" + suffix
	secondQuiescenceDigest := digestBytes([]byte(secondQuiescenceID))
	if _, err := pool.Exec(ctx, `INSERT INTO kim.network_port_source_quiescence_evidence(
		evidence_id,port_id,port_generation,source_host_id,source_binding_generation,
		vm_id,vm_generation,command_id,verification_id,observation_generation,
		observation_digest,source_vm_not_running,source_interface_absent,quiescence_state,
		evidence_digest,retirement_evidence_id)
		VALUES($1,$2,2,$3,2,$4,1,$5,$6,2,$7,true,true,'QUIESCED',$8,$9)`,
		secondQuiescenceID, ovsPortID, destinationHostID, vmMaterialization.VMID,
		dataplaneRequest.CommandID, dataplaneVerificationID,
		digestBytes([]byte("repeated-retirement-quiescence-second-observation-"+suffix)),
		secondQuiescenceDigest, secondRetirementEvidenceID); err != nil {
		t.Fatal(err)
	}
	thirdAdmissionID := "repeated-retirement-third-admission-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.placement_admission_decisions
		SELECT (jsonb_populate_record(NULL::kim.placement_admission_decisions,
			to_jsonb(source_decision) || jsonb_build_object(
				'admission_id',$2::text,'request_id',$3::text,'host_id',$4::text))).*
		FROM kim.placement_admission_decisions source_decision WHERE source_decision.admission_id=$1`,
		destinationAdmissionID, thirdAdmissionID,
		"repeated-retirement-third-request-"+suffix, hostID); err != nil {
		t.Fatal(err)
	}
	secondHandoffRequirement := placement.NetworkRequirement{
		PortID: ovsPortID, HandoffID: "repeated-retirement-handoff-second-" + suffix,
		SourceHostID: destinationHostID, SourcePortGeneration: 2, SourceBindingGeneration: 2,
		DestinationPortGeneration: 3, DestinationBindingGeneration: 3,
		SourceQuiescenceEvidenceID:     secondQuiescenceID,
		SourceQuiescenceEvidenceDigest: secondQuiescenceDigest,
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return claimNetworkPortHandoffTx(ctx, tx, thirdAdmissionID,
			PlacementAdmissionRequest{WorkloadID: vmMaterialization.VMID},
			placement.Evaluation{HostID: hostID}, secondHandoffRequirement)
	}); err != nil {
		t.Fatalf("ordinary PortBindingHandoff 2/2 -> 3/3: %v", err)
	}
	var handoffEvidenceCount int
	if err := pool.QueryRow(ctx, `SELECT p.port_generation,b.binding_generation,b.host_id
		FROM kim.network_ports_current p JOIN kim.port_bindings_current b USING(port_id)
		WHERE p.port_id=$1`, ovsPortID).Scan(&currentPortGeneration, &currentBindingGeneration, &currentBindingHost); err != nil || currentPortGeneration != 3 || currentBindingGeneration != 3 || currentBindingHost != hostID {
		t.Fatalf("second handoff current incarnation=%d/%d host=%s err=%v", currentPortGeneration, currentBindingGeneration, currentBindingHost, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.port_binding_handoff_evidence WHERE port_id=$1`, ovsPortID).Scan(&handoffEvidenceCount); err != nil || handoffEvidenceCount != 2 {
		t.Fatalf("immutable repeated Handoff evidence count=%d err=%v", handoffEvidenceCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT destination_binding_generation FROM kim.port_binding_handoffs_current WHERE port_id=$1`, ovsPortID).Scan(&currentBindingGeneration); err != nil || currentBindingGeneration != 3 {
		t.Fatalf("latest Handoff projection generation=%d err=%v", currentBindingGeneration, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.network_identity_claims WHERE port_id=$1 AND claim_state IN ('RESERVED','ACTIVE')`, ovsPortID).Scan(&identityCount); err != nil || identityCount != 2 {
		t.Fatalf("repeated Handoff changed Port MAC/IP identity count=%d err=%v", identityCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_power_observation_evidence SET observed_power_state='SHUTOFF' WHERE verification_id=$1`, powerVerificationID); err == nil {
		t.Fatal("immutable VM power observation evidence accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.vm_network_port_realization_evidence SET preboot_state='UNKNOWN' WHERE evidence_id=$1`, sriovObservation.EvidenceID); err == nil {
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

	// Generic VF retirement is an exact claim/Port incarnation authority. A
	// lost mutation response remains DISPATCH_UNKNOWN; the successor attempt is
	// READ_BACK_FIRST and only an exact typed command verification can release
	// the source allocation into RELEASE_PENDING.
	var vfPortID, vfDevice string
	var vfAllocationGeneration, vfPortGeneration, vfBindingGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT port_id,device_address,allocation_generation,port_generation,binding_generation FROM kim.pci_vf_allocation_claims WHERE claim_id=$1`, vfClaimID).Scan(&vfPortID, &vfDevice, &vfAllocationGeneration, &vfPortGeneration, &vfBindingGeneration); err != nil {
		t.Fatal(err)
	}
	vfRetirementRequest := PCIVFRetirementRequest{OperationID: "vf-retirement-" + suffix, OperationGeneration: 1, ClaimID: vfClaimID, AllocationGeneration: vfAllocationGeneration, SourceHostID: hostID, DeviceAddress: vfDevice, PortID: vfPortID, PortGeneration: vfPortGeneration, BindingGeneration: vfBindingGeneration, WorkloadID: winner.request.WorkloadID, VMID: vmMaterialization.VMID, VMGeneration: 1, OwnershipMarker: digestBytes([]byte("vf-owner-" + suffix))}
	if _, err := CommitPCIVFRetirement(ctx, pool, vfRetirementRequest); err != nil {
		t.Fatal(err)
	}
	seedRetirementVerification := func(claim PCIVFRetirementClaim, commandID, verificationID, state, observationDigest string, attempt int) {
		jobID := "job:" + commandID
		if _, err := AuthorizePCIVFRetirementCommand(ctx, pool, claim, jobID, commandID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,$2,$3,1,1,$4,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute')`, commandID, attempt, hostID, digestBytes([]byte(commandID))); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,$2,1,1,1)`, commandID, attempt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($1,$2,$3,$4,$5,$6,$7,'{}')`, verificationID, commandID, attempt, int64(attempt), observationDigest, state, digestBytes([]byte("vf-retirement-verifier"))); err != nil {
			t.Fatal(err)
		}
	}
	claim1, err := ClaimPCIVFRetirement(ctx, pool, vfClaimID, vfAllocationGeneration, "vf-worker-a", time.Minute)
	if err != nil || claim1.ClaimMode != "APPLY_ALLOWED" {
		t.Fatalf("VF retirement claim1=%#v err=%v", claim1, err)
	}
	unknownDigest := digestBytes([]byte("vf-retirement-unknown-" + suffix))
	seedRetirementVerification(claim1, "vf-retirement-command-1-"+suffix, "vf-retirement-verification-1-"+suffix, "UNKNOWN", unknownDigest, 1)
	unknown := PCIVFRetirementObservation{EvidenceID: "vf-retirement-evidence-unknown-" + suffix, PCIVFRetirementRequest: vfRetirementRequest, ClaimGeneration: claim1.ClaimGeneration, OwnershipMarkerMatches: true, SourceDomainNotRunning: true, SourceHostdevAbsent: false, VFDriverReleased: false, VFHolderAbsent: false, IOMMUGroupMatches: true, PCIObservationGeneration: 1, LibvirtObservationGeneration: 1, PCIObservationDigest: digestBytes([]byte("vf-pci-unknown")), LibvirtObservationDigest: unknownDigest, CommandID: "vf-retirement-command-1-" + suffix, AttemptIndex: 1, VerificationID: "vf-retirement-verification-1-" + suffix, VerifierDigest: digestBytes([]byte("vf-retirement-verifier")), ApplyResponseState: "LOST", ResultState: "UNKNOWN", EvidenceDigest: digestBytes([]byte("vf-retirement-evidence-unknown"))}
	if err := CompletePCIVFRetirement(ctx, pool, claim1, unknown); err != nil {
		t.Fatal(err)
	}
	claim2, err := ClaimPCIVFRetirement(ctx, pool, vfClaimID, vfAllocationGeneration, "vf-worker-b", time.Minute)
	if err != nil || claim2.ClaimMode != "READ_BACK_FIRST" || claim2.ClaimGeneration != 2 {
		t.Fatalf("VF retirement claim2=%#v err=%v", claim2, err)
	}
	matchedDigest := digestBytes([]byte("vf-retirement-matched-" + suffix))
	seedRetirementVerification(claim2, "vf-retirement-command-2-"+suffix, "vf-retirement-verification-2-"+suffix, "MATCHED", matchedDigest, 2)
	verified := unknown
	verified.EvidenceID = "vf-retirement-evidence-verified-" + suffix
	verified.ClaimGeneration = claim2.ClaimGeneration
	verified.SourceHostdevAbsent = true
	verified.VFDriverReleased = true
	verified.VFHolderAbsent = true
	verified.LibvirtObservationGeneration = 2
	verified.LibvirtObservationDigest = matchedDigest
	verified.CommandID = "vf-retirement-command-2-" + suffix
	verified.AttemptIndex = 2
	verified.VerificationID = "vf-retirement-verification-2-" + suffix
	verified.ApplyResponseState = "UNKNOWN"
	verified.ResultState = "VERIFIED"
	verified.EvidenceDigest = digestBytes([]byte("vf-retirement-evidence-verified"))
	if err := CompletePCIVFRetirement(ctx, pool, claim2, verified); err != nil {
		t.Fatal(err)
	}
	var vfRetirementState, vfClaimState string
	var vfAttempts int
	if err := pool.QueryRow(ctx, `SELECT r.operation_state,c.claim_state,(SELECT count(*) FROM kim.pci_vf_retirement_attempt_evidence a WHERE a.claim_id=r.claim_id AND a.allocation_generation=r.allocation_generation) FROM kim.pci_vf_retirement_operations_current r JOIN kim.pci_vf_allocation_claims c ON c.claim_id=r.claim_id WHERE r.claim_id=$1`, vfClaimID).Scan(&vfRetirementState, &vfClaimState, &vfAttempts); err != nil || vfRetirementState != "VERIFIED" || vfClaimState != "RELEASE_PENDING" || vfAttempts != 2 {
		t.Fatalf("VF retirement=%s claim=%s attempts=%d err=%v", vfRetirementState, vfClaimState, vfAttempts, err)
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

func seedLocalLVMVerification(ctx context.Context, pool TxBeginner, fixture localLVMVerificationFixture) error {
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
		ValidatedOperations: []string{"VF_DISCOVER", "VF_ASSIGN", "VF_DETACH", "VF_READ_BACK"}, EvidenceState: "QUALIFIED",
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
