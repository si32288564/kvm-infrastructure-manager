// Command kim-agent-reconnect-storm measures bounded Gateway admission and
// PostgreSQL session-authority behavior under a synchronized reconnect wave.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/gateway"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/reconnect"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type result struct {
	Sessions               int           `json:"sessions"`
	GatewayAdmissionLimit  int           `json:"gateway_admission_limit"`
	DatabasePoolLimit      int32         `json:"database_pool_limit"`
	StormDuration          time.Duration `json:"storm_duration_ns"`
	SessionsPerSecond      float64       `json:"sessions_per_second"`
	ReconnectP50           time.Duration `json:"reconnect_p50_ns"`
	ReconnectP95           time.Duration `json:"reconnect_p95_ns"`
	ReconnectP99           time.Duration `json:"reconnect_p99_ns"`
	DatabaseTxP50          time.Duration `json:"database_tx_p50_ns"`
	DatabaseTxP95          time.Duration `json:"database_tx_p95_ns"`
	DatabaseTxP99          time.Duration `json:"database_tx_p99_ns"`
	AdmissionRejected      int64         `json:"admission_rejected"`
	AdmissionPeak          int64         `json:"admission_peak"`
	MaximumAgentAttempts   int           `json:"maximum_agent_attempts"`
	MeanAgentAttempts      float64       `json:"mean_agent_attempts"`
	PoolAcquireCount       int64         `json:"pool_acquire_count"`
	PoolEmptyAcquireCount  int64         `json:"pool_empty_acquire_count"`
	PoolAcquireDuration    time.Duration `json:"pool_acquire_duration_ns"`
	CurrentGenerationTwo   int           `json:"current_generation_two"`
	ImmutableAttemptRows   int           `json:"immutable_attempt_rows"`
	ImmutableLifecycleRows int           `json:"immutable_lifecycle_event_rows"`
}

type waveResult struct {
	total         time.Duration
	reconnect     []time.Duration
	database      []time.Duration
	attempts      []int
	rejected      int64
	peak          int64
	poolBefore    *pgxpool.Stat
	poolAfter     *pgxpool.Stat
	returnedError error
}

func main() {
	databaseURL := flag.String("database-url", os.Getenv("KIM_POSTGRES_TEST_URL"), "dedicated PostgreSQL URL")
	sessions := flag.Int("sessions", 1000, "Host Agents in the reconnect wave")
	admissionLimit := flag.Int("admission-limit", 64, "concurrent Gateway authority admissions")
	databaseConnections := flag.Int("database-connections", 32, "PostgreSQL pool maximum")
	baseBackoff := flag.Duration("base-backoff", 2*time.Millisecond, "Agent reconnect base delay")
	maximumBackoff := flag.Duration("max-backoff", 100*time.Millisecond, "Agent reconnect maximum delay")
	flag.Parse()
	if *databaseURL == "" {
		fatal(errors.New("dedicated database URL is required"))
	}
	if *sessions < 1 || *admissionLimit < 1 || *databaseConnections < 1 {
		fatal(errors.New("session, admission, and database limits must be positive"))
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
	if err := ensureDatabaseAuthority(ctx, pool); err != nil {
		fatal(err)
	}
	runID := fmt.Sprintf("q094-%d", time.Now().UnixNano())
	if err := seedHosts(ctx, pool, runID, *sessions); err != nil {
		fatal(err)
	}
	policy := reconnect.Backoff{Base: *baseBackoff, Max: *maximumBackoff}
	if warm := runWave(ctx, pool, runID, *sessions, *admissionLimit, policy, 1); warm.returnedError != nil {
		fatal(fmt.Errorf("warm current session authority: %w", warm.returnedError))
	}
	if err := activateCredentialRevision(ctx, pool, runID, 2); err != nil {
		fatal(err)
	}
	storm := runWave(ctx, pool, runID, *sessions, *admissionLimit, policy, 2)
	if storm.returnedError != nil {
		fatal(storm.returnedError)
	}

	currentGeneration, attempts, events, err := verifyEvidence(ctx, pool, runID)
	if err != nil {
		fatal(err)
	}
	attemptSum, maximumAttempts := 0, 0
	for _, count := range storm.attempts {
		attemptSum += count
		if count > maximumAttempts {
			maximumAttempts = count
		}
	}
	output := result{
		Sessions: *sessions, GatewayAdmissionLimit: *admissionLimit, DatabasePoolLimit: int32(*databaseConnections),
		StormDuration: storm.total, SessionsPerSecond: float64(*sessions) / storm.total.Seconds(),
		ReconnectP50: percentile(storm.reconnect, 50), ReconnectP95: percentile(storm.reconnect, 95), ReconnectP99: percentile(storm.reconnect, 99),
		DatabaseTxP50: percentile(storm.database, 50), DatabaseTxP95: percentile(storm.database, 95), DatabaseTxP99: percentile(storm.database, 99),
		AdmissionRejected: storm.rejected, AdmissionPeak: storm.peak, MaximumAgentAttempts: maximumAttempts,
		MeanAgentAttempts:     float64(attemptSum) / float64(*sessions),
		PoolAcquireCount:      storm.poolAfter.AcquireCount() - storm.poolBefore.AcquireCount(),
		PoolEmptyAcquireCount: storm.poolAfter.EmptyAcquireCount() - storm.poolBefore.EmptyAcquireCount(),
		PoolAcquireDuration:   storm.poolAfter.AcquireDuration() - storm.poolBefore.AcquireDuration(),
		CurrentGenerationTwo:  currentGeneration, ImmutableAttemptRows: attempts, ImmutableLifecycleRows: events,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fatal(err)
	}
}

func runWave(ctx context.Context, pool *pgxpool.Pool, runID string, sessions, admissionLimit int, policy reconnect.Backoff, wave int) waveResult {
	limiter, err := gateway.NewAdmissionLimiter(admissionLimit)
	if err != nil {
		return waveResult{returnedError: err}
	}
	start := make(chan struct{})
	reconnectLatencies := make([]time.Duration, sessions)
	databaseLatencies := make([]time.Duration, sessions)
	attemptCounts := make([]int, sessions)
	var wait sync.WaitGroup
	var firstError atomic.Pointer[error]
	poolBefore := pool.Stat()
	waveStarted := time.Now()
	for index := 0; index < sessions; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			started := time.Now()
			for attempt := 1; ; attempt++ {
				attemptCounts[index] = attempt
				release, admissionErr := limiter.TryAcquire()
				if errors.Is(admissionErr, gateway.ErrAdmissionLimited) {
					delay, delayErr := policy.Delay(attempt, uint64(index+1)*0x9e3779b97f4a7c15+uint64(wave*attempt))
					if delayErr != nil {
						storeFirstError(&firstError, delayErr)
						return
					}
					timer := time.NewTimer(delay)
					select {
					case <-ctx.Done():
						timer.Stop()
						storeFirstError(&firstError, context.Cause(ctx))
						return
					case <-timer.C:
					}
					continue
				}
				if admissionErr != nil {
					storeFirstError(&firstError, admissionErr)
					return
				}
				databaseStarted := time.Now()
				grant, grantErr := postgres.AdmitAgentSession(ctx, pool, postgres.AgentSessionAdmission{
					SessionAttemptID:     fmt.Sprintf("%s-host-%06d-wave-%d", runID, index, wave),
					HostID:               fmt.Sprintf("%s-host-%06d", runID, index),
					ConnectionInstanceID: fmt.Sprintf("connection-%d", wave), TransportProfile: "q094-reconnect-storm",
					ProtocolVersion: "v1", AgentArtifactDigest: fmt.Sprintf("%064x", wave), CredentialBindingRevision: int64(wave),
					PeerCertificateFingerprint: fixtureCredentialFingerprint(wave),
					ExpectedSessionGeneration:  int64(wave),
					HandshakeEvidence:          map[string]any{"wave": wave},
				})
				databaseLatencies[index] = time.Since(databaseStarted)
				release()
				if grantErr != nil {
					storeFirstError(&firstError, grantErr)
					return
				}
				if grant.SessionGeneration != int64(wave) {
					storeFirstError(&firstError, fmt.Errorf("Host %d generation = %d, want %d", index, grant.SessionGeneration, wave))
					return
				}
				reconnectLatencies[index] = time.Since(started)
				return
			}
		}(index)
	}
	close(start)
	wait.Wait()
	returned := waveResult{
		total: time.Since(waveStarted), reconnect: reconnectLatencies, database: databaseLatencies, attempts: attemptCounts,
		rejected: limiter.Rejected(), peak: limiter.Peak(), poolBefore: poolBefore, poolAfter: pool.Stat(),
	}
	if stored := firstError.Load(); stored != nil {
		returned.returnedError = *stored
	}
	return returned
}

func ensureDatabaseAuthority(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('q094-fixture', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode = 'ACTIVE'
	`)
	return err
}

func seedHosts(ctx context.Context, pool *pgxpool.Pool, runID string, count int) error {
	rows := make([][]any, count)
	for index := 0; index < count; index++ {
		rows[index] = []any{fmt.Sprintf("%s-host-%06d", runID, index), "APPROVED"}
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"kim", "host_identities"}, []string{"host_id", "enrollment_state"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.host_enrollment_decisions (
			decision_id, host_id, decision_revision, policy_id, policy_generation,
			hardware_evidence_digest, decision_state, actor_id, reason_code
		)
		SELECT host_id || '-enrollment-1', host_id, 1, 'q094-fixture', 1, $2,
		       'APPROVED', 'q094-fixture', 'fixture'
		FROM kim.host_identities WHERE host_id LIKE $1
	`, runID+"-host-%", fmt.Sprintf("%064x", 1)); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.host_enrollment_bindings_current (host_id, decision_id, decision_revision, binding_state)
		SELECT host_id, host_id || '-enrollment-1', 1, 'ENROLLED'
		FROM kim.host_identities WHERE host_id LIKE $1
	`, runID+"-host-%"); err != nil {
		return err
	}
	return activateCredentialRevision(ctx, pool, runID, 1)
}

func activateCredentialRevision(ctx context.Context, pool *pgxpool.Pool, runID string, revision int) error {
	fingerprint := fixtureCredentialFingerprint(revision)
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.agent_credential_binding_evidence (
			host_id, binding_revision, certificate_fingerprint_sha256, public_key_digest,
			issuer_id, certificate_profile_revision, trust_generation,
			enrollment_decision_id, enrollment_decision_revision, evidence_digest,
			binding_state, valid_not_before, valid_not_after
		)
		SELECT host_id, $2, $3, $4, 'q094-fixture-ca', 'host-agent/v1', 1,
		       host_id || '-enrollment-1', 1, $5, 'ACTIVE',
		       statement_timestamp() - interval '1 hour', statement_timestamp() + interval '1 hour'
		FROM kim.host_identities WHERE host_id LIKE $1
	`, runID+"-host-%", revision, fingerprint, fmt.Sprintf("%064x", revision+10), fmt.Sprintf("%064x", revision+20)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO kim.agent_credential_bindings_current (host_id, binding_revision, binding_state, trust_generation)
		SELECT host_id, $2, 'CURRENT', 1 FROM kim.host_identities WHERE host_id LIKE $1
		ON CONFLICT (host_id) DO UPDATE SET binding_revision=EXCLUDED.binding_revision,
		binding_state='CURRENT', updated_at=statement_timestamp()
	`, runID+"-host-%", revision)
	return err
}

func fixtureCredentialFingerprint(revision int) string {
	return fmt.Sprintf("%064x", revision+100)
}

func verifyEvidence(ctx context.Context, pool *pgxpool.Pool, runID string) (current, attempts, events int, returnedErr error) {
	like := runID + "-host-%"
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_transport_sessions_current WHERE host_id LIKE $1 AND session_generation = 2 AND state = 'CURRENT'`, like).Scan(&current); err != nil {
		return 0, 0, 0, err
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_transport_session_attempts WHERE host_id LIKE $1`, like).Scan(&attempts); err != nil {
		return 0, 0, 0, err
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_transport_session_events AS event JOIN kim.agent_transport_session_attempts AS attempt USING (session_attempt_id) WHERE attempt.host_id LIKE $1`, like).Scan(&events); err != nil {
		return 0, 0, 0, err
	}
	return current, attempts, events, nil
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

func storeFirstError(target *atomic.Pointer[error], err error) {
	if err == nil {
		return
	}
	value := err
	target.CompareAndSwap(nil, &value)
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
