package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/db/migrations"
)

func TestMigratePostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	available, err := migrations.List()
	if err != nil {
		t.Fatal(err)
	}
	first, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if first.CurrentVersion != available[len(available)-1].Version {
		t.Fatalf("first CurrentVersion = %d", first.CurrentVersion)
	}
	second, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("replay Migrate: %v", err)
	}
	if second.Applied != 0 || second.CurrentVersion != first.CurrentVersion {
		t.Fatalf("replay result = %#v, first = %#v", second, first)
	}

	var migrationCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM kim.schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(available) {
		t.Fatalf("migration ledger count = %d, want %d", migrationCount, len(available))
	}

	requiredTables := []string{
		"project_revision_evidence",
		"projects_current",
		"northbound_role_bindings_current",
		"northbound_idempotency_evidence",
		"northbound_audit_evidence",
		"network_resource_revision_evidence",
		"network_segment_allocation_decision_evidence",
		"network_segment_allocations_current",
		"network_segment_release_evidence",
		"network_realization_operation_evidence",
		"network_realization_attempt_evidence",
		"network_realization_attempt_event_evidence",
		"network_realization_observation_evidence",
		"network_realization_terminal_evidence",
		"network_realizations_current",
		"northbound_flavor_idempotency_evidence",
		"northbound_availability_policy_idempotency_evidence",
		"agent_message_receipts",
		"agent_resync_checkpoints",
		"host_inventory_snapshots",
		"host_capability_projections",
		"host_group_membership_set_evidence",
		"host_group_membership_set_member_evidence",
		"host_group_membership_sets_current",
		"host_group_selector_revision_evidence",
		"host_group_selectors_current",
		"host_group_selector_evaluation_evidence",
		"host_group_selector_evaluations_current",
		"agent_transport_session_attempts",
		"agent_transport_session_events",
		"agent_transport_sessions_current",
		"database_authority",
		"host_identities",
		"inbox_messages",
		"outbox_delivery_attempts",
		"outbox_delivery_events",
		"outbox_messages",
		"volume_backend_binding_evidence",
		"volume_backend_bindings_current",
		"volume_attachment_observation_evidence",
		"volume_attachment_observations_current",
		"virtual_machines_current",
		"vm_materialization_plan_evidence",
		"vm_definition_observation_evidence",
		"vm_image_realization_evidence",
		"vm_network_port_realization_evidence",
		"vm_network_port_realizations_current",
		"vm_power_observation_evidence",
		"vm_power_state_current",
		"recovery_materialization_evidence",
		"recovery_power_authority_evidence",
		"recovery_verification_evidence",
		"recovery_terminal_decision_evidence",
		"vm_port_dataplane_observation_evidence",
		"vm_port_dataplane_state_current",
		"network_intent_revision_evidence",
		"ovn_nb_observation_evidence",
		"ovn_sb_observation_evidence",
		"network_ovn_state_current",
		"ovn_logical_flow_observation_evidence",
		"ovn_chassis_encap_observation_evidence",
		"network_ovn_control_plane_state_current",
		"ovn_geneve_tunnel_observation_evidence",
		"network_ovn_tunnel_state_current",
		"network_identity_release_observation_evidence",
		"network_port_source_quiescence_evidence",
		"port_binding_handoff_evidence",
		"port_binding_handoffs_current",
		"network_port_binding_retirement_evidence",
		"network_port_binding_retirements_current",
		"network_port_binding_retirement_latest_current",
		"pci_vf_retirement_operations_current",
		"pci_vf_retirement_attempt_evidence",
		"pci_vf_retirement_evidence",
		"pci_vf_retirement_latest_current",
		"pci_vf_handoff_evidence",
		"pci_vf_handoffs_current",
		"backend_cleanup_operation_evidence",
		"backend_cleanup_origin_eligibility_evidence",
		"backend_cleanup_operations_current",
		"backend_cleanup_attempt_evidence",
		"backend_cleanup_observation_evidence",
		"backend_cleanup_terminal_evidence",
		"source_backend_cleanup_current",
		"host_placement_drain_evidence",
		"host_placement_drains_current",
		"host_placement_drain_transition_evidence",
		"host_evacuation_operation_evidence",
		"host_evacuation_operations_current",
		"host_evacuation_workload_set_evidence",
		"host_evacuation_workload_evidence",
		"host_evacuation_workloads_current",
		"host_evacuation_slot_claim_evidence",
		"host_evacuation_slot_claims_current",
		"host_evacuation_slot_transition_evidence",
		"planned_source_quiescence_evidence",
		"host_evacuation_source_network_retirement_authority_evidence",
		"host_evacuation_child_network_evidence_binding",
		"host_evacuation_child_terminal_evidence",
		"host_evacuation_terminal_evidence",
		"local_lvm_relocation_copy_operation_evidence",
		"local_lvm_relocation_copy_operations_current",
		"local_lvm_relocation_copy_attempt_evidence",
		"local_lvm_relocation_content_observation_evidence",
		"local_lvm_relocation_copy_verification_evidence",
		"local_lvm_relocation_copy_terminal_evidence",
		"local_lvm_relocation_transport_session_evidence",
		"local_lvm_relocation_transport_sessions_current",
		"local_lvm_relocation_transport_event_evidence",
		"local_lvm_relocation_transport_peer_observation_evidence",
		"local_lvm_relocation_transport_terminal_evidence",
		"local_lvm_source_cleanup_authority_evidence",
		"local_lvm_source_cleanup_observation_identity_evidence",
		"local_lvm_capacity_reclamation_evidence",
		"ovn_runtime_work_current",
		"ovn_runtime_work_attempt_evidence",
		"ovn_runtime_work_event_evidence",
		"vm_materialization_readiness_current",
	}
	for _, table := range requiredTables {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'kim' AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("required table kim.%s does not exist", table)
		}
	}
	// Keep the integration contract repeatable against the same disposable DB.
	if _, err := pool.Exec(ctx, `
		DELETE FROM kim.outbox_delivery_events WHERE message_id = 'integration-message';
		DELETE FROM kim.outbox_delivery_attempts WHERE message_id = 'integration-message';
		DELETE FROM kim.outbox_messages WHERE message_id = 'integration-message';
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.outbox_messages (
			message_id, aggregate_type, aggregate_id, event_type,
			schema_version, payload_digest, payload
		) VALUES ('integration-message', 'Host', 'host-1', 'HostObserved',
			'v1', $1, '{"host_id":"host-1"}')
		ON CONFLICT (message_id) DO NOTHING
	`, digest64("a")); err != nil {
		t.Fatal(err)
	}

	firstClaimTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := ClaimOutboxTx(ctx, firstClaimTx, OutboxClaimRequest{
		Owner:      "publisher-a",
		Limit:      1,
		Lease:      time.Minute,
		EventTypes: []string{"HostObserved"},
	})
	if err != nil {
		_ = firstClaimTx.Rollback(ctx)
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ClaimGeneration != 1 {
		_ = firstClaimTx.Rollback(ctx)
		t.Fatalf("first claim = %#v", claimed)
	}
	if err := firstClaimTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	unknownTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim := OutboxClaim{MessageID: claimed[0].MessageID, Owner: "publisher-a", ClaimGeneration: 1}
	if err := RecordOutboxDispatchUnknownTx(ctx, unknownTx, firstClaim, map[string]any{"reason": "response_lost"}); err != nil {
		_ = unknownTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := unknownTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE kim.outbox_messages
		SET claim_expires_at = statement_timestamp() - interval '1 second'
		WHERE message_id = 'integration-message'
	`); err != nil {
		t.Fatal(err)
	}
	secondClaimTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reclaimed, err := ClaimOutboxTx(ctx, secondClaimTx, OutboxClaimRequest{
		Owner:      "publisher-b",
		Limit:      1,
		Lease:      time.Minute,
		EventTypes: []string{"HostObserved"},
	})
	if err != nil {
		_ = secondClaimTx.Rollback(ctx)
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ClaimGeneration != 2 {
		_ = secondClaimTx.Rollback(ctx)
		t.Fatalf("reclaimed = %#v", reclaimed)
	}
	if err := secondClaimTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var unknownEvidence, attempts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM kim.outbox_delivery_events
		WHERE message_id = 'integration-message' AND event_type = 'DISPATCH_UNKNOWN'
	`).Scan(&unknownEvidence); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM kim.outbox_delivery_attempts
		WHERE message_id = 'integration-message'
	`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if unknownEvidence != 1 || attempts != 2 {
		t.Fatalf("unknown evidence = %d, attempts = %d", unknownEvidence, attempts)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.host_identities (host_id, enrollment_state)
		VALUES ('session-history-host', 'APPROVED')
		ON CONFLICT (host_id) DO NOTHING
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.agent_transport_session_attempts (
			session_attempt_id, host_id, connection_instance_id,
			transport_profile, protocol_version, agent_artifact_digest,
			credential_binding_revision, handshake_evidence, handshake_evidence_digest
		) VALUES (
			'session-attempt-1', 'session-history-host', 'connection-1',
			'typed-http2-spike', 'v1', $1, 1, '{}', $2
		)
		ON CONFLICT (session_attempt_id) DO NOTHING
	`, digest64("b"), digest64("c")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.agent_transport_session_events (
			session_attempt_id, event_sequence, event_type,
			event_payload, event_payload_digest
		) VALUES ('session-attempt-1', 1, 'OPENED', '{}', $1)
		ON CONFLICT (session_attempt_id, event_sequence) DO NOTHING
	`, digest64("d")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE kim.agent_transport_session_events
		SET event_type = 'CLOSED'
		WHERE session_attempt_id = 'session-attempt-1' AND event_sequence = 1
	`); err == nil {
		t.Fatal("immutable session evidence accepted UPDATE")
	}

	var oldTable, currentTable bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regclass('kim.agent_transport_sessions') IS NOT NULL,
		       to_regclass('kim.agent_transport_sessions_current') IS NOT NULL
	`).Scan(&oldTable, &currentTable); err != nil {
		t.Fatal(err)
	}
	if oldTable || !currentTable {
		t.Fatalf("session authority rename: old=%v current=%v", oldTable, currentTable)
	}
}
