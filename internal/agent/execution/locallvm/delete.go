package locallvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	DeleteCommandType    = "LOCAL_LVM_VOLUME_DELETE"
	DeleteSchemaVersion  = "kim.command.local-lvm-volume-delete/v1"
	DeleteReadBackType   = "LOCAL_LVM_VOLUME_DELETE_READ_BACK"
	DeleteReadBackSchema = "kim.command.local-lvm-volume-delete-read-back/v1"
	StateAbsent          = "ABSENT"
)

type DeleteClient interface {
	VerifyVolumeGroup(context.Context, string, string) error
	LogicalVolume(context.Context, string, string) (LogicalVolume, bool, error)
	RemoveLogicalVolume(context.Context, string, string) error
}

type deleteRequest struct {
	BackendID          string `json:"backend_id"`
	BackendGeneration  uint64 `json:"backend_generation"`
	VGUUID             string `json:"vg_uuid"`
	ExpectedLVUUID     string `json:"expected_lv_uuid"`
	BackendResourceKey string `json:"backend_resource_key"`
	BindingID          string `json:"binding_id"`
	BindingGeneration  uint64 `json:"binding_generation"`
	CleanupOperationID string `json:"cleanup_operation_id"`
	CleanupGeneration  uint64 `json:"cleanup_generation"`
	DesiredState       string `json:"desired_state"`
}

type DeleteBackend struct {
	Client       DeleteClient
	VolumeGroups map[string]string
	ReadBackOnly bool
}

func (backend DeleteBackend) CommandType() string {
	if backend.ReadBackOnly {
		return DeleteReadBackType
	}
	return DeleteCommandType
}
func (backend DeleteBackend) SchemaVersion() string {
	if backend.ReadBackOnly {
		return DeleteReadBackSchema
	}
	return DeleteSchemaVersion
}

func (backend DeleteBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	decoded, err := backend.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if err := backend.Client.VerifyVolumeGroup(ctx, decoded.vgName, decoded.request.VGUUID); err != nil {
		return agentexecution.BackendResult{}, err
	}
	lv, found, err := backend.Client.LogicalVolume(ctx, decoded.vgName, decoded.lvName)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	// A same-name replacement is foreign. The exact obsolete UUID is already
	// absent and must never be removed by this authority.
	if found && lv.LVUUID == decoded.request.ExpectedLVUUID {
		if backend.ReadBackOnly {
			return backend.result(ctx, decoded, lease.TargetResourceID, lease.AttemptIndex)
		}
		if lv.VGUUID != decoded.request.VGUUID || lv.Name != decoded.lvName || lv.DeviceOpen {
			return agentexecution.BackendResult{}, errors.New("exact Local LVM source has conflicting identity or holder")
		}
		if err := backend.Client.RemoveLogicalVolume(ctx, decoded.vgName, decoded.lvName); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	return backend.result(ctx, decoded, lease.TargetResourceID, lease.AttemptIndex)
}

func (backend DeleteBackend) Observe(ctx context.Context, request contract.VerificationRequest) (contract.Observation, error) {
	decoded, err := backend.decode(request.TargetResourceID, request.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	if err := backend.Client.VerifyVolumeGroup(ctx, decoded.vgName, decoded.request.VGUUID); err != nil {
		return contract.Observation{}, err
	}
	return backend.observe(ctx, decoded, request.TargetResourceID, request.AttemptIndex)
}

func (backend DeleteBackend) result(ctx context.Context, decoded deleteDecoded, target string, attempt int) (agentexecution.BackendResult, error) {
	observation, err := backend.observe(ctx, decoded, target, attempt)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	outcome := "UNKNOWN"
	if observation.State == "MATCHED" {
		outcome = "SUCCEEDED"
	}
	return agentexecution.BackendResult{Outcome: outcome, Result: observation.Evidence, Observation: observation}, nil
}

type deleteDecoded struct {
	volumeID, vgName, lvName string
	request                  deleteRequest
}

func (backend DeleteBackend) decode(target string, payload []byte) (deleteDecoded, error) {
	if backend.Client == nil {
		return deleteDecoded{}, errors.New("Local LVM delete client is required")
	}
	match := volumeTargetPattern.FindStringSubmatch(target)
	if match == nil {
		return deleteDecoded{}, errors.New("invalid Volume target identity")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired deleteRequest
	if err := decoder.Decode(&desired); err != nil {
		return deleteDecoded{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return deleteDecoded{}, errors.New("trailing Local LVM delete payload")
	}
	desired.DesiredState = strings.ToUpper(desired.DesiredState)
	if desired.DesiredState != StateAbsent || desired.BackendID == "" || desired.BackendGeneration == 0 || desired.VGUUID == "" || desired.ExpectedLVUUID == "" || desired.BindingID == "" || desired.BindingGeneration == 0 || desired.CleanupOperationID == "" || desired.CleanupGeneration == 0 {
		return deleteDecoded{}, errors.New("invalid Local LVM delete authority")
	}
	vgName, ok := backend.VolumeGroups[desired.VGUUID]
	lvName := ResourceKey(match[1])
	if !ok || !validLVMName(vgName) || desired.BackendResourceKey != lvName {
		return deleteDecoded{}, errors.New("Local LVM identity is not configured")
	}
	return deleteDecoded{match[1], vgName, lvName, desired}, nil
}

func (backend DeleteBackend) observe(ctx context.Context, decoded deleteDecoded, target string, attempt int) (contract.Observation, error) {
	lv, found, err := backend.Client.LogicalVolume(ctx, decoded.vgName, decoded.lvName)
	if err != nil {
		return contract.Observation{}, err
	}
	exactPresent, foreign := false, false
	evidence := map[string]any{"target_resource_id": target, "volume_id": decoded.volumeID, "backend_id": decoded.request.BackendID, "backend_generation": decoded.request.BackendGeneration, "vg_uuid": decoded.request.VGUUID, "expected_lv_uuid": decoded.request.ExpectedLVUUID, "backend_resource_key": decoded.request.BackendResourceKey, "binding_id": decoded.request.BindingID, "binding_generation": decoded.request.BindingGeneration, "cleanup_operation_id": decoded.request.CleanupOperationID, "cleanup_generation": decoded.request.CleanupGeneration, "source": "lvm_lvs"}
	state := "MATCHED"
	if found {
		evidence["observed_lv_uuid"] = lv.LVUUID
		exactPresent = lv.LVUUID == decoded.request.ExpectedLVUUID
		foreign = !exactPresent
		if exactPresent {
			state = "NOT_APPLIED"
		}
	}
	evidence["exact_source_lv_present"], evidence["foreign_replacement_present"] = exactPresent, foreign
	payload, _ := json.Marshal(evidence)
	digest := sha256.Sum256(payload)
	return contract.Observation{State: state, Generation: int64(attempt), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}, nil
}
