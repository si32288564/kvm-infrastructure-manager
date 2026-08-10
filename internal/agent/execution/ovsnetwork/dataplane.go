package ovsnetwork

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

const DataplaneCommandType = "NETWORK_PORT_OVS_DATAPLANE_OBSERVE"
const DataplaneSchemaVersion = "kim.command.network-port-ovs-dataplane-observe/v1"

type DataplaneObservation struct {
	DomainRunning    bool
	InterfacePresent bool
	BridgeMatches    bool
	TargetDevice     string
	Bridge           string
	LinkState        string
}

type DataplaneClient interface {
	Dataplane(context.Context, string, string, string) (DataplaneObservation, error)
}

type DataplaneBackend struct{ Client DataplaneClient }

func (DataplaneBackend) CommandType() string   { return DataplaneCommandType }
func (DataplaneBackend) SchemaVersion() string { return DataplaneSchemaVersion }

func (b DataplaneBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	request, err := b.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	observed, err := b.Client.Dataplane(ctx, request.DomainUUID, request.MACAddress, request.SegmentClaimID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	return dataplaneResult(request, observed, lease.AttemptIndex), nil
}

func (b DataplaneBackend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	request, err := b.decode(verification.TargetResourceID, verification.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	observed, err := b.Client.Dataplane(ctx, request.DomainUUID, request.MACAddress, request.SegmentClaimID)
	if err != nil {
		return contract.Observation{}, err
	}
	return dataplaneResult(request, observed, verification.AttemptIndex).Observation, nil
}

func (b DataplaneBackend) decode(target string, payload []byte) (request, error) {
	match := portPattern.FindStringSubmatch(target)
	if b.Client == nil || match == nil {
		return request{}, errors.New("complete typed OVS dataplane observation authority is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded request
	if err := decoder.Decode(&decoded); err != nil {
		return decoded, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return decoded, errors.New("trailing OVS dataplane payload")
	}
	if decoded.PortID != match[1] || !uuidPattern.MatchString(decoded.DomainUUID) || decoded.BindingType != "OVS" || decoded.DesiredState != "CONVERGED" || decoded.VMGeneration == 0 || decoded.PortGeneration == 0 || decoded.NetworkGeneration == 0 || decoded.SegmentGeneration == 0 || decoded.HostMappingGeneration == 0 || decoded.BindingGeneration == 0 || !macPattern.MatchString(decoded.MACAddress) {
		return decoded, errors.New("invalid typed OVS dataplane observation")
	}
	return decoded, nil
}

func dataplaneResult(request request, observed DataplaneObservation, generation int) agentexecution.BackendResult {
	matched := observed.DomainRunning && observed.InterfacePresent && observed.BridgeMatches && observed.TargetDevice != "" && observed.LinkState == "up"
	state, outcome := "DEGRADED", "UNKNOWN"
	if matched {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	evidence := map[string]any{
		"domain_uuid": request.DomainUUID, "vm_generation": request.VMGeneration,
		"port_id": request.PortID, "port_generation": request.PortGeneration,
		"network_id": request.NetworkID, "network_generation": request.NetworkGeneration,
		"segment_claim_id": request.SegmentClaimID, "segment_generation": request.SegmentGeneration,
		"host_mapping_generation": request.HostMappingGeneration, "binding_generation": request.BindingGeneration,
		"binding_type": "OVS", "mac_address": request.MACAddress,
		"domain_running": observed.DomainRunning, "interface_present": observed.InterfacePresent,
		"target_device": observed.TargetDevice, "bridge_observed": observed.Bridge,
		"bridge_matches": observed.BridgeMatches, "link_state": observed.LinkState,
		"source": "libvirt_active_xml+ovsdb_interface",
	}
	raw, _ := json.Marshal(evidence)
	digest := sha256.Sum256(raw)
	observation := contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}
	return agentexecution.BackendResult{Outcome: outcome, Result: evidence, Observation: observation}
}
