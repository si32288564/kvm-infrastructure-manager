package libvirtvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const CleanupCommandType = "VIRTUAL_MACHINE_UNDEFINE"
const CleanupSchemaVersion = "kim.command.virtual-machine-undefine/v1"

type CleanupObservation struct {
	Present, Running          bool
	UUID, PlanDigest          string
	MaterializationGeneration uint64
}

type CleanupClient interface {
	DomainCleanupState(context.Context, string) (CleanupObservation, error)
	UndefineDomain(context.Context, string) error
}

type CleanupBackend struct{ Client CleanupClient }

func (CleanupBackend) CommandType() string   { return CleanupCommandType }
func (CleanupBackend) SchemaVersion() string { return CleanupSchemaVersion }

type cleanupRequest struct {
	CleanupOperationID              string `json:"cleanup_operation_id"`
	CleanupGeneration               uint64 `json:"cleanup_generation"`
	DomainUUID                      string `json:"domain_uuid"`
	VMGeneration                    uint64 `json:"vm_generation"`
	SourceHostID                    string `json:"source_host_id"`
	SourcePlanDigest                string `json:"source_plan_digest"`
	SourceMaterializationGeneration uint64 `json:"source_materialization_generation"`
	BackendIdentityDigest           string `json:"backend_identity_digest"`
	DesiredState                    string `json:"desired_state"`
}

func (b CleanupBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	r, err := b.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	current, err := b.Client.DomainCleanupState(ctx, r.DomainUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if current.Present {
		if current.Running || !cleanupIdentityMatches(r, current) {
			return cleanupResult(r, current, lease.AttemptIndex), nil
		}
		if err := b.Client.UndefineDomain(ctx, r.DomainUUID); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	current, err = b.Client.DomainCleanupState(ctx, r.DomainUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	return cleanupResult(r, current, lease.AttemptIndex), nil
}

func (b CleanupBackend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	r, err := b.decode(verification.TargetResourceID, verification.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	current, err := b.Client.DomainCleanupState(ctx, r.DomainUUID)
	if err != nil {
		return contract.Observation{}, err
	}
	return cleanupResult(r, current, verification.AttemptIndex).Observation, nil
}

func (b CleanupBackend) decode(target string, payload []byte) (cleanupRequest, error) {
	match := vmTargetPattern.FindStringSubmatch(target)
	if b.Client == nil || match == nil {
		return cleanupRequest{}, errors.New("complete typed Domain cleanup authority is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var r cleanupRequest
	if err := decoder.Decode(&r); err != nil {
		return r, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return r, errors.New("trailing Domain cleanup payload")
	}
	if r.DomainUUID != match[1] || r.CleanupOperationID == "" || r.CleanupGeneration == 0 || r.VMGeneration == 0 || r.SourceHostID == "" || len(r.SourcePlanDigest) != 64 || r.SourceMaterializationGeneration == 0 || len(r.BackendIdentityDigest) != 64 || r.DesiredState != "ABSENT" {
		return r, errors.New("invalid typed Domain cleanup authority")
	}
	return r, nil
}

func cleanupIdentityMatches(r cleanupRequest, o CleanupObservation) bool {
	return o.Present && o.UUID == r.DomainUUID && o.PlanDigest == r.SourcePlanDigest && o.MaterializationGeneration == r.SourceMaterializationGeneration
}

func cleanupResult(r cleanupRequest, o CleanupObservation, generation int) agentexecution.BackendResult {
	absent := !o.Present
	identityMatches := absent || cleanupIdentityMatches(r, o)
	state, outcome := "CONFLICTING", "UNKNOWN"
	if absent {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	evidence := map[string]any{
		"cleanup_operation_id": r.CleanupOperationID, "cleanup_generation": r.CleanupGeneration,
		"domain_uuid": r.DomainUUID, "vm_generation": r.VMGeneration, "source_host_id": r.SourceHostID,
		"source_plan_digest": r.SourcePlanDigest, "source_materialization_generation": r.SourceMaterializationGeneration,
		"backend_identity_digest": r.BackendIdentityDigest, "domain_present": o.Present, "domain_running": o.Running,
		"identity_matches": identityMatches, "observed_plan_digest": o.PlanDigest,
		"observed_materialization_generation": o.MaterializationGeneration,
		"source":                              "standard_libvirt_inactive_domain_absence_read_back",
	}
	raw, _ := json.Marshal(evidence)
	sum := sha256.Sum256(raw)
	observation := contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(sum[:]), Evidence: evidence}
	return agentexecution.BackendResult{Outcome: outcome, Result: evidence, Observation: observation}
}
