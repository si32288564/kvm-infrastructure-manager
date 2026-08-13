package locallvmtransport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
)

const Capability = "kim.host.local-lvm-cross-host-transport.v1"

// EndpointRegistry is administrator-owned endpoint discovery. Commands carry
// no URL, address, path, or port and cannot redirect guest blocks.
type EndpointRegistry struct{ endpoints map[string]string }

func NewEndpointRegistry(configured map[string]string) (EndpointRegistry, error) {
	registry := EndpointRegistry{endpoints: make(map[string]string, len(configured))}
	for hostID, raw := range configured {
		parsed, err := url.Parse(raw)
		if err != nil || hostID == "" || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return EndpointRegistry{}, errors.New("Local LVM transport endpoint must be an administrator-owned HTTPS origin")
		}
		registry.endpoints[hostID] = strings.TrimSuffix(parsed.String(), "/") + StreamPath
	}
	return registry, nil
}

func (registry EndpointRegistry) endpoint(hostID string) (string, error) {
	endpoint, ok := registry.endpoints[hostID]
	if !ok {
		return "", ErrAuthorityConflict
	}
	return endpoint, nil
}

type routeKey struct {
	id         string
	generation uint64
}

// SourceRegistry holds only authorities delivered through the current normal
// Agent session. Activate invalidates every route when that session changes.
type SourceRegistry struct {
	mu                 sync.Mutex
	hostID             string
	credentialRevision uint64
	certificateDigest  string
	sessionGeneration  uint64
	routes             map[routeKey]Authority
	active             int
}

func NewSourceRegistry(hostID string, credentialRevision uint64, certificateDigest string) (*SourceRegistry, error) {
	if hostID == "" || credentialRevision == 0 || len(certificateDigest) != 64 {
		return nil, errors.New("complete Local LVM source runtime identity is required")
	}
	return &SourceRegistry{hostID: hostID, credentialRevision: credentialRevision, certificateDigest: certificateDigest, routes: map[routeKey]Authority{}}, nil
}

func (registry *SourceRegistry) Activate(sessionGeneration uint64, credentialRevision int64) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if sessionGeneration == 0 || credentialRevision < 1 || uint64(credentialRevision) != registry.credentialRevision {
		return ErrAuthorityConflict
	}
	registry.sessionGeneration = sessionGeneration
	registry.routes = map[routeKey]Authority{}
	return nil
}

func (registry *SourceRegistry) Deactivate(sessionGeneration uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sessionGeneration == sessionGeneration {
		registry.sessionGeneration = 0
		registry.routes = map[routeKey]Authority{}
	}
}

func (registry *SourceRegistry) Authorize(authority Authority, hostAuthorityGeneration, sessionGeneration uint64) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if authority.Validate(time.Now()) != nil || authority.Source.HostID != registry.hostID || authority.SourceHostAuthorityGeneration != hostAuthorityGeneration || authority.SourceCredentialBindingRevision != registry.credentialRevision || authority.SourceSessionGeneration != sessionGeneration || registry.sessionGeneration != sessionGeneration || authority.SourceCertificateFingerprint != registry.certificateDigest {
		return ErrAuthorityConflict
	}
	key := routeKey{authority.TransportSessionID, authority.TransportGeneration}
	if prior, ok := registry.routes[key]; ok && prior.Digest() != authority.Digest() {
		return ErrAuthorityConflict
	}
	registry.routes[key] = authority
	return nil
}

func (registry *SourceRegistry) acquire(id string, generation uint64, digest string) (Authority, func(), error) {
	registry.mu.Lock()
	authority, ok := registry.routes[routeKey{id, generation}]
	if !ok || registry.sessionGeneration == 0 || authority.SourceSessionGeneration != registry.sessionGeneration || authority.Digest() != digest || authority.Validate(time.Now()) != nil || registry.active >= authority.MaximumConcurrentPerHost {
		registry.mu.Unlock()
		return Authority{}, nil, ErrAuthorityConflict
	}
	registry.active++
	registry.mu.Unlock()
	return authority, func() {
		registry.mu.Lock()
		registry.active--
		registry.mu.Unlock()
	}, nil
}

type SourceRouter struct {
	Registry *SourceRegistry
	Reader   SourceReader
	Metrics  *Metrics
}

func (router SourceRouter) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if router.Registry == nil || request.URL.Path != StreamPath || request.URL.RawQuery != "" {
		http.Error(w, "Local LVM transport authority required", http.StatusUnauthorized)
		return
	}
	authority, release, err := router.Registry.acquire(header(request, HeaderSessionID), headerUint(request, HeaderGeneration), header(request, HeaderAuthorityDigest))
	if err != nil {
		if router.Metrics != nil {
			router.Metrics.IntegrityFailures.Add(1)
		}
		http.Error(w, "Local LVM transport authority rejected", http.StatusForbidden)
		return
	}
	defer release()
	SourceHandler{Authority: authority, Reader: router.Reader, Metrics: router.Metrics}.ServeHTTP(w, request)
}

type RuntimeConfig struct {
	HostID, ListenAddress string
	CredentialRevision    uint64
	TLSConfig             *tls.Config
	Reader                SourceReader
	Writer                DestinationWriter
	Endpoints             EndpointRegistry
	Metrics               *Metrics
}

// Runtime owns the closed source listener and the two typed execution roles.
// Start completes before the normal Agent session can become current.
type Runtime struct {
	config   RuntimeConfig
	registry *SourceRegistry
	server   *http.Server
	listener net.Listener
	client   *http.Client
}

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.HostID == "" || config.ListenAddress == "" || config.CredentialRevision == 0 || config.TLSConfig == nil || config.Reader == nil || config.Writer == nil || len(config.TLSConfig.Certificates) != 1 || config.TLSConfig.ClientCAs == nil || config.TLSConfig.RootCAs == nil {
		return nil, errors.New("complete enabled Local LVM transport runtime configuration is required")
	}
	digest := certificateFingerprint(config.TLSConfig.Certificates[0])
	registry, err := NewSourceRegistry(config.HostID, config.CredentialRevision, digest)
	if err != nil {
		return nil, err
	}
	strict := config.TLSConfig.Clone()
	strict.MinVersion, strict.MaxVersion = tls.VersionTLS13, tls.VersionTLS13
	strict.ClientAuth = tls.RequireAndVerifyClientCert
	strict.NextProtos = []string{"h2"}
	return &Runtime{config: config, registry: registry, client: NewMutualTLSClient(strict), server: &http.Server{Handler: SourceRouter{Registry: registry, Reader: config.Reader, Metrics: config.Metrics}, TLSConfig: strict, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}}, nil
}

func (runtime *Runtime) Start(_ context.Context) error {
	listener, err := net.Listen("tcp", runtime.config.ListenAddress)
	if err != nil {
		return fmt.Errorf("start Local LVM transport listener: %w", err)
	}
	runtime.listener = listener
	go func() { _ = runtime.server.ServeTLS(listener, "", "") }()
	return nil
}

func (runtime *Runtime) Activate(sessionGeneration uint64, credentialRevision int64) error {
	return runtime.registry.Activate(sessionGeneration, credentialRevision)
}

func (runtime *Runtime) Deactivate(sessionGeneration uint64) {
	runtime.registry.Deactivate(sessionGeneration)
}

func (runtime *Runtime) Close(ctx context.Context) error { return runtime.server.Shutdown(ctx) }

func (runtime *Runtime) Backends() []agentexecution.Backend {
	return []agentexecution.Backend{
		SourceAuthorizeBackend{Registry: runtime.registry, Reader: runtime.config.Reader, LocalCertificateFingerprint: runtime.registry.certificateDigest, CredentialRevision: runtime.config.CredentialRevision},
		DestinationBackend{HostID: runtime.config.HostID, Writer: runtime.config.Writer, Client: runtime.client, Endpoints: runtime.config.Endpoints, LocalCertificateFingerprint: runtime.registry.certificateDigest, CredentialRevision: runtime.config.CredentialRevision, Metrics: runtime.config.Metrics},
	}
}

func (runtime *Runtime) Address() string {
	if runtime.listener == nil {
		return ""
	}
	return runtime.listener.Addr().String()
}

func certificateFingerprint(certificate tls.Certificate) string {
	digest := sha256.Sum256(certificate.Certificate[0])
	return hex.EncodeToString(digest[:])
}
