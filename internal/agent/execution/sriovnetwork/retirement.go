package sriovnetwork

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

const RetirementCommandType = "PCI_VF_RETIRE"
const RetirementSchemaVersion = "kim.command.pci-vf-retire/v1"

type RetirementObservation struct {
	DomainRunning, HostDevicePresent, DriverBound, HolderPresent bool
	DeviceAddress, IOMMUGroup                                    string
}

type RetirementClient interface {
	RetirementState(context.Context, string, string, string) (RetirementObservation, error)
	DetachHostDevice(context.Context, string, string, string) error
}

type RetirementBackend struct{ Client RetirementClient }

func (RetirementBackend) CommandType() string   { return RetirementCommandType }
func (RetirementBackend) SchemaVersion() string { return RetirementSchemaVersion }

type retirementRequest struct {
	DomainUUID           string `json:"domain_uuid"`
	VMGeneration         uint64 `json:"vm_generation"`
	PortID               string `json:"port_id"`
	PortGeneration       uint64 `json:"port_generation"`
	BindingGeneration    uint64 `json:"binding_generation"`
	SourceHostID         string `json:"source_host_id"`
	DeviceAddress        string `json:"device_address"`
	VFClaimID            string `json:"vf_claim_id"`
	AllocationGeneration uint64 `json:"allocation_generation"`
	IOMMUGroup           string `json:"iommu_group"`
	OwnershipMarker      string `json:"ownership_marker"`
	OperationID          string `json:"operation_id"`
	OperationGeneration  uint64 `json:"operation_generation"`
	DesiredState         string `json:"desired_state"`
}

func (b RetirementBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	r, err := b.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	current, err := b.Client.RetirementState(ctx, r.DomainUUID, r.DeviceAddress, r.IOMMUGroup)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if current.DomainRunning {
		return retirementResult(r, current, lease.AttemptIndex), nil
	}
	if current.HostDevicePresent {
		if err := b.Client.DetachHostDevice(ctx, r.DomainUUID, r.DeviceAddress, r.IOMMUGroup); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	current, err = b.Client.RetirementState(ctx, r.DomainUUID, r.DeviceAddress, r.IOMMUGroup)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	return retirementResult(r, current, lease.AttemptIndex), nil
}
func (b RetirementBackend) Observe(ctx context.Context, v contract.VerificationRequest) (contract.Observation, error) {
	r, err := b.decode(v.TargetResourceID, v.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	current, err := b.Client.RetirementState(ctx, r.DomainUUID, r.DeviceAddress, r.IOMMUGroup)
	if err != nil {
		return contract.Observation{}, err
	}
	return retirementResult(r, current, v.AttemptIndex).Observation, nil
}
func (b RetirementBackend) decode(target string, payload []byte) (retirementRequest, error) {
	match := portPattern.FindStringSubmatch(target)
	if b.Client == nil || match == nil {
		return retirementRequest{}, errors.New("complete typed VF retirement authority is required")
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	var r retirementRequest
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return r, errors.New("trailing VF retirement payload")
	}
	if r.PortID != match[1] || !uuidPattern.MatchString(r.DomainUUID) || r.VMGeneration == 0 || r.PortGeneration == 0 || r.BindingGeneration == 0 || r.SourceHostID == "" || !pciPattern.MatchString(r.DeviceAddress) || r.VFClaimID == "" || r.AllocationGeneration == 0 || r.IOMMUGroup == "" || len(r.OwnershipMarker) != 64 || r.OperationID == "" || r.OperationGeneration == 0 || r.DesiredState != "RETIRED" {
		return r, errors.New("invalid typed VF retirement")
	}
	return r, nil
}
func retirementResult(r retirementRequest, o RetirementObservation, generation int) agentexecution.BackendResult {
	matched := !o.DomainRunning && !o.HostDevicePresent && !o.DriverBound && !o.HolderPresent && o.DeviceAddress == r.DeviceAddress && o.IOMMUGroup == r.IOMMUGroup
	state, outcome := "CONFLICTING", "UNKNOWN"
	if matched {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	e := map[string]any{"operation_id": r.OperationID, "operation_generation": r.OperationGeneration, "domain_uuid": r.DomainUUID, "vm_generation": r.VMGeneration, "port_id": r.PortID, "port_generation": r.PortGeneration, "binding_generation": r.BindingGeneration, "source_host_id": r.SourceHostID, "device_address": r.DeviceAddress, "vf_claim_id": r.VFClaimID, "allocation_generation": r.AllocationGeneration, "iommu_group": r.IOMMUGroup, "ownership_marker": r.OwnershipMarker, "source_domain_not_running": !o.DomainRunning, "source_hostdev_absent": !o.HostDevicePresent, "vf_driver_released": !o.DriverBound, "vf_holder_absent": !o.HolderPresent, "iommu_group_matches": o.IOMMUGroup == r.IOMMUGroup, "source": "libvirt_inactive_hostdev_and_linux_sysfs_read_back"}
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	observation := contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(sum[:]), Evidence: e}
	return agentexecution.BackendResult{Outcome: outcome, Result: e, Observation: observation}
}
