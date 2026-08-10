// Package sriovnetwork implements closed typed pre-boot SR-IOV Port realization.
package sriovnetwork

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
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const CommandType = "NETWORK_PORT_SRIOV_REALIZE"
const SchemaVersion = "kim.command.network-port-sriov-realize/v1"

var portPattern = regexp.MustCompile(`^port:([A-Za-z0-9][A-Za-z0-9._-]{0,254})$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var pciPattern = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
var macPattern = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

type Observation struct {
	Present, IdentityMatches bool
	DeviceAddress, MAC       string
}
type Client interface {
	HostDevice(context.Context, string, string, string) (Observation, error)
	AttachHostDevice(context.Context, string, Observation) error
}
type Backend struct{ Client Client }

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

type request struct {
	DomainUUID               string `json:"domain_uuid"`
	VMGeneration             uint64 `json:"vm_generation"`
	PortID                   string `json:"port_id"`
	PortGeneration           uint64 `json:"port_generation"`
	NetworkID                string `json:"network_id"`
	NetworkGeneration        uint64 `json:"network_generation"`
	SegmentClaimID           string `json:"segment_claim_id"`
	SegmentGeneration        uint64 `json:"segment_generation"`
	HostMappingGeneration    uint64 `json:"host_mapping_generation"`
	BindingGeneration        uint64 `json:"binding_generation"`
	MACAddress               string `json:"mac_address"`
	DeviceAddress            string `json:"device_address"`
	VFClaimID                string `json:"vf_claim_id"`
	PCIObservationGeneration uint64 `json:"pci_observation_generation"`
	PCIObservationDigest     string `json:"pci_observation_digest"`
	QualificationID          string `json:"qualification_id"`
	QualificationRevision    uint64 `json:"qualification_revision"`
	PolicyID                 string `json:"policy_id"`
	PolicyGeneration         uint64 `json:"policy_generation"`
	BindingType              string `json:"binding_type"`
	DesiredState             string `json:"desired_state"`
}

func (b Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	r, err := b.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	current, err := b.Client.HostDevice(ctx, r.DomainUUID, r.DeviceAddress, r.MACAddress)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	expected := Observation{Present: true, IdentityMatches: true, DeviceAddress: r.DeviceAddress, MAC: r.MACAddress}
	if current.Present && !same(current, expected) {
		return result(r, current, lease.AttemptIndex), nil
	}
	if !current.Present {
		if err := b.Client.AttachHostDevice(ctx, r.DomainUUID, expected); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	current, err = b.Client.HostDevice(ctx, r.DomainUUID, r.DeviceAddress, r.MACAddress)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	return result(r, current, lease.AttemptIndex), nil
}
func (b Backend) Observe(ctx context.Context, v contract.VerificationRequest) (contract.Observation, error) {
	r, err := b.decode(v.TargetResourceID, v.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	current, err := b.Client.HostDevice(ctx, r.DomainUUID, r.DeviceAddress, r.MACAddress)
	if err != nil {
		return contract.Observation{}, err
	}
	return result(r, current, v.AttemptIndex).Observation, nil
}
func (b Backend) decode(target string, payload []byte) (request, error) {
	match := portPattern.FindStringSubmatch(target)
	if b.Client == nil || match == nil {
		return request{}, errors.New("complete typed SR-IOV Port authority is required")
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	var r request
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return r, errors.New("trailing SR-IOV Port payload")
	}
	if r.PortID != match[1] || !uuidPattern.MatchString(r.DomainUUID) || r.VMGeneration == 0 || r.PortGeneration == 0 || r.NetworkID == "" || r.NetworkGeneration == 0 || r.SegmentClaimID == "" || r.SegmentGeneration == 0 || r.HostMappingGeneration == 0 || r.BindingGeneration == 0 || !macPattern.MatchString(r.MACAddress) || !pciPattern.MatchString(r.DeviceAddress) || r.VFClaimID == "" || r.PCIObservationGeneration == 0 || len(r.PCIObservationDigest) != 64 || r.QualificationID == "" || r.QualificationRevision == 0 || r.PolicyID == "" || r.PolicyGeneration == 0 || r.BindingType != "SRIOV_DIRECT" || r.DesiredState != "REALIZED" {
		return r, errors.New("invalid typed SR-IOV Port realization")
	}
	return r, nil
}
func result(r request, current Observation, generation int) agentexecution.BackendResult {
	matched := same(current, Observation{Present: true, IdentityMatches: true, DeviceAddress: r.DeviceAddress, MAC: r.MACAddress})
	state, outcome := "CONFLICTING", "UNKNOWN"
	if matched {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	e := map[string]any{"domain_uuid": r.DomainUUID, "vm_generation": r.VMGeneration, "port_id": r.PortID, "port_generation": r.PortGeneration, "network_id": r.NetworkID, "network_generation": r.NetworkGeneration, "segment_claim_id": r.SegmentClaimID, "segment_generation": r.SegmentGeneration, "host_mapping_generation": r.HostMappingGeneration, "binding_generation": r.BindingGeneration, "binding_type": r.BindingType, "mac_address": r.MACAddress, "device_address": r.DeviceAddress, "vf_claim_id": r.VFClaimID, "pci_observation_generation": r.PCIObservationGeneration, "pci_observation_digest": r.PCIObservationDigest, "qualification_id": r.QualificationID, "qualification_revision": r.QualificationRevision, "policy_id": r.PolicyID, "policy_generation": r.PolicyGeneration, "domain_hostdev_present": current.Present, "domain_hostdev_identity_matches": matched, "source": "libvirt_inactive_domain_hostdev_xml"}
	encoded, _ := json.Marshal(e)
	sum := sha256.Sum256(encoded)
	o := contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(sum[:]), Evidence: e}
	return agentexecution.BackendResult{Outcome: outcome, Result: e, Observation: o}
}
func same(a, b Observation) bool {
	return a.Present && a.IdentityMatches && a.DeviceAddress == b.DeviceAddress && a.MAC == b.MAC
}
