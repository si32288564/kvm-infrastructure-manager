// Package ovnadapter defines the closed typed contract between KIM Network
// authority and an isolated OVN controller adapter.
package ovnadapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
)

const PortIntentSchema = "kim.network-intent.ovn-port/v2"

type PortIntentInput struct {
	IntentID, ProjectID, NetworkID, PortID, SegmentClaimID, HostID string
	OVNChassisName                                                 string
	IntentGeneration, NetworkGeneration, PortGeneration            uint64
	SegmentGeneration, HostMappingGeneration, BindingGeneration    uint64
	MACAddress, IPAddress                                          string
}

type PortPlan struct {
	SchemaVersion      string            `json:"schema_version"`
	NetworkExternalIDs map[string]string `json:"network_external_ids"`
	PortExternalIDs    map[string]string `json:"port_external_ids"`
	LogicalSwitch      LogicalSwitchPlan `json:"logical_switch"`
	LogicalPort        LogicalPortPlan   `json:"logical_switch_port"`
}

type LogicalSwitchPlan struct {
	Name      string `json:"name"`
	NetworkID string `json:"network_id"`
	ProjectID string `json:"project_id"`
}

type LogicalPortPlan struct {
	Name           string `json:"name"`
	PortID         string `json:"port_id"`
	HostID         string `json:"host_id"`
	OVNChassisName string `json:"ovn_chassis_name"`
	MACAddress     string `json:"mac_address"`
	IPAddress      string `json:"ip_address"`
}

func PlanPort(input PortIntentInput) ([]byte, string, error) {
	_, macErr := net.ParseMAC(input.MACAddress)
	if input.IntentID == "" || input.ProjectID == "" || input.NetworkID == "" || input.PortID == "" || input.SegmentClaimID == "" || input.HostID == "" || input.OVNChassisName == "" || input.IntentGeneration == 0 || input.NetworkGeneration == 0 || input.PortGeneration == 0 || input.SegmentGeneration == 0 || input.HostMappingGeneration == 0 || input.BindingGeneration == 0 || macErr != nil || net.ParseIP(input.IPAddress) == nil {
		return nil, "", errors.New("complete typed OVN Port intent is required")
	}
	networkMarkers := map[string]string{
		"kim.owner": "KIM", "kim.aggregate_type": "NETWORK",
		"kim.project_id": input.ProjectID, "kim.network_id": input.NetworkID,
		"kim.network_generation": strconv.FormatUint(input.NetworkGeneration, 10),
	}
	portMarkers := map[string]string{
		"kim.owner": "KIM", "kim.aggregate_type": "PORT",
		"kim.intent_id": input.IntentID, "kim.intent_generation": strconv.FormatUint(input.IntentGeneration, 10),
		"kim.network_id": input.NetworkID, "kim.network_generation": strconv.FormatUint(input.NetworkGeneration, 10),
		"kim.port_id": input.PortID, "kim.port_generation": strconv.FormatUint(input.PortGeneration, 10),
		"kim.segment_claim_id": input.SegmentClaimID, "kim.segment_generation": strconv.FormatUint(input.SegmentGeneration, 10),
		"kim.host_mapping_generation": strconv.FormatUint(input.HostMappingGeneration, 10),
		"kim.binding_generation":      strconv.FormatUint(input.BindingGeneration, 10),
	}
	plan := PortPlan{
		SchemaVersion: PortIntentSchema, NetworkExternalIDs: networkMarkers, PortExternalIDs: portMarkers,
		LogicalSwitch: LogicalSwitchPlan{Name: objectName("kim-ls", input.NetworkID), NetworkID: input.NetworkID, ProjectID: input.ProjectID},
		LogicalPort: LogicalPortPlan{Name: objectName("kim-lsp", input.PortID), PortID: input.PortID, HostID: input.HostID,
			OVNChassisName: input.OVNChassisName, MACAddress: input.MACAddress, IPAddress: input.IPAddress},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	return raw, hex.EncodeToString(digest[:]), nil
}

func DecodePortPlan(raw []byte, expectedDigest string) (PortPlan, error) {
	actual := sha256.Sum256(raw)
	if len(expectedDigest) != 64 || hex.EncodeToString(actual[:]) != expectedDigest {
		return PortPlan{}, errors.New("OVN Port plan digest mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan PortPlan
	if err := decoder.Decode(&plan); err != nil {
		return PortPlan{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PortPlan{}, errors.New("OVN Port plan contains trailing data")
	}
	if plan.SchemaVersion != PortIntentSchema || plan.LogicalSwitch.Name == "" || plan.LogicalSwitch.NetworkID == "" ||
		plan.LogicalSwitch.ProjectID == "" || plan.LogicalPort.Name == "" || plan.LogicalPort.PortID == "" ||
		plan.LogicalPort.HostID == "" || plan.LogicalPort.OVNChassisName == "" || net.ParseIP(plan.LogicalPort.IPAddress) == nil {
		return PortPlan{}, errors.New("incomplete OVN Port plan")
	}
	if _, err := net.ParseMAC(plan.LogicalPort.MACAddress); err != nil {
		return PortPlan{}, errors.New("invalid OVN Port plan MAC")
	}
	if plan.LogicalSwitch.Name != objectName("kim-ls", plan.LogicalSwitch.NetworkID) || plan.LogicalPort.Name != objectName("kim-lsp", plan.LogicalPort.PortID) ||
		plan.NetworkExternalIDs["kim.owner"] != "KIM" || plan.NetworkExternalIDs["kim.aggregate_type"] != "NETWORK" ||
		plan.NetworkExternalIDs["kim.network_id"] != plan.LogicalSwitch.NetworkID || plan.PortExternalIDs["kim.owner"] != "KIM" ||
		plan.PortExternalIDs["kim.aggregate_type"] != "PORT" || plan.PortExternalIDs["kim.port_id"] != plan.LogicalPort.PortID {
		return PortPlan{}, errors.New("OVN Port plan ownership markers conflict")
	}
	return plan, nil
}

func objectName(prefix, identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return prefix + "-" + hex.EncodeToString(digest[:8])
}

type Observation struct {
	OwnershipMarkerMatches, ObjectSetDigestMatches bool
	LogicalSwitchPresent, LogicalSwitchPortPresent bool
	PortBindingPresent, DatapathPresent            bool
	ExpectedChassisMatches                         bool
}

type ControlPlaneObservation struct {
	LogicalDatapathPresent, ExpectedDatapathMatches     bool
	RequiredIngressFlowsPresent                         bool
	RequiredEgressFlowsPresent                          bool
	RequiredPortIdentityFlowsPresent                    bool
	ExpectedChassisMatches, ChassisRegistered           bool
	EncapPresent, EncapTypeAllowed, TunnelEndpointKnown bool
}

type TunnelObservation struct {
	SourceChassisMatches, DestinationChassisMatches bool
	SourceTunnelPresent, DestinationTunnelPresent   bool
	PacketsSent, PacketsReceived                    uint64
}

func (observation TunnelObservation) State() string {
	if !observation.SourceChassisMatches || !observation.DestinationChassisMatches {
		return "CONFLICTING"
	}
	if !observation.SourceTunnelPresent || !observation.DestinationTunnelPresent || observation.PacketsSent == 0 {
		return "UNKNOWN"
	}
	if observation.PacketsReceived == observation.PacketsSent {
		return "VERIFIED"
	}
	return "DEGRADED"
}

func (observation ControlPlaneObservation) LogicalFlowState() string {
	if !observation.ExpectedDatapathMatches {
		return "CONFLICTING"
	}
	if !observation.LogicalDatapathPresent {
		return "UNKNOWN"
	}
	if observation.RequiredIngressFlowsPresent && observation.RequiredEgressFlowsPresent && observation.RequiredPortIdentityFlowsPresent {
		return "MATCHED"
	}
	return "UNKNOWN"
}

func (observation ControlPlaneObservation) ChassisEncapState() string {
	if !observation.ExpectedChassisMatches {
		return "CONFLICTING"
	}
	if observation.ChassisRegistered && observation.EncapPresent && observation.EncapTypeAllowed && observation.TunnelEndpointKnown {
		return "MATCHED"
	}
	return "UNKNOWN"
}

func (observation Observation) NBState() string {
	if observation.OwnershipMarkerMatches && observation.ObjectSetDigestMatches && observation.LogicalSwitchPresent && observation.LogicalSwitchPortPresent {
		return "MATCHED"
	}
	if !observation.OwnershipMarkerMatches || !observation.ObjectSetDigestMatches {
		return "CONFLICTING"
	}
	return "UNKNOWN"
}

func (observation Observation) SBState() string {
	if observation.NBState() != "MATCHED" {
		return "UNKNOWN"
	}
	if observation.PortBindingPresent && observation.DatapathPresent && observation.ExpectedChassisMatches {
		return "MATCHED"
	}
	return "UNKNOWN"
}
