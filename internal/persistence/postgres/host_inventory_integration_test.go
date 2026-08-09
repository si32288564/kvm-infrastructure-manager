package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestHostInventoryProjectionPostgreSQLIntegration(t *testing.T) {
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
		VALUES ('host-inventory-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode = 'ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	hostID := fmt.Sprintf("host-inventory-%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities (host_id, enrollment_state) VALUES ($1, 'APPROVED')`, hostID); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{
		SessionAttemptID: hostID + "-attempt", HostID: hostID, ConnectionInstanceID: "connection",
		TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")),
		CredentialBindingRevision: 1, ExpectedSessionGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}

	newer := inventoryFixture(t, hostID, 2, "COMPLETE", "kim.host.kvm.v1")
	newerEnvelope, err := agentinventory.NewEnvelope(newer, 1, hostID+"-inventory-2")
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := AcceptHostInventory(ctx, pool, newerEnvelope, 1<<20); err != nil || receipt.Disposition != "ACCEPTED" {
		t.Fatalf("newer Inventory receipt = %#v, error = %v", receipt, err)
	}
	older := inventoryFixture(t, hostID, 1, "DEGRADED", "kim.host.kvm.v1")
	olderEnvelope, err := agentinventory.NewEnvelope(older, 1, hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, pool, olderEnvelope, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, pool, newerEnvelope, 1<<20); err != nil {
		t.Fatalf("idempotent Inventory replay: %v", err)
	}
	var generation, snapshotCount int
	var state, sourceMessage string
	if err := pool.QueryRow(ctx, `
		SELECT observation_generation, projection_state, source_message_id
		FROM kim.host_capability_projections WHERE host_id = $1
	`, hostID).Scan(&generation, &state, &sourceMessage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_inventory_snapshots WHERE host_id = $1`, hostID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || state != "CURRENT" || sourceMessage != newerEnvelope.MessageID || snapshotCount != 2 {
		t.Fatalf("projection generation/state/source/snapshots = %d/%s/%s/%d", generation, state, sourceMessage, snapshotCount)
	}
}

func inventoryFixture(t *testing.T, hostID string, generation uint64, status, capability string) agentinventory.Snapshot {
	t.Helper()
	return agentinventory.Snapshot{
		SchemaVersion: agentinventory.SnapshotSchemaV2, HostIdentity: hostID,
		ObservationGeneration: generation, CollectionStatus: status,
		Fragments: []agentinventory.Fragment{{
			Domain:         agentinventory.DomainVirtualization,
			Source:         agentinventory.Source{ModuleName: "libvirt", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestBytes([]byte("module"))},
			Capabilities:   []agentinventory.Capability{{Name: capability, Version: "v1", State: agentinventory.AvailabilityAvailable}},
			Virtualization: &agentinventory.Virtualization{KVMAvailable: true, LibvirtVersion: "fixture", QEMUVersion: "fixture"},
		}},
	}
}
