package locallvmtransport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type runtimePublisher struct {
	fail bool
	last session.Envelope
}

func (publisher *runtimePublisher) Publish(envelope session.Envelope) error {
	publisher.last = envelope
	if publisher.fail {
		return errors.New("qualified response loss after side effect")
	}
	return nil
}

func runtimeTLS(t *testing.T) (source, destination *tls.Config) {
	t.Helper()
	source, destination, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	source = source.Clone()
	source.RootCAs = source.ClientCAs
	source.MaxVersion = tls.VersionTLS13
	destination = destination.Clone()
	destination.ClientCAs = destination.RootCAs
	destination.ClientAuth = tls.RequireAndVerifyClientCert
	destination.MaxVersion = tls.VersionTLS13
	return source, destination
}

func TestNormalAgentRuntimeTransfersThroughTypedAsymmetricRoles(t *testing.T) {
	sourceTLS, destinationTLS := runtimeTLS(t)
	sourceID := VolumeIdentity{"host-a", "volume-a", "binding-a", "vg-a", "lv-a", 3}
	destinationID := VolumeIdentity{"host-b", "volume-b", "binding-b", "vg-b", "lv-b", 7}
	content := make([]byte, 16<<10)
	copy(content, "unique-normal-agent-runtime-marker")
	copy(content[len(content)-64:], "second-marker-near-end")
	sourceStore := &sourceMemory{identity: sourceID, content: append([]byte(nil), content...)}
	destinationStore := &destinationMemory{identity: destinationID, content: make([]byte, len(content))}

	emptyEndpoints, err := NewEndpointRegistry(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	sourceRuntime, err := NewRuntime(RuntimeConfig{HostID: "host-a", ListenAddress: "127.0.0.1:0", CredentialRevision: 5, TLSConfig: sourceTLS, Reader: sourceStore, Writer: &destinationMemory{identity: sourceID, content: make([]byte, len(content))}, Endpoints: emptyEndpoints, Metrics: &Metrics{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceRuntime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceRuntime.Close(context.Background()) })
	endpoints, err := NewEndpointRegistry(map[string]string{"host-a": "https://" + sourceRuntime.Address()})
	if err != nil {
		t.Fatal(err)
	}
	destinationRuntime, err := NewRuntime(RuntimeConfig{HostID: "host-b", ListenAddress: "127.0.0.1:0", CredentialRevision: 7, TLSConfig: destinationTLS, Reader: &sourceMemory{identity: destinationID, content: make([]byte, len(content))}, Writer: destinationStore, Endpoints: endpoints, Metrics: &Metrics{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := destinationRuntime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationRuntime.Close(context.Background()) })
	if err := sourceRuntime.Activate(17, 5); err != nil {
		t.Fatal(err)
	}
	if err := destinationRuntime.Activate(19, 7); err != nil {
		t.Fatal(err)
	}
	authority := Authority{TransportSessionID: "normal-session", TransportGeneration: 1, CopyOperationID: "normal-copy", CopyGeneration: 1, Source: sourceID, Destination: destinationID, SourceHostAuthorityGeneration: 11, DestinationHostAuthorityGeneration: 13, SourceCredentialBindingRevision: 5, DestinationCredentialBindingRevision: 7, SourceSessionGeneration: 17, DestinationSessionGeneration: 19, ExactByteCount: uint64(len(content)), ChunkSize: 4096, DigestAlgorithm: "SHA-256", TransportPolicyRevision: 1, MaximumConcurrentPerHost: 1, ExpiresAt: time.Now().Add(time.Minute), SourceCertificateFingerprint: certDigest(sourceTLS.Certificates[0]), DestinationCertificateFingerprint: certDigest(destinationTLS.Certificates[0])}
	payload, err := EncodeCommandPayload(authority)
	if err != nil {
		t.Fatal(err)
	}
	sourceBackend := sourceRuntime.Backends()[0].(SourceAuthorizeBackend)
	sourceResult, err := sourceBackend.Execute(t.Context(), runtimeLease(authority, payload, true))
	if err != nil || sourceResult.Observation.State != "MATCHED" || sourceResult.Observation.Evidence["peer_role"] != "SOURCE" {
		t.Fatalf("source result=%+v err=%v", sourceResult, err)
	}
	destinationBackend := destinationRuntime.Backends()[1].(DestinationBackend)
	destinationResult, err := destinationBackend.Execute(t.Context(), runtimeLease(authority, payload, false))
	if err != nil || destinationResult.Observation.State != "MATCHED" || destinationResult.Observation.Evidence["peer_role"] != "DESTINATION" || destinationResult.Observation.Evidence["response_state"] != "RECEIVED" {
		t.Fatalf("destination result=%+v err=%v", destinationResult, err)
	}
	if string(sourceStore.content) != string(destinationStore.content) || destinationStore.flushes != 1 {
		t.Fatal("normal runtime did not preserve and flush exact guest content")
	}
	if capabilities := sourceBackend.Capabilities(); len(capabilities) != 1 || capabilities[0] != Capability {
		t.Fatalf("capabilities=%v", capabilities)
	}

	// The same product backend through the normal execution module completes
	// the block side effect and then loses its Result. The existing journal and
	// Verification Request converge by destination read-back without another
	// destination or a caller success assertion.
	journal, err := executionjournal.Open(filepath.Join(t.TempDir(), "journal"), "host-b")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	publisher := &runtimePublisher{fail: true}
	module, err := agentexecution.NewModule("host-b", journal, publisher, strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(destinationBackend); err != nil {
		t.Fatal(err)
	}
	advertised := false
	for _, capability := range module.Descriptor().Capabilities {
		advertised = advertised || capability == Capability
	}
	if !advertised {
		t.Fatal("normal Agent handshake omitted enabled Local LVM transport capability")
	}
	lease := runtimeLease(authority, payload, false)
	lease.CommandID = "runtime-response-loss"
	leasePayload, _ := json.Marshal(lease)
	envelope := session.NewEnvelope("host-b", 19, session.StreamCommand, "runtime-response-loss-message", contract.CommandLeaseSchema, "transport/normal-session", 1, leasePayload)
	envelope.CorrelationKey = lease.CommandID
	if err := module.Handle(t.Context(), envelope); err == nil {
		t.Fatal("lost normal Agent Result was reported as delivered")
	}
	publisher.fail = false
	verification := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema, CommandID: lease.CommandID, AttemptIndex: 1, HostID: "host-b", SessionGeneration: 19, CommandType: DestinationCommandType, CommandSchemaVersion: DestinationSchema, TargetResourceID: lease.TargetResourceID, CommandPayload: payload, CommandPayloadDigest: lease.CommandPayloadDigest}
	verificationPayload, _ := json.Marshal(verification)
	verificationEnvelope := session.NewEnvelope("host-b", 19, session.StreamCommand, "runtime-response-loss-read-back", contract.VerificationRequestSchema, "transport/normal-session", 1, verificationPayload)
	verificationEnvelope.CorrelationKey = lease.CommandID
	if err := module.Handle(t.Context(), verificationEnvelope); err != nil {
		t.Fatal(err)
	}
	readBack, err := contract.DecodeVerificationObservation(publisher.last.Payload)
	if err != nil || readBack.Observation.State != "MATCHED" || readBack.Observation.Evidence["content_digest"] != destinationResult.Observation.Evidence["content_digest"] {
		t.Fatalf("runtime response-loss read-back=%+v err=%v", readBack, err)
	}

	// A normal Agent reconnect revokes every old route. The old authority
	// cannot be uplifted to session 18 and absence of a response proves no
	// negative side effect.
	sourceRuntime.Deactivate(17)
	if err := sourceRuntime.Activate(18, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := destinationBackend.Execute(t.Context(), runtimeLease(authority, payload, false)); err == nil {
		t.Fatal("old source Agent session route survived reconnect")
	}
	staleLease := runtimeLease(authority, payload, true)
	staleLease.SessionGeneration = 18
	if _, err := sourceBackend.Execute(t.Context(), staleLease); err == nil {
		t.Fatal("old transport authority uplifted to new source Agent session")
	}
	destinationRuntime.Deactivate(19)
	if err := destinationRuntime.Activate(20, 7); err != nil {
		t.Fatal(err)
	}
	staleDestinationLease := runtimeLease(authority, payload, false)
	staleDestinationLease.SessionGeneration = 20
	if _, err := destinationBackend.Execute(t.Context(), staleDestinationLease); err == nil {
		t.Fatal("old transport authority uplifted to new destination Agent session")
	}
	if err := sourceRuntime.Activate(18, 6); err == nil {
		t.Fatal("rotated credential reused a runtime configured for the old credential")
	}
}

func TestRuntimeRejectsTLS12UnauthenticatedAndUnregisteredEndpoints(t *testing.T) {
	sourceTLS, destinationTLS := runtimeTLS(t)
	sourceID := VolumeIdentity{"host-a", "volume-a", "binding-a", "vg-a", "lv-a", 1}
	destinationID := VolumeIdentity{"host-b", "volume-b", "binding-b", "vg-b", "lv-b", 1}
	store := &sourceMemory{identity: sourceID, content: make([]byte, 4096)}
	endpoints, _ := NewEndpointRegistry(map[string]string{})
	runtime, err := NewRuntime(RuntimeConfig{HostID: "host-a", ListenAddress: "127.0.0.1:0", CredentialRevision: 5, TLSConfig: sourceTLS, Reader: store, Writer: &destinationMemory{identity: sourceID, content: make([]byte, 4096)}, Endpoints: endpoints})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if err := runtime.Activate(17, 5); err != nil {
		t.Fatal(err)
	}
	authority := Authority{TransportSessionID: "tls-session", TransportGeneration: 1, CopyOperationID: "tls-copy", CopyGeneration: 1, Source: sourceID, Destination: destinationID, SourceHostAuthorityGeneration: 11, DestinationHostAuthorityGeneration: 13, SourceCredentialBindingRevision: 5, DestinationCredentialBindingRevision: 7, SourceSessionGeneration: 17, DestinationSessionGeneration: 19, ExactByteCount: 4096, ChunkSize: 4096, DigestAlgorithm: "SHA-256", TransportPolicyRevision: 1, MaximumConcurrentPerHost: 1, ExpiresAt: time.Now().Add(time.Minute), SourceCertificateFingerprint: certDigest(sourceTLS.Certificates[0]), DestinationCertificateFingerprint: certDigest(destinationTLS.Certificates[0])}
	payload, _ := EncodeCommandPayload(authority)
	if _, err := runtime.Backends()[0].(SourceAuthorizeBackend).Execute(t.Context(), runtimeLease(authority, payload, true)); err != nil {
		t.Fatal(err)
	}

	for name, config := range map[string]*tls.Config{
		"TLS 1.2": func() *tls.Config {
			c := destinationTLS.Clone()
			c.MinVersion, c.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			return c
		}(),
		"no client cert": func() *tls.Config { c := destinationTLS.Clone(); c.Certificates = nil; return c }(),
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: config, ForceAttemptHTTP2: true}}
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+runtime.Address()+StreamPath, nil)
			request.Header.Set(HeaderSessionID, authority.TransportSessionID)
			request.Header.Set(HeaderGeneration, "1")
			request.Header.Set(HeaderAuthorityDigest, authority.Digest())
			if response, err := client.Do(request); err == nil {
				response.Body.Close()
				t.Fatalf("%s connection accepted", name)
			}
		})
	}
	if _, err := NewEndpointRegistry(map[string]string{"host-a": "http://example.invalid:1"}); err == nil {
		t.Fatal("non-TLS arbitrary endpoint accepted")
	}
	if _, err := NewEndpointRegistry(map[string]string{"host-a": "https://example.invalid:1/arbitrary"}); err == nil {
		t.Fatal("arbitrary endpoint path accepted")
	}
	destination := DestinationBackend{HostID: "host-b", Writer: &destinationMemory{identity: destinationID, content: make([]byte, 4096)}, Client: NewMutualTLSClient(destinationTLS), Endpoints: endpoints, LocalCertificateFingerprint: authority.DestinationCertificateFingerprint, CredentialRevision: 7}
	if _, err := destination.Execute(t.Context(), runtimeLease(authority, payload, false)); err == nil {
		t.Fatal("unregistered source endpoint accepted")
	}
}

func TestRuntimeAuthorityFencesCredentialHostBindingAndExpiry(t *testing.T) {
	serverTLS, clientTLS := runtimeTLS(t)
	authority := Authority{TransportSessionID: "fence-session", TransportGeneration: 1, CopyOperationID: "fence-copy", CopyGeneration: 1, Source: VolumeIdentity{"host-a", "source", "source-binding", "vg-a", "lv-a", 1}, Destination: VolumeIdentity{"host-b", "destination", "destination-binding", "vg-b", "lv-b", 1}, SourceHostAuthorityGeneration: 11, DestinationHostAuthorityGeneration: 13, SourceCredentialBindingRevision: 5, DestinationCredentialBindingRevision: 7, SourceSessionGeneration: 17, DestinationSessionGeneration: 19, ExactByteCount: 4096, ChunkSize: 4096, DigestAlgorithm: "SHA-256", TransportPolicyRevision: 1, MaximumConcurrentPerHost: 1, ExpiresAt: time.Now().Add(time.Minute), SourceCertificateFingerprint: certDigest(serverTLS.Certificates[0]), DestinationCertificateFingerprint: certDigest(clientTLS.Certificates[0])}
	registry, _ := NewSourceRegistry("host-a", 5, authority.SourceCertificateFingerprint)
	_ = registry.Activate(17, 5)
	backend := SourceAuthorizeBackend{Registry: registry, Reader: &sourceMemory{identity: authority.Source, content: make([]byte, 4096)}, LocalCertificateFingerprint: authority.SourceCertificateFingerprint, CredentialRevision: 5}
	for name, mutate := range map[string]func(*contract.CommandLease){
		"host authority": func(lease *contract.CommandLease) { lease.HostAuthorityGeneration++ },
		"session":        func(lease *contract.CommandLease) { lease.SessionGeneration++ },
		"host":           func(lease *contract.CommandLease) { lease.HostID = "host-c" },
	} {
		t.Run(name, func(t *testing.T) {
			payload, _ := EncodeCommandPayload(authority)
			lease := runtimeLease(authority, payload, true)
			mutate(&lease)
			if _, err := backend.Execute(t.Context(), lease); err == nil {
				t.Fatalf("stale %s accepted", name)
			}
		})
	}
	credentialBackend := backend
	credentialBackend.CredentialRevision = 6
	payload, _ := EncodeCommandPayload(authority)
	if _, err := credentialBackend.Execute(t.Context(), runtimeLease(authority, payload, true)); err == nil {
		t.Fatal("stale credential revision accepted")
	}
	drift := authority
	drift.Source.BindingGeneration++
	driftPayload, _ := EncodeCommandPayload(drift)
	if _, err := backend.Execute(t.Context(), runtimeLease(drift, driftPayload, true)); err == nil {
		t.Fatal("stale Binding generation accepted")
	}
	expired := authority
	expired.ExpiresAt = time.Now().Add(-time.Second)
	expiredPayload, _ := json.Marshal(commandPayload{Authority: expired})
	if _, err := backend.Execute(t.Context(), runtimeLease(expired, expiredPayload, true)); err == nil {
		t.Fatal("expired transport session accepted")
	}
	if strings.Contains(string(payload), "/dev/") {
		t.Fatal("typed transport payload contains a device path")
	}
}

func runtimeLease(authority Authority, payload []byte, source bool) contract.CommandLease {
	host, hostAuthority, session, commandType, schema := authority.Destination.HostID, authority.DestinationHostAuthorityGeneration, authority.DestinationSessionGeneration, DestinationCommandType, DestinationSchema
	if source {
		host, hostAuthority, session, commandType, schema = authority.Source.HostID, authority.SourceHostAuthorityGeneration, authority.SourceSessionGeneration, SourceAuthorizeCommandType, SourceAuthorizeSchema
	}
	digest := sha256.Sum256(payload)
	return contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: commandType + "-command", LeaseGeneration: 1, AttemptIndex: 1, HostID: host, HostAuthorityGeneration: int64(hostAuthority), SessionGeneration: int64(session), LeaseToken: "runtime-lease", CommandType: commandType, CommandSchemaVersion: schema, TargetResourceID: "local-lvm-transport:" + authority.TransportSessionID, CommandPayload: payload, CommandPayloadDigest: hex.EncodeToString(digest[:]), ExecutionTimeoutMillis: 60_000}
}
