// Command kim-agent-grpc-reconnect-storm connects the real gRPC/mTLS adapter
// to Gateway admission and PostgreSQL session authority under a reconnect wave.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/gateway"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/reconnect"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/faultproxy"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/grpcstream"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type result struct {
	Sessions                  int           `json:"sessions"`
	AdmissionLimit            int           `json:"admission_limit"`
	TLSHandshakeLimit         int           `json:"tls_handshake_limit"`
	DatabasePoolLimit         int32         `json:"database_pool_limit"`
	StormDuration             time.Duration `json:"storm_duration_ns"`
	SessionsPerSecond         float64       `json:"sessions_per_second"`
	OpenP50                   time.Duration `json:"mtls_grant_open_p50_ns"`
	OpenP95                   time.Duration `json:"mtls_grant_open_p95_ns"`
	OpenP99                   time.Duration `json:"mtls_grant_open_p99_ns"`
	AdmissionRejected         int64         `json:"admission_rejected"`
	TLSHandshakeRejected      int64         `json:"tls_handshake_rejected"`
	TLSHandshakeRejectedTotal int64         `json:"tls_handshake_rejected_warm_and_storm"`
	TLSHandshakePeak          int64         `json:"tls_handshake_peak"`
	MaximumAttempts           int           `json:"maximum_agent_attempts"`
	MeanAttempts              float64       `json:"mean_agent_attempts"`
	AcceptedConnections       int64         `json:"accepted_physical_connections_total"`
	StormConnections          int64         `json:"accepted_physical_connections_storm"`
	ActiveConnections         int           `json:"active_physical_connections"`
	ProxyEnabled              bool          `json:"tls_passthrough_proxy_enabled"`
	ProxyAcceptedConnections  int64         `json:"proxy_accepted_physical_connections_total"`
	ProxyDrainedConnections   int64         `json:"proxy_drained_connections_total"`
	ProxyDisconnectsObserved  int           `json:"proxy_disconnects_observed"`
	ProxyActiveConnections    int           `json:"proxy_active_connections"`
	CurrentGenerationTwo      int           `json:"current_generation_two"`
	PoolEmptyAcquireCount     int64         `json:"pool_empty_acquire_count"`
	PoolAcquireDuration       time.Duration `json:"pool_acquire_duration_ns"`
}

type waveResult struct {
	connections []session.TransportConnection
	latencies   []time.Duration
	attempts    []int
	duration    time.Duration
	rejected    int64
	poolBefore  *pgxpool.Stat
	poolAfter   *pgxpool.Stat
	err         error
}

func main() {
	databaseURL := flag.String("database-url", os.Getenv("KIM_POSTGRES_TEST_URL"), "dedicated PostgreSQL URL")
	sessions := flag.Int("sessions", 1000, "Host Agents in the reconnect wave")
	admissionLimit := flag.Int("admission-limit", 16, "concurrent Gateway authority admissions")
	tlsHandshakeLimit := flag.Int("tls-handshake-limit", 32, "concurrent pre-auth TLS handshakes")
	databaseConnections := flag.Int("database-connections", 32, "PostgreSQL pool maximum")
	baseBackoff := flag.Duration("base-backoff", 25*time.Millisecond, "Agent reconnect base delay")
	maximumBackoff := flag.Duration("max-backoff", time.Second, "Agent reconnect maximum delay")
	retryAfter := flag.Duration("retry-after", 25*time.Millisecond, "Gateway admission retry hint")
	proxyEnabled := flag.Bool("tls-passthrough-proxy", false, "route Agent mTLS through an opaque TCP proxy and drain generation one")
	flag.Parse()
	if *databaseURL == "" || *sessions < 1 || *admissionLimit < 1 || *tlsHandshakeLimit < 1 || *databaseConnections < 1 {
		fatal(errors.New("database URL and positive limits are required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, int32(*databaseConnections))
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		fatal(err)
	}
	if err := ensureAuthority(ctx, pool); err != nil {
		fatal(err)
	}
	runID := fmt.Sprintf("q094-grpc-%d", time.Now().UnixNano())
	if err := seedHosts(ctx, pool, runID, *sessions); err != nil {
		fatal(err)
	}

	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	countingListener := contracttest.NewCountingListener(listener)
	limiter, err := gateway.NewAdmissionLimiter(*admissionLimit)
	if err != nil {
		fatal(err)
	}
	handshakeLimiter, err := gateway.NewHandshakeLimiter(*tlsHandshakeLimit)
	if err != nil {
		fatal(err)
	}
	limitedCredentials, err := gateway.NewLimitedTransportCredentials(credentials.NewTLS(serverTLS), handshakeLimiter)
	if err != nil {
		fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(limitedCredentials))
	agentprotocolv1.RegisterAgentTransportServer(server, gateway.GRPCServer{Authorizer: gateway.PostgresSessionAuthorizer{
		DB: pool, Admission: limiter, RetryAfter: *retryAfter,
	}})
	go func() { _ = server.Serve(countingListener) }()
	defer server.Stop()
	target := countingListener.Addr().String()
	var transportProxy *faultproxy.Proxy
	if *proxyEnabled {
		front, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			fatal(listenErr)
		}
		transportProxy, err = faultproxy.New(front, target, 5*time.Second)
		if err != nil {
			fatal(err)
		}
		go func() { _ = transportProxy.Serve() }()
		defer func() { _ = transportProxy.Close() }()
		target = transportProxy.Addr().String()
	}
	adapter := &grpcstream.Adapter{Target: target, TLSConfig: clientTLS, MaxMessageBytes: 64 * 1024}
	policy := reconnect.Backoff{Base: *baseBackoff, Max: *maximumBackoff}

	warm := runWave(ctx, adapter, pool, limiter, runID, *sessions, 1, policy)
	if warm.err != nil {
		fatal(fmt.Errorf("warm gRPC session authority: %w", warm.err))
	}
	if transportProxy != nil {
		drained := transportProxy.Drain()
		if drained < *sessions {
			fatal(fmt.Errorf("proxy drained %d connections, want at least %d", drained, *sessions))
		}
		if err := observeDisconnects(warm.connections, 5*time.Second); err != nil {
			fatal(err)
		}
	}
	closeAll(warm.connections)
	if !waitForActive(countingListener, 0, 5*time.Second) {
		fatal(fmt.Errorf("warm physical connections did not drain: active=%d", countingListener.Active()))
	}
	handshakeRejectedBeforeStorm := handshakeLimiter.Rejected()
	acceptedBeforeStorm := countingListener.Accepted()
	storm := runWave(ctx, adapter, pool, limiter, runID, *sessions, 2, policy)
	if storm.err != nil {
		fatal(storm.err)
	}
	defer closeAll(storm.connections)

	var current int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_transport_sessions_current WHERE host_id LIKE $1 AND session_generation = 2 AND state = 'CURRENT'`, runID+"-host-%").Scan(&current); err != nil {
		fatal(err)
	}
	totalAttempts, maximumAttempts := 0, 0
	for _, attempts := range storm.attempts {
		totalAttempts += attempts
		if attempts > maximumAttempts {
			maximumAttempts = attempts
		}
	}
	output := result{
		Sessions: *sessions, AdmissionLimit: *admissionLimit, TLSHandshakeLimit: *tlsHandshakeLimit, DatabasePoolLimit: int32(*databaseConnections),
		StormDuration: storm.duration, SessionsPerSecond: float64(*sessions) / storm.duration.Seconds(),
		OpenP50: percentile(storm.latencies, 50), OpenP95: percentile(storm.latencies, 95), OpenP99: percentile(storm.latencies, 99),
		AdmissionRejected: storm.rejected, TLSHandshakeRejected: handshakeLimiter.Rejected() - handshakeRejectedBeforeStorm,
		TLSHandshakeRejectedTotal: handshakeLimiter.Rejected(), TLSHandshakePeak: handshakeLimiter.Peak(),
		MaximumAttempts: maximumAttempts, MeanAttempts: float64(totalAttempts) / float64(*sessions),
		AcceptedConnections: countingListener.Accepted(), StormConnections: countingListener.Accepted() - acceptedBeforeStorm,
		ActiveConnections: countingListener.Active(), CurrentGenerationTwo: current,
		PoolEmptyAcquireCount: storm.poolAfter.EmptyAcquireCount() - storm.poolBefore.EmptyAcquireCount(),
		PoolAcquireDuration:   storm.poolAfter.AcquireDuration() - storm.poolBefore.AcquireDuration(),
	}
	if transportProxy != nil {
		output.ProxyEnabled = true
		output.ProxyAcceptedConnections = transportProxy.Accepted()
		output.ProxyDrainedConnections = transportProxy.Drained()
		output.ProxyDisconnectsObserved = *sessions
		output.ProxyActiveConnections = transportProxy.Active()
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fatal(err)
	}
}

func runWave(ctx context.Context, adapter session.TransportAdapter, pool *pgxpool.Pool, limiter *gateway.AdmissionLimiter, runID string, count, wave int, policy reconnect.Backoff) waveResult {
	connections := make([]session.TransportConnection, count)
	latencies := make([]time.Duration, count)
	attempts := make([]int, count)
	var wait sync.WaitGroup
	var firstError atomic.Pointer[error]
	start := make(chan struct{})
	poolBefore := pool.Stat()
	rejectedBefore := limiter.Rejected()
	started := time.Now()
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			hostStarted := time.Now()
			for attempt := 1; ; attempt++ {
				attempts[index] = attempt
				connection, err := adapter.Open(ctx, session.Handshake{
					HostIdentity: fmt.Sprintf("%s-host-%06d", runID, index), SessionGeneration: uint64(wave),
					ProtocolVersion: "v1", SessionAttemptID: fmt.Sprintf("%s-host-%06d-wave-%d", runID, index, wave),
					ConnectionInstanceID: fmt.Sprintf("wave-%d-physical-%d", wave, attempt),
					AgentArtifactDigest:  fmt.Sprintf("%064x", wave), CredentialBindingRevision: int64(wave),
					Capabilities: []string{"kim.agent.libvirt.v1", "kim.agent.storage.v1", "kim.agent.network.v1"},
				})
				if err == nil {
					connections[index] = connection
					latencies[index] = time.Since(hostStarted)
					return
				}
				var rejection *session.AdmissionRejectedError
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					storeError(&firstError, err)
					return
				}
				if errors.As(err, &rejection) && !rejection.Retryable {
					storeError(&firstError, err)
					return
				}
				delay, delayErr := policy.Delay(attempt, uint64(index+1)*0x9e3779b97f4a7c15+uint64(wave*attempt))
				if delayErr != nil {
					storeError(&firstError, delayErr)
					return
				}
				if rejection != nil && rejection.RetryAfter > delay {
					delay = rejection.RetryAfter
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					storeError(&firstError, context.Cause(ctx))
					return
				case <-timer.C:
				}
			}
		}(index)
	}
	close(start)
	wait.Wait()
	returned := waveResult{
		connections: connections, latencies: latencies, attempts: attempts, duration: time.Since(started),
		rejected: limiter.Rejected() - rejectedBefore, poolBefore: poolBefore, poolAfter: pool.Stat(),
	}
	if stored := firstError.Load(); stored != nil {
		returned.err = *stored
		closeAll(connections)
	}
	return returned
}

func ensureAuthority(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode) VALUES ('q094-grpc-fixture',1,'ACTIVE') ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'`)
	return err
}

func seedHosts(ctx context.Context, pool *pgxpool.Pool, runID string, count int) error {
	rows := make([][]any, count)
	for index := 0; index < count; index++ {
		rows[index] = []any{fmt.Sprintf("%s-host-%06d", runID, index), "APPROVED"}
	}
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"kim", "host_identities"}, []string{"host_id", "enrollment_state"}, pgx.CopyFromRows(rows))
	return err
}

func closeAll(connections []session.TransportConnection) {
	for _, connection := range connections {
		if connection != nil {
			_ = connection.Close()
		}
	}
}

func waitForActive(listener *contracttest.CountingListener, target int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if listener.Active() <= target {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return listener.Active() <= target
}

func observeDisconnects(connections []session.TransportConnection, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var wait sync.WaitGroup
	var firstError atomic.Pointer[error]
	for index, connection := range connections {
		wait.Add(1)
		go func(index int, connection session.TransportConnection) {
			defer wait.Done()
			if connection == nil {
				storeError(&firstError, fmt.Errorf("warm connection %d is nil", index))
				return
			}
			if _, err := connection.Receive(ctx); err == nil {
				storeError(&firstError, fmt.Errorf("warm connection %d did not observe proxy drain", index))
			} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				storeError(&firstError, fmt.Errorf("warm connection %d drain observation: %w", index, err))
			}
		}(index, connection)
	}
	wait.Wait()
	if stored := firstError.Load(); stored != nil {
		return *stored
	}
	return nil
}

func percentile(values []time.Duration, percentage int) time.Duration {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	index := (len(sorted)*percentage + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}

func storeError(target *atomic.Pointer[error], err error) {
	value := err
	target.CompareAndSwap(nil, &value)
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
