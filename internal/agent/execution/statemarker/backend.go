package statemarker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	CommandType   = "HOST_AGENT_STATE_MARKER_ENSURE"
	SchemaVersion = "kim.command.host-agent-state-marker/v1"
)

type request struct {
	Value string `json:"value"`
}

// Backend mutates only a KIM-owned state directory. It is the first closed
// execution-plane qualification backend and never changes KVM/libvirt state.
type Backend struct{ Directory string }

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	if backend.Directory == "" {
		return agentexecution.BackendResult{}, errors.New("state marker directory is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(lease.CommandPayload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil || desired.Value == "" || len(desired.Value) > 256 {
		return agentexecution.BackendResult{}, errors.New("invalid state marker request")
	}
	select {
	case <-ctx.Done():
		return agentexecution.BackendResult{}, ctx.Err()
	default:
	}
	if err := os.MkdirAll(backend.Directory, 0o700); err != nil {
		return agentexecution.BackendResult{}, err
	}
	targetDigest := sha256.Sum256([]byte(lease.TargetResourceID))
	path := filepath.Join(backend.Directory, hex.EncodeToString(targetDigest[:])+".json")
	payload, _ := json.Marshal(map[string]string{"target_resource_id": lease.TargetResourceID, "value": desired.Value})
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(backend.Directory, ".tmp-")
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return agentexecution.BackendResult{}, err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return agentexecution.BackendResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return agentexecution.BackendResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return agentexecution.BackendResult{}, err
	}
	if err := os.Rename(name, path); err != nil {
		return agentexecution.BackendResult{}, err
	}
	directory, err := os.Open(backend.Directory)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return agentexecution.BackendResult{}, err
	}
	if err := directory.Close(); err != nil {
		return agentexecution.BackendResult{}, err
	}
	observed, err := os.ReadFile(path)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	digest := sha256.Sum256(observed)
	return agentexecution.BackendResult{Outcome: "SUCCEEDED", Result: map[string]any{"state": "APPLIED"}, Observation: contract.Observation{State: "MATCHED", Generation: int64(lease.AttemptIndex), Digest: hex.EncodeToString(digest[:]), Evidence: map[string]any{"target_resource_id": lease.TargetResourceID, "value": desired.Value}}}, nil
}
