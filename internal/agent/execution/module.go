package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type BackendResult struct {
	Outcome     string
	Result      map[string]any
	Observation contract.Observation
}

type Backend interface {
	CommandType() string
	SchemaVersion() string
	Execute(context.Context, contract.CommandLease) (BackendResult, error)
}

type Observer interface {
	Observe(context.Context, contract.VerificationRequest) (contract.Observation, error)
}

type Publisher interface {
	Publish(session.Envelope) error
}

// Module executes only compile-time registered typed backends. It never owns
// a socket, certificate, reconnect loop, or arbitrary command primitive.
type Module struct {
	hostID         string
	journal        *executionjournal.Journal
	publisher      Publisher
	verifierDigest string
	mu             sync.RWMutex
	backends       map[string]Backend
	sealed         bool
}

func NewModule(hostID string, journal *executionjournal.Journal, publisher Publisher, verifierDigest string) (*Module, error) {
	if hostID == "" || journal == nil || publisher == nil || len(verifierDigest) != 64 {
		return nil, errors.New("complete Agent execution module dependencies are required")
	}
	return &Module{hostID: hostID, journal: journal, publisher: publisher, verifierDigest: verifierDigest, backends: make(map[string]Backend)}, nil
}

func (module *Module) RegisterBackend(backend Backend) error {
	if backend == nil || backend.CommandType() == "" || backend.SchemaVersion() == "" {
		return errors.New("typed execution backend descriptor is required")
	}
	key := backend.CommandType() + "\x00" + backend.SchemaVersion()
	module.mu.Lock()
	defer module.mu.Unlock()
	if module.sealed {
		return errors.New("typed execution backend registry is sealed")
	}
	if _, found := module.backends[key]; found {
		return errors.New("typed execution backend already registered")
	}
	module.backends[key] = backend
	return nil
}

func (module *Module) Descriptor() session.ModuleDescriptor {
	return session.ModuleDescriptor{Name: "execution", Capabilities: []string{"kim.agent.execution/v1"}, MinSchemaVersion: contract.CommandLeaseSchema, MaxSchemaVersion: contract.VerificationRequestSchema, MessageSchemas: []string{contract.CommandLeaseSchema, contract.VerificationRequestSchema}}
}

func (module *Module) Handle(ctx context.Context, envelope session.Envelope) error {
	if envelope.Stream != session.StreamCommand {
		return errors.New("execution module received unsupported logical message")
	}
	if envelope.SchemaVersion == contract.VerificationRequestSchema {
		return module.handleVerification(ctx, envelope)
	}
	if envelope.SchemaVersion != contract.CommandLeaseSchema {
		return errors.New("execution module received unsupported schema")
	}
	lease, err := contract.DecodeCommandLease(envelope.Payload)
	if err != nil {
		return err
	}
	if lease.HostID != module.hostID || envelope.HostIdentity != module.hostID || uint64(lease.SessionGeneration) != envelope.SessionGeneration || lease.CommandID != envelope.CorrelationKey {
		return errors.New("Command Lease identity does not match current Agent envelope")
	}
	digest := sha256.Sum256(lease.CommandPayload)
	if hex.EncodeToString(digest[:]) != lease.CommandPayloadDigest {
		return errors.New("Command payload digest mismatch")
	}
	module.mu.Lock()
	module.sealed = true
	backend := module.backends[lease.CommandType+"\x00"+lease.CommandSchemaVersion]
	module.mu.Unlock()
	if backend == nil {
		return fmt.Errorf("unsupported typed Command %s/%s", lease.CommandType, lease.CommandSchemaVersion)
	}
	journalDigest, err := module.journal.Prepare(lease.CommandID, lease.AttemptIndex, lease.CommandPayloadDigest, lease.TargetResourceID)
	if err != nil {
		return err
	}
	executionContext, cancel := context.WithTimeout(ctx, time.Duration(lease.ExecutionTimeoutMillis)*time.Millisecond)
	defer cancel()
	backendResult, executeErr := backend.Execute(executionContext, lease)
	if executeErr != nil {
		backendResult = BackendResult{Outcome: "UNKNOWN", Result: map[string]any{"reason": "backend_execution_uncertain"}, Observation: contract.Observation{State: "UNKNOWN", Generation: int64(lease.AttemptIndex), Digest: digestValue("unknown"), Evidence: map[string]any{"error_class": "backend_execution"}}}
	}
	resultPayload, err := json.Marshal(backendResult.Result)
	if err != nil {
		return err
	}
	resultDigest := sha256.Sum256(resultPayload)
	if _, err := module.journal.Complete(lease.CommandID, lease.AttemptIndex, hex.EncodeToString(resultDigest[:]), backendResult.Outcome); err != nil {
		return err
	}
	result := contract.CommandResult{
		SchemaVersion: contract.CommandResultSchema, CommandID: lease.CommandID,
		AttemptIndex: lease.AttemptIndex, LeaseToken: lease.LeaseToken, JournalDigest: journalDigest,
		ResultID: fmt.Sprintf("%s-result-%d", lease.CommandID, lease.AttemptIndex), Outcome: backendResult.Outcome,
		Result: backendResult.Result, Observation: backendResult.Observation, VerifierDigest: module.verifierDigest,
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	message := session.NewEnvelope(module.hostID, envelope.SessionGeneration, session.StreamResult,
		fmt.Sprintf("command-result/%s/%d", lease.CommandID, lease.AttemptIndex), contract.CommandResultSchema,
		"command/"+lease.CommandID, uint64(lease.AttemptIndex), payload)
	message.CorrelationKey = lease.CommandID
	return module.publisher.Publish(message)
}

func (module *Module) handleVerification(ctx context.Context, envelope session.Envelope) error {
	request, err := contract.DecodeVerificationRequest(envelope.Payload)
	if err != nil {
		return err
	}
	if request.HostID != module.hostID || envelope.HostIdentity != module.hostID || uint64(request.SessionGeneration) != envelope.SessionGeneration || request.CommandID != envelope.CorrelationKey || uint64(request.AttemptIndex) != envelope.Sequence {
		return errors.New("Verification Request identity mismatch")
	}
	digest := sha256.Sum256(request.CommandPayload)
	if hex.EncodeToString(digest[:]) != request.CommandPayloadDigest {
		return errors.New("Verification Request payload digest mismatch")
	}
	module.mu.Lock()
	module.sealed = true
	backend := module.backends[request.CommandType+"\x00"+request.CommandSchemaVersion]
	module.mu.Unlock()
	observer, ok := backend.(Observer)
	if !ok {
		return errors.New("typed backend does not support read-back verification")
	}
	_, journalDigest, err := module.journal.Evidence(request.CommandID, request.AttemptIndex, request.CommandPayloadDigest, request.TargetResourceID)
	var observation contract.Observation
	if errors.Is(err, executionjournal.ErrNotFound) {
		// A valid read-only Verification request with no local STARTED evidence
		// is an UNKNOWN domain observation, not a transport/session failure. Do
		// not manufacture a STARTED record or invoke the backend as proof that
		// this Agent executed the Command.
		journalDigest = digestValue("journal_evidence_absent")
		observation = contract.Observation{State: "UNKNOWN", Generation: int64(request.AttemptIndex), Digest: digestValue("verification_journal_absent"), Evidence: map[string]any{"error_class": "journal_evidence_unavailable", "journal_state": "ABSENT"}}
	} else if err != nil {
		return err
	} else {
		observation, err = observer.Observe(ctx, request)
		if err != nil {
			observation = contract.Observation{State: "UNKNOWN", Generation: int64(request.AttemptIndex), Digest: digestValue("verification_unknown"), Evidence: map[string]any{"error_class": "read_back"}}
		}
	}
	response := contract.VerificationObservation{SchemaVersion: contract.VerificationObservationSchema, CommandID: request.CommandID, AttemptIndex: request.AttemptIndex, TargetResourceID: request.TargetResourceID, CommandPayloadDigest: request.CommandPayloadDigest, Observation: observation, VerifierDigest: module.verifierDigest, JournalDigest: journalDigest}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	message := session.NewEnvelope(module.hostID, envelope.SessionGeneration, session.StreamResync, fmt.Sprintf("verification-observation/%s/%d", request.CommandID, request.AttemptIndex), contract.VerificationObservationSchema, "command/"+request.CommandID, uint64(request.AttemptIndex), payload)
	message.CorrelationKey = request.CommandID
	return module.publisher.Publish(message)
}

func digestValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

var _ session.Module = (*Module)(nil)
