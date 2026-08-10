// Package libvirtvolume implements closed typed Local LVM disk attachment
// through standard libvirt APIs. Command callers cannot provide XML, paths,
// libvirt methods, flags, target names, or LVM command arguments.
package libvirtvolume

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
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	CommandType     = "LOCAL_LVM_VOLUME_ATTACHMENT_ENSURE"
	SchemaVersion   = "kim.command.local-lvm-volume-attachment/v1"
	StateAttached   = "ATTACHED"
	StateDetached   = "DETACHED"
	SingleWriter    = "SINGLE_WRITER"
	minimumDiskSlot = 1
	maximumDiskSlot = 25
)

var (
	attachmentTargetPattern = regexp.MustCompile(`^attachment:([A-Za-z0-9][A-Za-z0-9._-]{0,127})$`)
	domainUUIDPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type DiskObservation struct {
	Present, ReadOnly          bool
	SourcePath, Target, Serial string
}

type DomainClient interface {
	Disk(context.Context, string, string) (DiskObservation, error)
	AttachDisk(context.Context, string, DiskObservation) error
	DetachDisk(context.Context, string, DiskObservation) error
}

type VolumeResolver interface {
	Resolve(context.Context, string, string, string) (locallvm.LogicalVolume, string, error)
}

type Backend struct {
	Domains DomainClient
	Volumes VolumeResolver
}

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

type request struct {
	DomainUUID         string `json:"domain_uuid"`
	VolumeID           string `json:"volume_id"`
	VGUUID             string `json:"vg_uuid"`
	LVUUID             string `json:"lv_uuid"`
	BackendResourceKey string `json:"backend_resource_key"`
	DiskSlot           uint8  `json:"disk_slot"`
	DesiredState       string `json:"desired_state"`
	AccessMode         string `json:"access_mode"`
}

type decodedRequest struct {
	attachmentID, target, serial string
	request                      request
}

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	decoded, err := backend.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	volume, sourcePath, err := backend.Volumes.Resolve(ctx, decoded.request.VGUUID, decoded.request.BackendResourceKey, decoded.request.LVUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	current, err := backend.Domains.Disk(ctx, decoded.request.DomainUUID, decoded.target)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	expected := DiskObservation{Present: true, SourcePath: sourcePath, Target: decoded.target, Serial: decoded.serial, ReadOnly: false}
	switch decoded.request.DesiredState {
	case StateAttached:
		if current.Present && !sameDisk(current, expected) {
			observation := backend.observation(decoded, volume, current, sourcePath, "CONFLICTING", lease.AttemptIndex)
			return agentexecution.BackendResult{Outcome: "UNKNOWN", Result: observation.Evidence, Observation: observation}, nil
		}
		if !current.Present {
			if err := backend.Domains.AttachDisk(ctx, decoded.request.DomainUUID, expected); err != nil {
				return agentexecution.BackendResult{}, err
			}
		}
	case StateDetached:
		if current.Present && !sameDisk(current, expected) {
			observation := backend.observation(decoded, volume, current, sourcePath, "CONFLICTING", lease.AttemptIndex)
			return agentexecution.BackendResult{Outcome: "UNKNOWN", Result: observation.Evidence, Observation: observation}, nil
		}
		if current.Present {
			if err := backend.Domains.DetachDisk(ctx, decoded.request.DomainUUID, expected); err != nil {
				return agentexecution.BackendResult{}, err
			}
		}
	}
	observation, err := backend.observe(ctx, decoded, lease.AttemptIndex)
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
	return backend.observe(ctx, decoded, verification.AttemptIndex)
}

func (backend Backend) observe(ctx context.Context, decoded decodedRequest, generation int) (contract.Observation, error) {
	volume, sourcePath, err := backend.Volumes.Resolve(ctx, decoded.request.VGUUID, decoded.request.BackendResourceKey, decoded.request.LVUUID)
	if err != nil {
		return contract.Observation{}, err
	}
	disk, err := backend.Domains.Disk(ctx, decoded.request.DomainUUID, decoded.target)
	if err != nil {
		return contract.Observation{}, err
	}
	expected := DiskObservation{Present: true, SourcePath: sourcePath, Target: decoded.target, Serial: decoded.serial, ReadOnly: false}
	state := "CONFLICTING"
	if decoded.request.DesiredState == StateAttached && sameDisk(disk, expected) && volume.DeviceOpen {
		state = "MATCHED"
	}
	if decoded.request.DesiredState == StateDetached && !disk.Present && !volume.DeviceOpen {
		state = "MATCHED"
	}
	return backend.observation(decoded, volume, disk, sourcePath, state, generation), nil
}

func (backend Backend) observation(decoded decodedRequest, volume locallvm.LogicalVolume, disk DiskObservation, expectedSourcePath, state string, generation int) contract.Observation {
	evidence := map[string]any{
		"attachment_id": decoded.attachmentID, "domain_uuid": decoded.request.DomainUUID,
		"volume_id": decoded.request.VolumeID, "vg_uuid": decoded.request.VGUUID,
		"observed_vg_uuid": volume.VGUUID, "observed_lv_uuid": volume.LVUUID,
		"backend_resource_key": decoded.request.BackendResourceKey,
		"desired_state":        decoded.request.DesiredState, "target_device": decoded.target,
		"device_present": disk.Present, "device_identity_matches": disk.Present && disk.Target == decoded.target && disk.Serial == decoded.serial,
		"source_identity_matches": disk.Present && disk.SourcePath == expectedSourcePath,
		"read_only":               disk.ReadOnly, "holder_open": volume.DeviceOpen,
		"source": "libvirt_domain_xml+lvm_lv_device_open",
	}
	payload, _ := json.Marshal(evidence)
	digest := sha256.Sum256(payload)
	return contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}
}

func (backend Backend) decode(target string, payload []byte) (decodedRequest, error) {
	if backend.Domains == nil || backend.Volumes == nil {
		return decodedRequest{}, errors.New("complete libvirt Volume backend is required")
	}
	match := attachmentTargetPattern.FindStringSubmatch(target)
	if match == nil {
		return decodedRequest{}, errors.New("invalid Volume Attachment target identity")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return decodedRequest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return decodedRequest{}, errors.New("trailing Volume Attachment payload")
	}
	desired.DesiredState = strings.ToUpper(desired.DesiredState)
	desired.AccessMode = strings.ToUpper(desired.AccessMode)
	if !domainUUIDPattern.MatchString(desired.DomainUUID) || desired.VolumeID == "" || desired.VGUUID == "" || desired.LVUUID == "" || desired.BackendResourceKey != locallvm.ResourceKey(desired.VolumeID) || desired.DiskSlot < minimumDiskSlot || desired.DiskSlot > maximumDiskSlot || desired.AccessMode != SingleWriter || (desired.DesiredState != StateAttached && desired.DesiredState != StateDetached) {
		return decodedRequest{}, errors.New("invalid typed Volume Attachment request")
	}
	return decodedRequest{attachmentID: match[1], target: "vd" + string(rune('a'+desired.DiskSlot)), serial: volumeSerial(desired.VolumeID), request: desired}, nil
}

func sameDisk(observed, expected DiskObservation) bool {
	return observed.Present && observed.SourcePath == expected.SourcePath && observed.Target == expected.Target && observed.Serial == expected.Serial && observed.ReadOnly == expected.ReadOnly
}

func volumeSerial(volumeID string) string {
	digest := sha256.Sum256([]byte(volumeID))
	return "KIM-" + hex.EncodeToString(digest[:16])
}
