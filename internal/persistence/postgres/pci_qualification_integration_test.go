package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestPCIQualificationAndFinalAdmissionPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('pci-qualification-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	hostID := fmt.Sprintf("host-pci-%d", time.Now().UnixNano())
	qualificationID := hostID + "-qualification"
	certificateFingerprint := digestBytes([]byte("pci-certificate"))
	prepareSessionIdentityFixture(t, ctx, pool, hostID, 1, certificateFingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: hostID + "-attempt", HostID: hostID, ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: certificateFingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}

	snapshot := pciInventoryFixture(hostID, 1, "ixgbevf")
	envelope, err := agentinventory.NewEnvelope(snapshot, 1, hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, pool, envelope, 1<<20); err != nil {
		t.Fatal(err)
	}
	var observationDigest string
	if err := pool.QueryRow(ctx, `SELECT observation_digest FROM kim.host_pci_device_projections WHERE host_id=$1 AND device_address='0000:03:00.1'`, hostID).Scan(&observationDigest); err != nil {
		t.Fatal(err)
	}
	request := PCIVFClaimRequest{ClaimID: hostID + "-claim-before-qualification", HostID: hostID, DeviceAddress: "0000:03:00.1", ProjectID: "project", WorkloadID: "vm", PolicyID: "policy", PolicyGeneration: 1, HostCapabilityGeneration: 1, QualificationID: qualificationID, QualificationRevision: 1}
	decision, err := EvaluatePCIAllocationState(ctx, pool, hostID, "0000:03:00.1", "policy")
	if err != nil || decision.State != "BLOCKED" {
		t.Fatalf("observed-only allocation state/error = %#v/%v", decision, err)
	}
	if err := ClaimQualifiedVF(ctx, pool, request); !errors.Is(err, ErrPCIQualificationBlocked) {
		t.Fatalf("observed VF without qualification was not blocked: %v", err)
	}

	fingerprint := map[string]string{"device": "8086:10ed", "driver": "ixgbevf/6", "firmware": "A", "kernel": "K1", "iommu": "strict", "libvirt_qemu": "L1/Q1"}
	state, err := RefreshPCIQualificationBinding(ctx, pool, PCIQualificationBindingRequest{HostID: hostID, DeviceAddress: "0000:03:00.1", QualificationID: "missing", Revision: 1, CurrentGeneration: 1, CurrentObservationDigest: observationDigest, CurrentBindingFingerprint: fingerprint})
	if err != nil || state != "UNKNOWN" {
		t.Fatalf("missing qualification binding state/error = %s/%v", state, err)
	}
	evidence := PCIQualificationEvidence{QualificationID: qualificationID, Revision: 1, HostID: hostID, DeviceAddress: "0000:03:00.1", ProfileRevision: "sriov-profile/v1", TestArtifactDigest: digestBytes([]byte("test-artifact")), EvaluatorDigest: digestBytes([]byte("evaluator")), ObservedGeneration: 1, ObservationDigest: observationDigest, BindingFingerprint: fingerprint, ValidatedOperations: []string{"VF_DISCOVER", "VF_ASSIGN", "VF_DETACH", "VF_READ_BACK"}, EvidenceState: "QUALIFIED"}
	if err := RecordPCIQualificationEvidence(ctx, pool, evidence); err != nil {
		t.Fatal(err)
	}
	state, err = RefreshPCIQualificationBinding(ctx, pool, PCIQualificationBindingRequest{HostID: hostID, DeviceAddress: "0000:03:00.1", QualificationID: qualificationID, Revision: 1, CurrentGeneration: 1, CurrentObservationDigest: observationDigest, CurrentBindingFingerprint: fingerprint})
	if err != nil || state != "CURRENT" {
		t.Fatalf("binding state/error = %s/%v", state, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.pci_allocation_policy_bindings (host_id, policy_id, policy_generation, policy_state, qualification_profile_revision) VALUES ($1,'policy',1,'ALLOWED','sriov-profile/v1')`, hostID); err != nil {
		t.Fatal(err)
	}
	decision, err = EvaluatePCIAllocationState(ctx, pool, hostID, "0000:03:00.1", "policy")
	if err != nil || decision.State != "AVAILABLE" {
		t.Fatalf("qualified allocation state/error = %#v/%v", decision, err)
	}
	numa := 1
	request.RequiredNUMANodeID = &numa
	request.RequiredIOMMUGroup = "13"
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, claimID := range []string{hostID + "-claim-1", hostID + "-claim-2"} {
		claimRequest := request
		claimRequest.ClaimID = claimID
		go func() {
			<-start
			results <- ClaimQualifiedVF(ctx, pool, claimRequest)
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrPCIAllocationConflict):
			conflicts++
		default:
			t.Fatalf("concurrent qualified VF claim: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent claims success/conflict = %d/%d", successes, conflicts)
	}
	decision, err = EvaluatePCIAllocationState(ctx, pool, hostID, "0000:03:00.1", "policy")
	if err != nil || decision.State != "CLAIMED" {
		t.Fatalf("claimed allocation state/error = %#v/%v", decision, err)
	}
	request.ClaimID = hostID + "-claim-3"
	if err := ClaimQualifiedVF(ctx, pool, request); !errors.Is(err, ErrPCIAllocationConflict) {
		t.Fatalf("duplicate VF claim was not fenced: %v", err)
	}

	changed := pciInventoryFixture(hostID, 2, "vfio-pci")
	changedEnvelope, err := agentinventory.NewEnvelope(changed, 1, hostID+"-inventory-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, pool, changedEnvelope, 1<<20); err != nil {
		t.Fatal(err)
	}
	var changedDigest string
	if err := pool.QueryRow(ctx, `SELECT observation_digest FROM kim.host_pci_device_projections WHERE host_id=$1 AND device_address='0000:03:00.1'`, hostID).Scan(&changedDigest); err != nil {
		t.Fatal(err)
	}
	state, err = RefreshPCIQualificationBinding(ctx, pool, PCIQualificationBindingRequest{HostID: hostID, DeviceAddress: "0000:03:00.1", QualificationID: qualificationID, Revision: 1, CurrentGeneration: 2, CurrentObservationDigest: changedDigest, CurrentBindingFingerprint: map[string]string{"device": "8086:10ed", "driver": "vfio-pci/1", "firmware": "A", "kernel": "K1", "iommu": "strict", "libvirt_qemu": "L1/Q1"}})
	if err != nil || state != "STALE" {
		t.Fatalf("changed observation binding state/error = %s/%v", state, err)
	}
	request.ClaimID = hostID + "-claim-after-change"
	request.HostCapabilityGeneration = 2
	if err := ClaimQualifiedVF(ctx, pool, request); !errors.Is(err, ErrPCIQualificationBlocked) {
		t.Fatalf("stale qualification did not block allocation: %v", err)
	}
	revokedFingerprint := map[string]string{"device": "8086:10ed", "driver": "vfio-pci/1", "firmware": "A", "kernel": "K1", "iommu": "strict", "libvirt_qemu": "L1/Q1"}
	revoked := PCIQualificationEvidence{QualificationID: qualificationID, Revision: 2, HostID: hostID, DeviceAddress: "0000:03:00.1", ProfileRevision: "sriov-profile/v1", TestArtifactDigest: digestBytes([]byte("revocation")), EvaluatorDigest: digestBytes([]byte("evaluator")), ObservedGeneration: 2, ObservationDigest: changedDigest, BindingFingerprint: revokedFingerprint, EvidenceState: "REVOKED"}
	if err := RecordPCIQualificationEvidence(ctx, pool, revoked); err != nil {
		t.Fatal(err)
	}
	state, err = RefreshPCIQualificationBinding(ctx, pool, PCIQualificationBindingRequest{HostID: hostID, DeviceAddress: "0000:03:00.1", QualificationID: qualificationID, Revision: 2, CurrentGeneration: 2, CurrentObservationDigest: changedDigest, CurrentBindingFingerprint: revokedFingerprint})
	if err != nil || state != "REVOKED" {
		t.Fatalf("revoked qualification binding state/error = %s/%v", state, err)
	}
}

func pciInventoryFixture(hostID string, generation uint64, vfDriver string) agentinventory.Snapshot {
	vfIndex := uint32(0)
	return agentinventory.Snapshot{SchemaVersion: agentinventory.SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: generation, CollectionStatus: "COMPLETE", Fragments: []agentinventory.Fragment{{
		Domain: agentinventory.DomainPCI,
		Source: agentinventory.Source{ModuleName: "linux-pci", ModuleVersion: "v1", SchemaVersion: "kim.inventory.linux-pci/v1", ArtifactDigest: digestBytes([]byte("pci-module"))},
		Capabilities: []agentinventory.Capability{
			{Name: "kim.host.iommu-observation.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.pci-numa-locality.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.pci-observation.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.sriov-observation.v1", Version: "v1", State: agentinventory.AvailabilityAvailable},
		},
		PCI: &agentinventory.PCI{IOMMUEnabled: true, Devices: []agentinventory.PCIDevice{
			{Address: "0000:03:00.0", VendorID: "8086", DeviceID: "10fb", Driver: "ixgbe", NUMANodeID: 1, IOMMUGroup: "12", SRIOVTotalVFs: 2, SRIOVEnabledVFs: 1, RelationshipState: agentinventory.AvailabilityAvailable},
			{Address: "0000:03:00.1", VendorID: "8086", DeviceID: "10ed", Driver: vfDriver, NUMANodeID: 1, IOMMUGroup: "13", PFAddress: "0000:03:00.0", VFIndex: &vfIndex, RelationshipState: agentinventory.AvailabilityAvailable},
		}},
	}}}
}
