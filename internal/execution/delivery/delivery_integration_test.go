package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/gateway"
	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/security/tokenprotect"
)

type recordingBus struct {
	messageID string
	payload   []byte
	err       error
}

func (bus *recordingBus) Publish(_ context.Context, subject, messageID string, payload []byte) (PublishAcknowledgement, error) {
	if subject != Subject {
		return PublishAcknowledgement{}, errors.New("unexpected subject")
	}
	bus.messageID, bus.payload = messageID, append([]byte(nil), payload...)
	if bus.err != nil {
		return PublishAcknowledgement{}, bus.err
	}
	return PublishAcknowledgement{Stream: "KIM_AGENT_COMMAND", Sequence: 9}, nil
}

type recordingSink struct {
	mu        sync.Mutex
	envelopes []session.Envelope
}

func (sink *recordingSink) Send(_ context.Context, envelope session.Envelope) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.envelopes = append(sink.envelopes, envelope)
	return nil
}

func TestDurableOutboxBusInboxGatewayPostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority (restore_epoch,authority_generation,mode) VALUES ('delivery-test',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	hostID := fmt.Sprintf("host-delivery-%d", time.Now().UnixNano())
	fingerprint := testDigest([]byte(hostID + "-certificate"))
	if err := postgres.RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecordEnrollmentDecision(ctx, pool, postgres.EnrollmentDecision{DecisionID: hostID + "-enrollment-1", HostID: hostID, Revision: 1, PolicyID: "manual", PolicyGeneration: 1, HardwareEvidenceDigest: testDigest([]byte("hardware")), State: "APPROVED", ActorID: "test", ReasonCode: "fixture"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := postgres.RecordAgentCredentialBinding(ctx, pool, postgres.AgentCredentialBindingEvidence{HostID: hostID, Revision: 1, CertificateFingerprint: fingerprint, PublicKeyDigest: testDigest([]byte("key")), IssuerID: "fixture-ca", ProfileRevision: "host-agent/v1", TrustGeneration: 1, EnrollmentDecisionID: hostID + "-enrollment-1", EnrollmentRevision: 1, EvidenceDigest: testDigest([]byte("credential")), State: "ACTIVE", ValidNotBefore: now.Add(-time.Hour), ValidNotAfter: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.AdmitAgentSession(ctx, pool, postgres.AgentSessionAdmission{SessionAttemptID: hostID + "-attempt-1", HostID: hostID, ConnectionInstanceID: "connection-1", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: testDigest([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	snapshot := agentinventory.Snapshot{SchemaVersion: agentinventory.SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: 1, CollectionStatus: "COMPLETE", Fragments: []agentinventory.Fragment{{Domain: agentinventory.DomainVirtualization, Source: agentinventory.Source{ModuleName: "libvirt", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: testDigest([]byte("module"))}, Capabilities: []agentinventory.Capability{{Name: "kim.host.kvm.v1", Version: "v1", State: agentinventory.AvailabilityAvailable}}, Virtualization: &agentinventory.Virtualization{KVMAvailable: true, LibvirtVersion: "fixture", QEMUVersion: "fixture"}}}}
	inventoryEnvelope, err := agentinventory.NewEnvelope(snapshot, 1, hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.AcceptHostInventory(ctx, pool, inventoryEnvelope, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := postgres.UpdateHostReadinessGate(ctx, pool, postgres.HostReadinessGate{HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	authority, err := postgres.ArmHostOperationAuthority(ctx, pool, postgres.HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "test", ReasonCode: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	commandID := hostID + "-command"
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: commandID + "-job", CommandID: commandID, HostID: hostID, ResourceType: "VM", ResourceID: commandID + "-vm", DesiredRevision: 1, CommandType: "VM_ENSURE_STATE", SchemaVersion: "kim.command.vm-ensure-state/v1", TargetResourceID: commandID + "-vm", Payload: map[string]any{"state": "RUNNING"}}); err != nil {
		t.Fatal(err)
	}
	protector := tokenprotect.AESGCM{KeyID: "delivery-key-1", Key: bytes.Repeat([]byte{7}, 32)}
	if _, err := postgres.AcquireCommandLease(ctx, pool, postgres.CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Minute, ExecutionTimeout: 30 * time.Second, DeliveryProtector: protector}); err != nil {
		t.Fatal(err)
	}
	bus := &recordingBus{}
	publisher := OutboxPublisher{DB: pool, Protector: protector, Bus: bus, Owner: "worker-1", BatchLimit: 8, ClaimLease: time.Minute, MaxMessageBytes: 1 << 20}
	if count, err := publisher.PublishOnce(ctx); err != nil || count != 1 {
		t.Fatalf("PublishOnce = %d, %v", count, err)
	}

	registry := gateway.NewOutboundRegistry()
	sink := &recordingSink{}
	release, err := registry.Register(hostID, 1, "connection-1", sink)
	if err != nil {
		t.Fatal(err)
	}
	handler := GatewayHandler{DB: pool, Registry: registry, Consumer: "gateway-command-v1", MaxMessageBytes: 1 << 20}
	if disposition, err := handler.Handle(ctx, bus.messageID, bus.payload); err != nil || disposition != ConsumeAck {
		t.Fatalf("first Gateway delivery = %s, %v", disposition, err)
	}
	if disposition, err := handler.Handle(ctx, bus.messageID, bus.payload); err != nil || disposition != ConsumeAck {
		t.Fatalf("duplicate Gateway delivery = %s, %v", disposition, err)
	}
	if len(sink.envelopes) != 2 || sink.envelopes[0].MessageID != sink.envelopes[1].MessageID || sink.envelopes[0].PayloadDigest != sink.envelopes[1].PayloadDigest {
		t.Fatalf("identical duplicate was not safely re-routed: %#v", sink.envelopes)
	}
	release()
	if disposition, err := handler.Handle(ctx, bus.messageID, bus.payload); disposition != ConsumeNak || !errors.Is(err, gateway.ErrNoCurrentAgentSession) {
		t.Fatalf("missing live session delivery = %s, %v", disposition, err)
	}
	if _, err := registry.Register(hostID, 1, "connection-1-retry", sink); err != nil {
		t.Fatal(err)
	}
	if disposition, err := handler.Handle(ctx, bus.messageID, bus.payload); err != nil || disposition != ConsumeAck || len(sink.envelopes) != 3 {
		t.Fatalf("redelivery after live session recovery = %s, %v, routes=%d", disposition, err, len(sink.envelopes))
	}

	conflictingMessage, err := Decode(bus.payload, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	conflictingMessage.Envelope.ResourceGeneration++
	conflicting, err := conflictingMessage.Encode(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if disposition, err := handler.Handle(ctx, bus.messageID, conflicting); disposition != ConsumeTerm || err == nil {
		t.Fatalf("digest conflict = %s, %v", disposition, err)
	}
	var conflicts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.inbox_message_conflicts WHERE consumer='gateway-command-v1' AND message_id=$1`, bus.messageID).Scan(&conflicts); err != nil || conflicts != 1 {
		t.Fatalf("quarantine evidence count = %d, %v", conflicts, err)
	}

	uncertainCommand := hostID + "-uncertain-command"
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: uncertainCommand + "-job", CommandID: uncertainCommand, HostID: hostID, ResourceType: "VM", ResourceID: uncertainCommand + "-vm", DesiredRevision: 1, CommandType: "VM_ENSURE_STATE", SchemaVersion: "kim.command.vm-ensure-state/v1", TargetResourceID: uncertainCommand + "-vm", Payload: map[string]any{"state": "STOPPED"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.AcquireCommandLease(ctx, pool, postgres.CommandLeaseRequest{CommandID: uncertainCommand, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Minute, ExecutionTimeout: 30 * time.Second, DeliveryProtector: protector}); err != nil {
		t.Fatal(err)
	}
	uncertainBus := &recordingBus{err: errors.New("PubAck response lost")}
	uncertainPublisher := OutboxPublisher{DB: pool, Protector: protector, Bus: uncertainBus, Owner: "worker-uncertain", BatchLimit: 1, ClaimLease: 15 * time.Millisecond, MaxMessageBytes: 1 << 20}
	if count, err := uncertainPublisher.PublishOnce(ctx); count != 0 || err == nil {
		t.Fatalf("uncertain publish = %d, %v", count, err)
	}
	firstPayload := append([]byte(nil), uncertainBus.payload...)
	time.Sleep(20 * time.Millisecond)
	uncertainBus.err = nil
	if count, err := uncertainPublisher.PublishOnce(ctx); count != 1 || err != nil {
		t.Fatalf("uncertain redelivery = %d, %v", count, err)
	}
	if !bytes.Equal(firstPayload, uncertainBus.payload) {
		t.Fatal("redelivery changed stable internal message identity or payload")
	}
	var unknownEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.outbox_delivery_events WHERE message_id=$1 AND event_type='DISPATCH_UNKNOWN'`, uncertainBus.messageID).Scan(&unknownEvents); err != nil || unknownEvents != 1 {
		t.Fatalf("DISPATCH_UNKNOWN evidence count = %d, %v", unknownEvents, err)
	}

	verificationCommand := hostID + "-verification-command"
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: verificationCommand + "-job", CommandID: verificationCommand, HostID: hostID, ResourceType: "VM", ResourceID: verificationCommand + "-vm", DesiredRevision: 1, CommandType: "VM_ENSURE_STATE", SchemaVersion: "kim.command.vm-ensure-state/v1", TargetResourceID: verificationCommand + "-vm", Payload: map[string]any{"state": "RUNNING"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.AcquireCommandLease(ctx, pool, postgres.CommandLeaseRequest{CommandID: verificationCommand, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: 5 * time.Millisecond, ExecutionTimeout: time.Millisecond, DeliveryProtector: protector}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if count, err := postgres.ExpireDueCommandLeases(ctx, pool, 8); err != nil || count < 1 {
		t.Fatalf("expire verification Command = %d, %v", count, err)
	}
	if count, err := postgres.EnqueuePendingCommandVerifications(ctx, pool, 8); err != nil || count != 1 {
		t.Fatalf("enqueue verification = %d, %v", count, err)
	}
	verificationBus := &recordingBus{}
	verificationPublisher := OutboxPublisher{DB: pool, Protector: protector, Bus: verificationBus, Owner: "worker-verification", BatchLimit: 8, ClaimLease: time.Minute, MaxMessageBytes: 1 << 20}
	if count, err := verificationPublisher.PublishOnce(ctx); err != nil || count != 1 {
		t.Fatalf("publish verification = %d, %v", count, err)
	}
	verificationMessage, err := Decode(verificationBus.payload, 1<<20)
	if err != nil || verificationMessage.Envelope.SchemaVersion != contract.VerificationRequestSchema {
		t.Fatalf("verification Bus envelope = %#v, %v", verificationMessage, err)
	}
	routesBefore := len(sink.envelopes)
	if disposition, err := handler.Handle(ctx, verificationBus.messageID, verificationBus.payload); err != nil || disposition != ConsumeAck || len(sink.envelopes) != routesBefore+1 {
		t.Fatalf("verification Gateway delivery = %s, %v, routes=%d", disposition, err, len(sink.envelopes))
	}
	routesAfterVerification := len(sink.envelopes)

	if err := postgres.UpdateHostReadinessGate(ctx, pool, postgres.HostReadinessGate{HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 2, PreflightState: "FAILED", ComplianceGeneration: 2, ComplianceState: "NON_COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if disposition, err := handler.Handle(ctx, bus.messageID, bus.payload); err != nil || disposition != ConsumeAck || len(sink.envelopes) != routesAfterVerification {
		t.Fatalf("stale authority delivery = %s, %v, routes=%d", disposition, err, len(sink.envelopes))
	}
}

func testDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
