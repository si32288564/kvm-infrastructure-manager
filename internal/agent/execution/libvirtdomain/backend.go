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
	DesiredState string `json:"desired_state"`
}

type Backend struct{ Client Client }

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	domainUUID, desired, err := decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil || backend.Client == nil {
		return agentexecution.BackendResult{}, errors.New("invalid typed libvirt power-state request")
	}
	current, err := backend.Client.DomainState(ctx, domainUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if current != desired {
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
	observation, err := backend.observe(ctx, domainUUID, desired, lease.TargetResourceID, lease.AttemptIndex)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	outcome := "SUCCEEDED"
	if observation.State != "MATCHED" {
		outcome = "UNKNOWN"
	}
	return agentexecution.BackendResult{Outcome: outcome, Result: map[string]any{"state": observation.State}, Observation: observation}, nil
}

func (backend Backend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	domainUUID, desired, err := decode(verification.TargetResourceID, verification.CommandPayload)
	if err != nil || backend.Client == nil {
		return contract.Observation{}, errors.New("invalid typed libvirt verification request")
	}
	return backend.observe(ctx, domainUUID, desired, verification.TargetResourceID, verification.AttemptIndex)
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

func decode(target string, payload []byte) (string, string, error) {
	match := vmTargetPattern.FindStringSubmatch(target)
	if match == nil {
		return "", "", errors.New("invalid VM target identity")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return "", "", err
	}
	desired.DesiredState = strings.ToUpper(desired.DesiredState)
	if desired.DesiredState != StateRunning && desired.DesiredState != StateShutoff {
		return "", "", errors.New("unsupported VM power state")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", "", errors.New("trailing VM power-state payload")
	}
	return match[1], desired.DesiredState, nil
}
