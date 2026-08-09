package dispatcher_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/statemarker"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/gateway"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/dispatcher"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type moduleSink struct{ module *agentexecution.Module }

func (sink moduleSink) Send(ctx context.Context, envelope session.Envelope) error {
	return sink.module.Handle(ctx, envelope)
}

type resultPublisher struct {
	receiver gateway.PostgresMessageReceiver
	receipt  session.Receipt
	envelope session.Envelope
}

type droppingPublisher struct{}

func (droppingPublisher) Publish(session.Envelope) error {
	return errors.New("simulated Agent crash before Result delivery")
}
func (publisher *resultPublisher) Publish(envelope session.Envelope) error {
	receipt, err := publisher.receiver.Receive(context.Background(), envelope)
	if err == nil {
		publisher.receipt, publisher.envelope = receipt, envelope
	}
	return err
}

func TestTypedHostStateMarkerRoundTripPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode) VALUES ('roundtrip-test',1,'ACTIVE') ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	hostID := fmt.Sprintf("host-roundtrip-%d", time.Now().UnixNano())
	fingerprint := digest("certificate-" + hostID)
	if err := postgres.RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecordEnrollmentDecision(ctx, pool, postgres.EnrollmentDecision{DecisionID: hostID + "-enrollment", HostID: hostID, Revision: 1, PolicyID: "manual", PolicyGeneration: 1, HardwareEvidenceDigest: digest("hardware"), State: "APPROVED", ActorID: "operator", ReasonCode: "fixture"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := postgres.RecordAgentCredentialBinding(ctx, pool, postgres.AgentCredentialBindingEvidence{HostID: hostID, Revision: 1, CertificateFingerprint: fingerprint, PublicKeyDigest: digest("public-key"), IssuerID: "fixture-ca", ProfileRevision: "host-agent/v1", TrustGeneration: 1, EnrollmentDecisionID: hostID + "-enrollment", EnrollmentRevision: 1, EvidenceDigest: digest("credential"), State: "ACTIVE", ValidNotBefore: now.Add(-time.Hour), ValidNotAfter: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	grant, err := postgres.AdmitAgentSession(ctx, pool, postgres.AgentSessionAdmission{SessionAttemptID: hostID + "-session-1", HostID: hostID, ConnectionInstanceID: "connection-1", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digest("agent"), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := inventory.Snapshot{SchemaVersion: inventory.SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: 1, CollectionStatus: "COMPLETE", Fragments: []inventory.Fragment{{Domain: inventory.DomainVirtualization, Source: inventory.Source{ModuleName: "libvirt", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digest("inventory-module")}, Capabilities: []inventory.Capability{{Name: "kim.host.kvm.v1", Version: "v1", State: inventory.AvailabilityAvailable}}, Virtualization: &inventory.Virtualization{KVMAvailable: true, LibvirtVersion: "fixture", QEMUVersion: "fixture"}}}}
	inventoryEnvelope, err := inventory.NewEnvelope(snapshot, uint64(grant.SessionGeneration), hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.AcceptHostInventory(ctx, pool, inventoryEnvelope, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := postgres.UpdateHostReadinessGate(ctx, pool, postgres.HostReadinessGate{HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	authority, err := postgres.ArmHostOperationAuthority(ctx, pool, postgres.HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "roundtrip"})
	if err != nil {
		t.Fatal(err)
	}

	commandID := hostID + "-command"
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: hostID + "-job", CommandID: commandID, HostID: hostID, ResourceType: "HOST_AGENT_STATE", ResourceID: hostID + "-state", DesiredRevision: 1, CommandType: statemarker.CommandType, SchemaVersion: statemarker.SchemaVersion, TargetResourceID: hostID + "-marker", Payload: map[string]any{"value": "ready"}}); err != nil {
		t.Fatal(err)
	}
	journal, err := executionjournal.Open(t.TempDir(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	publisher := &resultPublisher{receiver: gateway.PostgresMessageReceiver{DB: pool, MaxMessageBytes: 1 << 20}}
	module, err := agentexecution.NewModule(hostID, journal, publisher, digest("state-marker-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	markerDirectory := t.TempDir()
	if err := module.RegisterBackend(statemarker.Backend{Directory: markerDirectory}); err != nil {
		t.Fatal(err)
	}
	registry := gateway.NewOutboundRegistry()
	release, err := registry.Register(hostID, uint64(grant.SessionGeneration), "connection-1", moduleSink{module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	dispatch := dispatcher.Dispatcher{DB: pool, Sender: registry, LeaseDuration: time.Minute, ExecutionTimeout: 10 * time.Second}
	lease, err := dispatch.Dispatch(ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.HostAuthorityGeneration != authority.AuthorityGeneration || lease.SessionGeneration != grant.SessionGeneration {
		t.Fatalf("Lease authority/session = %d/%d", lease.HostAuthorityGeneration, lease.SessionGeneration)
	}
	var commandState, jobState string
	if err := pool.QueryRow(ctx, `SELECT current.command_state, job.job_state FROM kim.execution_commands_current current JOIN kim.execution_commands command USING(command_id) JOIN kim.execution_jobs job USING(job_id) WHERE current.command_id=$1`, commandID).Scan(&commandState, &jobState); err != nil {
		t.Fatal(err)
	}
	if commandState != "SUCCEEDED" || jobState != "SUCCEEDED" || publisher.receipt.Disposition != "ACCEPTED" {
		t.Fatalf("round-trip state/receipt = %s/%s/%s", commandState, jobState, publisher.receipt.Disposition)
	}
	replayedReceipt, err := publisher.receiver.Receive(ctx, publisher.envelope)
	if err != nil || replayedReceipt.MessageID != publisher.receipt.MessageID || replayedReceipt.PayloadDigest != publisher.receipt.PayloadDigest {
		t.Fatalf("atomic Result receipt replay = %#v, error = %v", replayedReceipt, err)
	}
	entries, err := os.ReadDir(markerDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("marker entries/error = %d/%v", len(entries), err)
	}

	conflictCommand := commandID + "-atomic-conflict"
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: conflictCommand + "-job", CommandID: conflictCommand, HostID: hostID, ResourceType: "HOST_AGENT_STATE", ResourceID: conflictCommand + "-state", DesiredRevision: 1, CommandType: statemarker.CommandType, SchemaVersion: statemarker.SchemaVersion, TargetResourceID: conflictCommand + "-marker", Payload: map[string]any{"value": "ready"}}); err != nil {
		t.Fatal(err)
	}
	conflictLease, err := postgres.AcquireCommandLease(ctx, pool, postgres.CommandLeaseRequest{CommandID: conflictCommand, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	messageID := conflictCommand + "-result-message"
	acceptedEnvelope := session.NewEnvelope(hostID, 1, session.StreamResult, messageID, "conflict-fixture/v1", "command/"+conflictCommand, 1, []byte("accepted-first"))
	acceptedEnvelope.CorrelationKey = conflictCommand
	if _, err := postgres.AcceptAgentMessage(ctx, pool, acceptedEnvelope, 1<<20); err != nil {
		t.Fatal(err)
	}
	conflictingEnvelope := session.NewEnvelope(hostID, 1, session.StreamResult, messageID, "conflict-fixture/v1", "command/"+conflictCommand, 1, []byte("different-domain-message"))
	conflictingEnvelope.CorrelationKey = conflictCommand
	_, err = postgres.AcceptAgentCommandResult(ctx, pool, conflictingEnvelope, 1<<20, postgres.AgentCommandResultDecision{
		Start:  postgres.CommandAttemptStart{CommandID: conflictCommand, AttemptIndex: 1, LeaseToken: conflictLease.Token, JournalEvidenceDigest: digest("journal")},
		Result: postgres.CommandResultSubmission{CommandID: conflictCommand, AttemptIndex: 1, LeaseToken: conflictLease.Token, ResultID: conflictCommand + "-result", Outcome: "SUCCEEDED", Payload: map[string]any{"state": "APPLIED"}},
	})
	if !errors.Is(err, postgres.ErrAgentMessageEvidenceConflict) {
		t.Fatalf("receipt conflict error = %v", err)
	}
	var conflictState string
	if err := pool.QueryRow(ctx, `SELECT command_state FROM kim.execution_commands_current WHERE command_id=$1`, conflictCommand).Scan(&conflictState); err != nil {
		t.Fatal(err)
	}
	if conflictState != "LEASED" {
		t.Fatalf("partial domain decision escaped receipt conflict: %s", conflictState)
	}

	crashCommand := commandID + "-crash-resync"
	release()
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: crashCommand + "-job", CommandID: crashCommand, HostID: hostID, ResourceType: "HOST_AGENT_STATE", ResourceID: crashCommand + "-state", DesiredRevision: 1, CommandType: statemarker.CommandType, SchemaVersion: statemarker.SchemaVersion, TargetResourceID: crashCommand + "-marker", Payload: map[string]any{"value": "recovered"}}); err != nil {
		t.Fatal(err)
	}
	crashJournalDirectory := t.TempDir()
	crashJournal, err := executionjournal.Open(crashJournalDirectory, hostID)
	if err != nil {
		t.Fatal(err)
	}
	crashModule, err := agentexecution.NewModule(hostID, crashJournal, droppingPublisher{}, digest("state-marker-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := crashModule.RegisterBackend(statemarker.Backend{Directory: markerDirectory}); err != nil {
		t.Fatal(err)
	}
	crashRelease, err := registry.Register(hostID, 1, "connection-crash", moduleSink{module: crashModule})
	if err != nil {
		t.Fatal(err)
	}
	crashDispatch := dispatcher.Dispatcher{DB: pool, Sender: registry, LeaseDuration: 50 * time.Millisecond, ExecutionTimeout: 10 * time.Millisecond}
	if _, err := crashDispatch.Dispatch(ctx, crashCommand); err == nil {
		t.Fatal("simulated Result delivery loss was not surfaced")
	}
	time.Sleep(60 * time.Millisecond)
	if err := postgres.ExpireCommandLease(ctx, pool, crashCommand); err != nil {
		t.Fatal(err)
	}
	if err := crashJournal.Close(); err != nil {
		t.Fatal(err)
	}
	var unknownCommand string
	if err := pool.QueryRow(ctx, `SELECT command_state FROM kim.execution_commands_current WHERE command_id=$1`, crashCommand).Scan(&unknownCommand); err != nil {
		t.Fatal(err)
	}
	if unknownCommand != "UNKNOWN" {
		t.Fatalf("crashed Command state = %s", unknownCommand)
	}
	secondGrant, err := postgres.AdmitAgentSession(ctx, pool, postgres.AgentSessionAdmission{SessionAttemptID: hostID + "-session-2", HostID: hostID, ConnectionInstanceID: "connection-2", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digest("agent"), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 2})
	if err != nil {
		t.Fatal(err)
	}
	restartedJournal, err := executionjournal.Open(crashJournalDirectory, hostID)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedJournal.Close()
	resyncPublisher := &resultPublisher{receiver: gateway.PostgresMessageReceiver{DB: pool, MaxMessageBytes: 1 << 20}}
	restartedModule, err := agentexecution.NewModule(hostID, restartedJournal, resyncPublisher, digest("state-marker-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := restartedModule.RegisterBackend(statemarker.Backend{Directory: markerDirectory}); err != nil {
		t.Fatal(err)
	}
	resyncRelease, err := registry.Register(hostID, uint64(secondGrant.SessionGeneration), "connection-2", moduleSink{module: restartedModule})
	if err != nil {
		t.Fatal(err)
	}
	defer resyncRelease()
	crashRelease()
	resyncDispatch := dispatcher.Dispatcher{DB: pool, Sender: registry, LeaseDuration: time.Minute, ExecutionTimeout: 10 * time.Second}
	if err := resyncDispatch.DispatchVerification(ctx, crashCommand); err != nil {
		t.Fatal(err)
	}
	var recoveredCommand, recoveredJob, authorityState string
	if err := pool.QueryRow(ctx, `SELECT current.command_state,job.job_state,authority.authority_state FROM kim.execution_commands_current current JOIN kim.execution_commands command USING(command_id) JOIN kim.execution_jobs job USING(job_id) JOIN kim.host_operation_authorities_current authority ON authority.host_id=command.host_id WHERE current.command_id=$1`, crashCommand).Scan(&recoveredCommand, &recoveredJob, &authorityState); err != nil {
		t.Fatal(err)
	}
	if recoveredCommand != "SUCCEEDED" || recoveredJob != "SUCCEEDED" {
		t.Fatalf("crash resync state = %s/%s", recoveredCommand, recoveredJob)
	}
	if authorityState != "FENCED" {
		t.Fatalf("read-only resync implicitly rearmed Host authority: %s", authorityState)
	}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
