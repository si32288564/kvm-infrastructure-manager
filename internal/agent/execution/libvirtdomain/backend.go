// Package libvirtdomain implements closed typed VM power-state operations
// through the standard libvirt API. It exposes no raw XML, method, or flags.
package libvirtdomain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	CommandType   = "VIRTUAL_MACHINE_POWER_STATE_ENSURE"
	SchemaVersion = "kim.command.virtual-machine-power-state/v1"
	StateRunning  = "RUNNING"
	StateShutoff  = "SHUTOFF"
)

var vmTargetPattern = regexp.MustCompile(`^vm:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type Client interface {
	DomainState(context.Context, string) (string, error)
	StartDomain(context.Context, string) error
	ShutdownDomain(context.Context, string) error
}

type request struct {
	DesiredState          string `json:"desired_state"`
	ObservationGeneration int64  `json:"observation_generation,omitempty"`
}

type Backend struct{ Client Client }

const (
	powerReadBackInterval = 50 * time.Millisecond
	powerReadBackTimeout  = 5 * time.Second
)

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	domainUUID, desired, observationGeneration, err := decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil || backend.Client == nil {
		return agentexecution.BackendResult{}, errors.New("invalid typed libvirt power-state request")
	}
	current, err := backend.Client.DomainState(ctx, domainUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	mutated := current != desired
	if mutated {
		switch desired {
		case StateRunning:
			err = backend.Client.StartDomain(ctx, domainUUID)
		case StateShutoff:
			err = backend.Client.ShutdownDomain(ctx, domainUUID)
		}
		if err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	generation := int64(lease.AttemptIndex)
	if observationGeneration > 0 {
		generation = observationGeneration
	}
	observation, err := backend.observe(ctx, domainUUID, desired, lease.TargetResourceID, int(generation))
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if mutated && observation.State != "MATCHED" {
		deadline := time.NewTimer(powerReadBackTimeout)
		defer deadline.Stop()
		ticker := time.NewTicker(powerReadBackInterval)
		defer ticker.Stop()
		for observation.State != "MATCHED" {
			select {
			case <-ctx.Done():
				return agentexecution.BackendResult{}, ctx.Err()
			case <-deadline.C:
				return agentexecution.BackendResult{Outcome: "UNKNOWN", Result: map[string]any{"state": observation.State}, Observation: observation}, nil
			case <-ticker.C:
				observation, err = backend.observe(ctx, domainUUID, desired, lease.TargetResourceID, int(generation))
				if err != nil {
					return agentexecution.BackendResult{}, err
				}
			}
		}
	}
	outcome := "SUCCEEDED"
	if observation.State != "MATCHED" {
		outcome = "UNKNOWN"
	}
	return agentexecution.BackendResult{Outcome: outcome, Result: map[string]any{"state": observation.State}, Observation: observation}, nil
}

func (backend Backend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	domainUUID, desired, observationGeneration, err := decode(verification.TargetResourceID, verification.CommandPayload)
	if err != nil || backend.Client == nil {
		return contract.Observation{}, errors.New("invalid typed libvirt verification request")
	}
	generation := int64(verification.AttemptIndex)
	if observationGeneration > 0 {
		generation = observationGeneration
	}
	return backend.observe(ctx, domainUUID, desired, verification.TargetResourceID, int(generation))
}

func (backend Backend) observe(ctx context.Context, domainUUID, desired, target string, generation int) (contract.Observation, error) {
	current, err := backend.Client.DomainState(ctx, domainUUID)
	if err != nil {
		return contract.Observation{}, err
	}
	state := "CONFLICTING"
	if current == desired {
		state = "MATCHED"
	}
	evidence := map[string]any{"target_resource_id": target, "domain_uuid": domainUUID, "desired_state": desired, "observed_state": current, "source": "libvirt_domain_state"}
	payload, _ := json.Marshal(evidence)
	digest := sha256.Sum256(payload)
	return contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}, nil
}

func decode(target string, payload []byte) (string, string, int64, error) {
	match := vmTargetPattern.FindStringSubmatch(target)
	if match == nil {
		return "", "", 0, errors.New("invalid VM target identity")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return "", "", 0, err
	}
	desired.DesiredState = strings.ToUpper(desired.DesiredState)
	if desired.DesiredState != StateRunning && desired.DesiredState != StateShutoff {
		return "", "", 0, errors.New("unsupported VM power state")
	}
	if desired.ObservationGeneration < 0 {
		return "", "", 0, errors.New("invalid VM power observation generation")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", "", 0, errors.New("trailing VM power-state payload")
	}
	return match[1], desired.DesiredState, desired.ObservationGeneration, nil
}
