package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

const realRecoverySourceHost = "kvm-base-g01-n001-p.core.s01.si1230.com"

// This opt-in physical test qualifies the previously missing boundary: a
// Command created in PostgreSQL is executed with that same Control-Plane Lease
// by the real remote helper, then accepted through the ordinary Result,
// Verification, Receipt, and Job transaction. The full Failure Epoch terminal
// campaign consumes this primitive; this test does not claim terminal Recovery.
func TestRealRecoveryAuthorityLeaseBoundHelperPostgreSQLIntegration(t *testing.T) {
	if os.Getenv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E") != "1" {
		t.Skip("explicit real Recovery authority E2E opt-in is not set")
	}
	databaseURL, runner := os.Getenv("KIM_POSTGRES_TEST_URL"), os.Getenv("KIM_REAL_KVM_RECOVERY_RUNNER")
	vgUUID := os.Getenv("KIM_REAL_KVM_RECOVERY_SOURCE_VG_UUID")
	if databaseURL == "" || runner == "" || vgUUID == "" {
		t.Fatal("database, runner, and exact source VG UUID are required")
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES('real-recovery-authority-e2e',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	fingerprint := digestBytes([]byte("real-recovery-authority058-certificate"))
	prepareSessionIdentityFixture(t, ctx, pool, realRecoverySourceHost, 1, fingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: "real-recovery-authority058-session",
		HostID: realRecoverySourceHost, ConnectionInstanceID: "real-recovery-authority058", TransportProfile: "integration",
		ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("real-helper-29b48d9f")),
		CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, realRecoverySourceHost)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: realRecoverySourceHost, CapabilityGeneration: 1,
		BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: realRecoverySourceHost,
		PolicyID: "real-recovery-authority058", PolicyGeneration: 1, ActorID: "qualification",
		ReasonCode: "explicit_real_recovery_authority_e2e"}); err != nil {
		t.Fatal(err)
	}
	const jobID = "real-recovery-authority058-lvm-job"
	const commandID = "real-recovery-authority058-lvm-command"
	const volumeID = "real-recovery-authority058-volume"
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: jobID, CommandID: commandID,
		HostID: realRecoverySourceHost, ResourceType: "VOLUME", ResourceID: volumeID, DesiredRevision: 1,
		CommandType: locallvm.CommandType, SchemaVersion: locallvm.SchemaVersion,
		TargetResourceID: "volume:" + volumeID,
		Payload:          map[string]any{"vg_uuid": vgUUID, "size_mib": 16, "desired_state": "PRESENT"}}); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"command_id": commandID, "message_id": commandID + "/result-message",
		"verification_id": commandID + "/verification", "host": realRecoverySourceHost,
		"helper_path": "/var/tmp/kim-real-recovery-authority058/kim-real-kvm-recovery-helper",
		"vg_name":     "kimrr_authority058_g01", "vg_uuid": vgUUID,
		"cache_root": "/var/tmp/kim-real-recovery-authority058/cache",
		"state_root": "/var/tmp/kim-real-recovery-authority058/state"}
	encoded, _ := json.Marshal(request)
	command := exec.CommandContext(ctx, runner)
	command.Stdin = bytes.NewReader(encoded)
	command.Env = append(os.Environ(), "KIM_POSTGRES_TEST_URL="+databaseURL, "KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("Lease-bound runner: %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(strings.ToLower(stdout.String()), "lease_token") {
		t.Fatalf("runner output exposed capability field: %s", stdout.String())
	}
	var state, resultOutcome, verificationState, receiptDisposition string
	var grants, attempts, results, receipts, verifications int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT job_state FROM kim.execution_jobs WHERE job_id=$1),
		(SELECT execution_outcome FROM kim.command_results WHERE command_id=$2),
		(SELECT verification_state FROM kim.command_verification_evidence WHERE command_id=$2),
		(SELECT disposition FROM kim.agent_message_receipts WHERE message_id=$3),
		(SELECT count(*) FROM kim.command_lease_grants WHERE command_id=$2),
		(SELECT count(*) FROM kim.command_attempts WHERE command_id=$2),
		(SELECT count(*) FROM kim.command_results WHERE command_id=$2),
		(SELECT count(*) FROM kim.agent_message_receipts WHERE message_id=$3),
		(SELECT count(*) FROM kim.command_verification_evidence WHERE command_id=$2)`, jobID, commandID, commandID+"/result-message").Scan(
		&state, &resultOutcome, &verificationState, &receiptDisposition, &grants, &attempts, &results, &receipts, &verifications); err != nil {
		t.Fatal(err)
	}
	if state != "SUCCEEDED" || resultOutcome != "SUCCEEDED" || verificationState != "MATCHED" || receiptDisposition != "ACCEPTED" ||
		grants != 1 || attempts != 1 || results != 1 || receipts != 1 || verifications != 1 {
		t.Fatalf("ordinary authority chain state=%s/%s/%s/%s counts=%d/%d/%d/%d/%d",
			state, resultOutcome, verificationState, receiptDisposition, grants, attempts, results, receipts, verifications)
	}
}
