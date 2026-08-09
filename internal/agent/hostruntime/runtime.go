// Package hostruntime wires the product Host Agent process lifecycle without
// exposing transport ownership to typed modules.
package hostruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/statemarker"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/reconnect"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/sessiongeneration"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/spool"
)

type Config struct {
	HostID                    string
	ProtocolVersion           string
	AgentArtifactDigest       string
	CredentialBindingRevision int64
	VerifierDigest            string
	StateDirectory            string
	SpoolDirectory            string
	JournalDirectory          string
	GenerationDirectory       string
	Adapter                   session.TransportAdapter
	QueueLimits               session.QueueLimits
	SpoolMaxEntries           int
	SpoolMaxBytes             int64
	FlushInterval             time.Duration
	ReconnectBackoff          reconnect.Backoff
}

type durablePublisher struct {
	manager *session.Manager
	spool   *spool.Spool
}

func (publisher durablePublisher) Publish(envelope session.Envelope) error {
	if err := publisher.spool.Enqueue(envelope); err != nil {
		return err
	}
	return publisher.manager.Publish(envelope)
}

func Run(ctx context.Context, config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	generationLedger, err := sessiongeneration.Open(config.GenerationDirectory, config.HostID)
	if err != nil {
		return fmt.Errorf("open Agent session generation ledger: %w", err)
	}
	defer generationLedger.Close()
	durableSpool, err := spool.Open(spool.Config{Directory: config.SpoolDirectory, HostIdentity: config.HostID, MaxEntries: config.SpoolMaxEntries, MaxBytes: config.SpoolMaxBytes, MaxMessageBytes: config.QueueLimits.MaxMessageBytes})
	if err != nil {
		return fmt.Errorf("open Agent durable spool: %w", err)
	}
	defer durableSpool.Close()
	executionJournal, err := executionjournal.Open(config.JournalDirectory, config.HostID)
	if err != nil {
		return fmt.Errorf("open Agent execution journal: %w", err)
	}
	defer executionJournal.Close()

	attempt := 1
	for {
		if err := context.Cause(ctx); err != nil {
			return nil
		}
		generation, err := generationLedger.Next()
		if err != nil {
			return err
		}
		queue, err := session.NewPriorityQueue(config.QueueLimits)
		if err != nil {
			return err
		}
		sessionNonce, err := randomID()
		if err != nil {
			return err
		}
		connectionNonce, err := randomID()
		if err != nil {
			return err
		}
		manager, err := session.NewManager(session.Handshake{
			HostIdentity: config.HostID, SessionGeneration: generation, ProtocolVersion: config.ProtocolVersion,
			SessionAttemptID:     fmt.Sprintf("%s-session-%d-%s", config.HostID, generation, sessionNonce),
			ConnectionInstanceID: fmt.Sprintf("%s-connection-%d-%s", config.HostID, generation, connectionNonce),
			AgentArtifactDigest:  config.AgentArtifactDigest, CredentialBindingRevision: config.CredentialBindingRevision,
		}, config.Adapter, queue)
		if err != nil {
			return err
		}
		module, err := agentexecution.NewModule(config.HostID, executionJournal, durablePublisher{manager: manager, spool: durableSpool}, config.VerifierDigest)
		if err != nil {
			return err
		}
		if err := module.RegisterBackend(statemarker.Backend{Directory: config.StateDirectory}); err != nil {
			return err
		}
		if err := manager.RegisterModule(module); err != nil {
			return err
		}
		if err := manager.Open(ctx); err != nil {
			if !waitReconnect(ctx, config.ReconnectBackoff, attempt, config.HostID) {
				return nil
			}
			attempt++
			continue
		}
		if err := generationLedger.CommitAccepted(generation); err != nil {
			_ = manager.Close()
			return err
		}
		pending, err := durableSpool.Pending()
		if err == nil {
			for _, envelope := range pending {
				if err = manager.Publish(envelope.BindSession(generation)); err != nil {
					break
				}
			}
		}
		if err == nil {
			err = (session.Runner{Manager: manager, ReceiptHandler: durableSpool, FlushInterval: config.FlushInterval}).Run(ctx)
		}
		_ = manager.Close()
		if context.Cause(ctx) != nil {
			return nil
		}
		attempt = 1
		if !waitReconnect(ctx, config.ReconnectBackoff, attempt, config.HostID) {
			return nil
		}
	}
}

func validate(config Config) error {
	if config.HostID == "" || config.ProtocolVersion == "" || len(config.AgentArtifactDigest) != 64 || config.CredentialBindingRevision < 1 || len(config.VerifierDigest) != 64 || config.StateDirectory == "" || config.SpoolDirectory == "" || config.JournalDirectory == "" || config.GenerationDirectory == "" || config.Adapter == nil || config.SpoolMaxEntries < 1 || config.SpoolMaxBytes < 1 || config.FlushInterval <= 0 {
		return errors.New("complete bounded Host Agent runtime configuration is required")
	}
	if _, err := config.ReconnectBackoff.Delay(1, 1); err != nil {
		return fmt.Errorf("invalid Host Agent reconnect backoff: %w", err)
	}
	return nil
}

func waitReconnect(ctx context.Context, backoff reconnect.Backoff, attempt int, hostID string) bool {
	delay, err := backoff.Delay(attempt, entropy(hostID, attempt))
	if err != nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func entropy(hostID string, attempt int) uint64 {
	var value uint64 = uint64(attempt)
	for _, character := range []byte(hostID) {
		value = value*1099511628211 ^ uint64(character)
	}
	return value
}

func randomID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read connection identity entropy: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
