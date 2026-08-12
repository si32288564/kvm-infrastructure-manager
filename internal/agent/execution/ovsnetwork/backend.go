// Package ovsnetwork implements closed typed pre-boot OVS Port realization.
package ovsnetwork

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

const (
	CommandType   = "NETWORK_PORT_OVS_REALIZE"
	SchemaVersion = "kim.command.network-port-ovs-realize/v1"
	StateRealized = "REALIZED"
)

var portPattern = regexp.MustCompile(`^port:([A-Za-z0-9][A-Za-z0-9._-]{0,254})$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var macPattern = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

type NICObservation struct {
	Present, IdentityMatches   bool
	Bridge, MAC, Model, PortID string
}

type Client interface {
	Bridge(context.Context, string) (string, bool, error)
	NIC(context.Context, string, string) (NICObservation, error)
	AttachNIC(context.Context, string, NICObservation) error
}

type Backend struct{ Client Client }

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

type request struct {
	DomainUUID            string `json:"domain_uuid"`
	VMGeneration          uint64 `json:"vm_generation"`
	PortID                string `json:"port_id"`
	PortGeneration        uint64 `json:"port_generation"`
	NetworkID             string `json:"network_id"`
	NetworkGeneration     uint64 `json:"network_generation"`
	SegmentClaimID        string `json:"segment_claim_id"`
	SegmentGeneration     uint64 `json:"segment_generation"`
	HostMappingGeneration uint64 `json:"host_mapping_generation"`
	BindingGeneration     uint64 `json:"binding_generation"`
	MACAddress            string `json:"mac_address"`
	MTU                   uint32 `json:"mtu"`
	BindingType           string `json:"binding_type"`
	DesiredState          string `json:"desired_state"`
}

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	desired, err := backend.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	bridge, current, err := backend.read(ctx, desired)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	expected := NICObservation{Present: true, IdentityMatches: true, Bridge: bridge, MAC: desired.MACAddress, Model: "virtio", PortID: desired.PortID}
	if current.Present && !sameNIC(current, expected) {
		return makeResult(desired, bridge, current, lease.AttemptIndex), nil
	}
	if !current.Present {
		if err := backend.Client.AttachNIC(ctx, desired.DomainUUID, expected); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	bridge, current, err = backend.read(ctx, desired)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	return makeResult(desired, bridge, current, lease.AttemptIndex), nil
}

func (backend Backend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	desired, err := backend.decode(verification.TargetResourceID, verification.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	bridge, current, err := backend.read(ctx, desired)
	if err != nil {
		return contract.Observation{}, err
	}
	return makeResult(desired, bridge, current, verification.AttemptIndex).Observation, nil
}

func (backend Backend) read(ctx context.Context, desired request) (string, NICObservation, error) {
	bridge, exists, err := backend.Client.Bridge(ctx, desired.SegmentClaimID)
	if err != nil || !exists {
		return "", NICObservation{}, errors.New("configured OVS bridge is not observable")
	}
	current, err := backend.Client.NIC(ctx, desired.DomainUUID, desired.MACAddress)
	return bridge, current, err
}

func makeResult(desired request, bridge string, current NICObservation, generation int) agentexecution.BackendResult {
	matched := sameNIC(current, NICObservation{Present: true, IdentityMatches: true, Bridge: bridge, MAC: desired.MACAddress, Model: "virtio", PortID: desired.PortID})
	state, outcome := "CONFLICTING", "UNKNOWN"
	if matched {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	evidence := map[string]any{
		"domain_uuid": desired.DomainUUID, "vm_generation": desired.VMGeneration,
		"port_id": desired.PortID, "port_generation": desired.PortGeneration,
		"network_id": desired.NetworkID, "network_generation": desired.NetworkGeneration,
		"segment_claim_id": desired.SegmentClaimID, "segment_generation": desired.SegmentGeneration,
		"host_mapping_generation": desired.HostMappingGeneration, "binding_generation": desired.BindingGeneration,
		"binding_type": desired.BindingType, "mac_address": desired.MACAddress, "mtu": desired.MTU,
		"bridge_observed": bridge != "", "domain_nic_present": current.Present, "interface_id": current.PortID,
		"domain_nic_identity_matches": matched, "source": "ovs_bridge+libvirt_inactive_domain_xml",
	}
	encoded, _ := json.Marshal(evidence)
	digest := sha256.Sum256(encoded)
	observation := contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}
	return agentexecution.BackendResult{Outcome: outcome, Result: evidence, Observation: observation}
}

func (backend Backend) decode(target string, payload []byte) (request, error) {
	match := portPattern.FindStringSubmatch(target)
	if backend.Client == nil || match == nil {
		return request{}, errors.New("complete typed OVS Port authority is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return desired, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return desired, errors.New("trailing OVS Port payload")
	}
	if desired.PortID != match[1] || !uuidPattern.MatchString(desired.DomainUUID) || desired.VMGeneration == 0 || desired.PortGeneration == 0 || desired.NetworkID == "" || desired.NetworkGeneration == 0 || desired.SegmentClaimID == "" || desired.SegmentGeneration == 0 || desired.HostMappingGeneration == 0 || desired.BindingGeneration == 0 || !macPattern.MatchString(desired.MACAddress) || desired.MTU < 576 || desired.MTU > 9216 || desired.BindingType != "OVS" || desired.DesiredState != StateRealized {
		return desired, errors.New("invalid typed OVS Port realization")
	}
	return desired, nil
}
func sameNIC(a, b NICObservation) bool {
	return a.Present && a.IdentityMatches && a.Bridge == b.Bridge && a.MAC == b.MAC && a.Model == b.Model && a.PortID == b.PortID
}
