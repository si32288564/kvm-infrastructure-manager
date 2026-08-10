// Package libvirtvm defines a closed typed, initially shut off libvirt Domain
// from a Final-Admission-derived plan. It accepts no XML, path, method, flag,
// machine type, CPU model, or arbitrary device description from callers.
package libvirtvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	CommandType   = "VIRTUAL_MACHINE_DEFINE"
	SchemaVersion = "kim.command.virtual-machine-define/v1"
)

var vmTargetPattern = regexp.MustCompile(`^vm:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type DomainObservation struct {
	Present                                        bool
	UUID, Name, PlanDigest, RootSource, RootLVUUID string
	MaterializationGeneration, VCPUs, MemoryMiB    uint64
	RootTarget, RootSerial                         string
}

type DomainSpec = DomainObservation

type Client interface {
	Domain(context.Context, string) (DomainObservation, error)
	Define(context.Context, DomainSpec) error
}

type Backend struct {
	Domains Client
	Volumes libvirtvolume.VolumeResolver
}

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

type rootVolume struct {
	VolumeID           string `json:"volume_id"`
	VGUUID             string `json:"vg_uuid"`
	LVUUID             string `json:"lv_uuid"`
	BackendResourceKey string `json:"backend_resource_key"`
}

type request struct {
	DomainUUID                string     `json:"domain_uuid"`
	MaterializationGeneration uint64     `json:"materialization_generation"`
	VCPUs                     uint64     `json:"vcpus"`
	MemoryMiB                 uint64     `json:"memory_mib"`
	DesiredState              string     `json:"desired_state"`
	ImageID                   string     `json:"image_id"`
	ImageRevision             uint64     `json:"image_revision"`
	ImageMaterializationState string     `json:"image_materialization_state"`
	NetworkRealizationState   string     `json:"network_realization_state"`
	RootVolume                rootVolume `json:"root_volume"`
}

type decoded struct {
	request request
	spec    DomainSpec
}

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	decoded, err := backend.decode(ctx, lease.TargetResourceID, lease.CommandPayload, lease.CommandPayloadDigest)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	current, err := backend.Domains.Domain(ctx, decoded.request.DomainUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if current.Present && !sameDomain(current, decoded.spec) {
		observation := makeObservation(decoded, current, lease.AttemptIndex, "CONFLICTING")
		return agentexecution.BackendResult{Outcome: "UNKNOWN", Result: observation.Evidence, Observation: observation}, nil
	}
	if !current.Present {
		if err := backend.Domains.Define(ctx, decoded.spec); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	return backend.observe(ctx, decoded, lease.AttemptIndex)
}

func (backend Backend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	decoded, err := backend.decode(ctx, verification.TargetResourceID, verification.CommandPayload, verification.CommandPayloadDigest)
	if err != nil {
		return contract.Observation{}, err
	}
	result, err := backend.observe(ctx, decoded, verification.AttemptIndex)
	return result.Observation, err
}

func (backend Backend) observe(ctx context.Context, decoded decoded, generation int) (agentexecution.BackendResult, error) {
	current, err := backend.Domains.Domain(ctx, decoded.request.DomainUUID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	state, outcome := "CONFLICTING", "UNKNOWN"
	if sameDomain(current, decoded.spec) {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	observation := makeObservation(decoded, current, generation, state)
	return agentexecution.BackendResult{Outcome: outcome, Result: observation.Evidence, Observation: observation}, nil
}

func (backend Backend) decode(ctx context.Context, target string, payload []byte, planDigest string) (decoded, error) {
	match := vmTargetPattern.FindStringSubmatch(target)
	if backend.Domains == nil || backend.Volumes == nil || match == nil || len(planDigest) != 64 {
		return decoded{}, errors.New("complete typed VM define authority is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return decoded{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return decoded{}, errors.New("trailing VM define payload")
	}
	if desired.DomainUUID != match[1] || desired.MaterializationGeneration == 0 || desired.VCPUs == 0 || desired.VCPUs > 1024 || desired.MemoryMiB < 16 || desired.MemoryMiB > 16*1024*1024 || desired.DesiredState != "DEFINED" || desired.ImageID == "" || desired.ImageRevision == 0 || desired.ImageMaterializationState != "PENDING" || desired.NetworkRealizationState != "PENDING" || desired.RootVolume.VolumeID == "" || desired.RootVolume.VGUUID == "" || desired.RootVolume.LVUUID == "" || desired.RootVolume.BackendResourceKey != locallvm.ResourceKey(desired.RootVolume.VolumeID) {
		return decoded{}, errors.New("invalid typed VM define plan")
	}
	volume, source, err := backend.Volumes.Resolve(ctx, desired.RootVolume.VGUUID, desired.RootVolume.BackendResourceKey, desired.RootVolume.LVUUID)
	if err != nil {
		return decoded{}, err
	}
	serial := volumeSerial(desired.RootVolume.VolumeID)
	spec := DomainSpec{Present: true, UUID: desired.DomainUUID, Name: "kim-" + desired.DomainUUID,
		PlanDigest: planDigest, MaterializationGeneration: desired.MaterializationGeneration,
		VCPUs: desired.VCPUs, MemoryMiB: desired.MemoryMiB, RootSource: source,
		RootLVUUID: volume.LVUUID, RootTarget: "vda", RootSerial: serial}
	return decoded{request: desired, spec: spec}, nil
}

func sameDomain(current, expected DomainObservation) bool {
	return current.Present && current.UUID == expected.UUID && current.Name == expected.Name &&
		current.PlanDigest == expected.PlanDigest && current.MaterializationGeneration == expected.MaterializationGeneration &&
		current.VCPUs == expected.VCPUs && current.MemoryMiB == expected.MemoryMiB &&
		current.RootSource == expected.RootSource && current.RootTarget == expected.RootTarget &&
		current.RootSerial == expected.RootSerial
}

func makeObservation(decoded decoded, current DomainObservation, generation int, state string) contract.Observation {
	evidence := map[string]any{
		"domain_uuid": decoded.request.DomainUUID, "materialization_generation": decoded.request.MaterializationGeneration,
		"plan_digest": decoded.spec.PlanDigest, "domain_present": current.Present,
		"domain_identity_matches":      current.Present && current.UUID == decoded.spec.UUID && current.Name == decoded.spec.Name,
		"plan_identity_matches":        current.Present && current.PlanDigest == decoded.spec.PlanDigest && current.MaterializationGeneration == decoded.spec.MaterializationGeneration,
		"compute_shape_matches":        current.Present && current.VCPUs == decoded.spec.VCPUs && current.MemoryMiB == decoded.spec.MemoryMiB,
		"root_volume_identity_matches": current.Present && current.RootSource == decoded.spec.RootSource && current.RootTarget == "vda" && current.RootSerial == decoded.spec.RootSerial,
		"image_materialization_state":  decoded.request.ImageMaterializationState,
		"network_realization_state":    decoded.request.NetworkRealizationState,
		"source":                       "libvirt_inactive_domain_xml+lvm_identity",
	}
	encoded, _ := json.Marshal(evidence)
	digest := sha256.Sum256(encoded)
	return contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}
}

func volumeSerial(volumeID string) string {
	digest := sha256.Sum256([]byte(volumeID))
	return "KIM-" + hex.EncodeToString(digest[:16])
}
