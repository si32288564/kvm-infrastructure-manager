// Package locallvm implements closed typed Local LVM volume realization.
// It accepts stable KIM identities only and never exposes arbitrary commands,
// paths, LVM flags, VG names, or LV names to Command callers.
package locallvm

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
	CommandType   = "LOCAL_LVM_VOLUME_ENSURE"
	SchemaVersion = "kim.command.local-lvm-volume-ensure/v1"
	StatePresent  = "PRESENT"
	MiB           = uint64(1024 * 1024)
)

var volumeTargetPattern = regexp.MustCompile(`^volume:([a-zA-Z0-9][a-zA-Z0-9._-]{0,127})$`)

type LogicalVolume struct {
	VGUUID, LVUUID, Name string
	SizeBytes            uint64
	DeviceOpen           bool
}

type Client interface {
	VerifyVolumeGroup(context.Context, string, string) error
	LogicalVolume(context.Context, string, string) (LogicalVolume, bool, error)
	CreateLogicalVolume(context.Context, string, string, uint64) error
}

type request struct {
	VGUUID       string `json:"vg_uuid"`
	SizeMiB      uint64 `json:"size_mib"`
	DesiredState string `json:"desired_state"`
}

type Backend struct {
	Client       Client
	VolumeGroups map[string]string // immutable admin mapping: VG UUID -> VG name
}

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	decoded, err := backend.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if err := backend.Client.VerifyVolumeGroup(ctx, decoded.vgName, decoded.request.VGUUID); err != nil {
		return agentexecution.BackendResult{}, err
	}
	_, found, err := backend.Client.LogicalVolume(ctx, decoded.vgName, decoded.lvName)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if !found {
		if err := backend.Client.CreateLogicalVolume(ctx, decoded.vgName, decoded.lvName, decoded.request.SizeMiB); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	observation, err := backend.observe(ctx, decoded, lease.TargetResourceID, lease.AttemptIndex)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	outcome := "UNKNOWN"
	if observation.State == "MATCHED" {
		outcome = "SUCCEEDED"
	}
	return agentexecution.BackendResult{Outcome: outcome, Result: observation.Evidence, Observation: observation}, nil
}

func (backend Backend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	decoded, err := backend.decode(verification.TargetResourceID, verification.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	if err := backend.Client.VerifyVolumeGroup(ctx, decoded.vgName, decoded.request.VGUUID); err != nil {
		return contract.Observation{}, err
	}
	return backend.observe(ctx, decoded, verification.TargetResourceID, verification.AttemptIndex)
}

type decodedRequest struct {
	volumeID string
	vgName   string
	lvName   string
	request  request
}

func (backend Backend) decode(target string, payload []byte) (decodedRequest, error) {
	if backend.Client == nil {
		return decodedRequest{}, errors.New("Local LVM client is required")
	}
	match := volumeTargetPattern.FindStringSubmatch(target)
	if match == nil {
		return decodedRequest{}, errors.New("invalid Volume target identity")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return decodedRequest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return decodedRequest{}, errors.New("trailing Local LVM payload")
	}
	desired.DesiredState = strings.ToUpper(desired.DesiredState)
	if desired.DesiredState != StatePresent || desired.VGUUID == "" || desired.SizeMiB == 0 || desired.SizeMiB > 16*1024*1024 {
		return decodedRequest{}, errors.New("invalid Local LVM desired state")
	}
	vgName, allowed := backend.VolumeGroups[desired.VGUUID]
	if !allowed || !validLVMName(vgName) {
		return decodedRequest{}, errors.New("Local LVM VG UUID is not configured")
	}
	return decodedRequest{volumeID: match[1], vgName: vgName, lvName: ResourceKey(match[1]), request: desired}, nil
}

func (backend Backend) observe(ctx context.Context, decoded decodedRequest, target string, generation int) (contract.Observation, error) {
	lv, found, err := backend.Client.LogicalVolume(ctx, decoded.vgName, decoded.lvName)
	if err != nil {
		return contract.Observation{}, err
	}
	evidence := map[string]any{
		"target_resource_id": target, "volume_id": decoded.volumeID,
		"backend_resource_key": decoded.lvName, "vg_uuid": decoded.request.VGUUID,
		"desired_size_bytes": decoded.request.SizeMiB * MiB, "source": "lvm_lvs",
	}
	state := "NOT_APPLIED"
	if found {
		evidence["observed_vg_uuid"] = lv.VGUUID
		evidence["observed_lv_uuid"] = lv.LVUUID
		evidence["observed_size_bytes"] = lv.SizeBytes
		state = "CONFLICTING"
		if lv.VGUUID == decoded.request.VGUUID && lv.Name == decoded.lvName && lv.LVUUID != "" && lv.SizeBytes == decoded.request.SizeMiB*MiB {
			state = "MATCHED"
		}
	}
	payload, _ := json.Marshal(evidence)
	digest := sha256.Sum256(payload)
	return contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}, nil
}

// ResourceKey derives the only LV name that KIM may create for a Volume.
func ResourceKey(volumeID string) string {
	digest := sha256.Sum256([]byte(volumeID))
	return "kim-" + hex.EncodeToString(digest[:16])
}

func validLVMName(value string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,126}$`, value)
	return matched && value != "." && value != ".." && !strings.Contains(value, "--")
}
