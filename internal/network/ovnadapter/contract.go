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
const PortBindingRetirementSchema = "kim.network-intent.ovn-port-binding-retirement/v1"

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

// PortBindingRetirementPlan preserves the logical Port and its KIM ownership
// markers while retiring one exact Host/chassis binding incarnation.
type PortBindingRetirementPlan struct {
	SchemaVersion              string            `json:"schema_version"`
	OperationID                string            `json:"operation_id"`
	OperationGeneration        uint64            `json:"operation_generation"`
	PortID                     string            `json:"port_id"`
	PortGeneration             uint64            `json:"port_generation"`
	BindingGeneration          uint64            `json:"binding_generation"`
	SourceHostID               string            `json:"source_host_id"`
	SourceOVNChassisName       string            `json:"source_ovn_chassis_name"`
	LogicalSwitchName          string            `json:"logical_switch_name"`
	LogicalPortName            string            `json:"logical_port_name"`
	ExpectedNetworkExternalIDs map[string]string `json:"expected_network_external_ids"`
	ExpectedPortExternalIDs    map[string]string `json:"expected_port_external_ids"`
	ExpectedObjectSetDigest    string            `json:"expected_object_set_digest"`
}

func PlanPortBindingRetirement(operationID string, operationGeneration uint64, source PortPlan, sourceObjectSetDigest string) ([]byte, string, error) {
	if operationID == "" || operationGeneration == 0 || len(sourceObjectSetDigest) != 64 {
		return nil, "", errors.New("complete typed OVN Port retirement intent is required")
	}
	if _, err := json.Marshal(source); err != nil {
		return nil, "", err
	}
	portGeneration, err := strconv.ParseUint(source.PortExternalIDs["kim.port_generation"], 10, 64)
	if err != nil || portGeneration == 0 {
		return nil, "", errors.New("source Port generation is required")
	}
	bindingGeneration, err := strconv.ParseUint(source.PortExternalIDs["kim.binding_generation"], 10, 64)
	if err != nil || bindingGeneration == 0 {
		return nil, "", errors.New("source binding generation is required")
	}
	plan := PortBindingRetirementPlan{SchemaVersion: PortBindingRetirementSchema, OperationID: operationID,
		OperationGeneration: operationGeneration, PortID: source.LogicalPort.PortID, PortGeneration: portGeneration,
		BindingGeneration: bindingGeneration, SourceHostID: source.LogicalPort.HostID,
		SourceOVNChassisName: source.LogicalPort.OVNChassisName, LogicalSwitchName: source.LogicalSwitch.Name,
		LogicalPortName: source.LogicalPort.Name, ExpectedNetworkExternalIDs: source.NetworkExternalIDs,
		ExpectedPortExternalIDs: source.PortExternalIDs, ExpectedObjectSetDigest: sourceObjectSetDigest}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	return raw, hex.EncodeToString(digest[:]), nil
}

func RestoreStoredPortBindingRetirementPlan(raw []byte, expectedDigest string) ([]byte, PortBindingRetirementPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan PortBindingRetirementPlan
	if err := decoder.Decode(&plan); err != nil {
		return nil, plan, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, plan, errors.New("OVN Port retirement plan contains trailing data")
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, plan, err
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != expectedDigest || plan.SchemaVersion != PortBindingRetirementSchema ||
		plan.OperationID == "" || plan.OperationGeneration == 0 || plan.PortID == "" || plan.PortGeneration == 0 ||
		plan.BindingGeneration == 0 || plan.SourceHostID == "" || plan.SourceOVNChassisName == "" ||
		plan.LogicalSwitchName == "" || plan.LogicalPortName == "" || len(plan.ExpectedObjectSetDigest) != 64 ||
		plan.ExpectedPortExternalIDs["kim.owner"] != "KIM" || plan.ExpectedPortExternalIDs["kim.port_id"] != plan.PortID {
		return nil, plan, errors.New("invalid OVN Port retirement plan")
	}
	return canonical, plan, nil
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
	return decodePortPlan(raw)
}

// RestoreStoredPortPlan reconstructs canonical wire bytes from PostgreSQL
// jsonb. PostgreSQL may change insignificant whitespace and object-key order,
// so authority is accepted only when strict typed decode followed by canonical
// marshal reproduces the immutable pre-storage digest.
func RestoreStoredPortPlan(raw []byte, expectedDigest string) ([]byte, PortPlan, error) {
	plan, err := decodePortPlan(raw)
	if err != nil {
		return nil, PortPlan{}, err
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, PortPlan{}, err
	}
	actual := sha256.Sum256(canonical)
	if len(expectedDigest) != 64 || hex.EncodeToString(actual[:]) != expectedDigest {
		return nil, PortPlan{}, errors.New("stored OVN Port plan digest mismatch")
	}
	return canonical, plan, nil
}

func decodePortPlan(raw []byte) (PortPlan, error) {
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

type PortBindingRetirementObservation struct {
	OwnershipMarkerMatches, ObjectSetDigestMatches bool
	LogicalSwitchPresent, LogicalSwitchPortPresent bool
	RequestedChassisAbsent, SourceChassisInactive  bool
	SourceOVSInterfaceAbsent                       bool
}

func (observation PortBindingRetirementObservation) State() string {
	if !observation.OwnershipMarkerMatches || !observation.ObjectSetDigestMatches || !observation.LogicalSwitchPresent || !observation.LogicalSwitchPortPresent {
		return "CONFLICTING"
	}
	if observation.RequestedChassisAbsent && observation.SourceChassisInactive && observation.SourceOVSInterfaceAbsent {
		return "VERIFIED"
	}
	return "UNKNOWN"
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
