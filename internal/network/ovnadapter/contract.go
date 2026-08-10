// Package ovnadapter defines the closed typed contract between KIM Network
// authority and an isolated OVN controller adapter.
package ovnadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
)

const PortIntentSchema = "kim.network-intent.ovn-port/v1"

type PortIntentInput struct {
	IntentID, ProjectID, NetworkID, PortID, SegmentClaimID, HostID string
	IntentGeneration, NetworkGeneration, PortGeneration            uint64
	SegmentGeneration, HostMappingGeneration, BindingGeneration    uint64
	MACAddress, IPAddress                                          string
}

type PortPlan struct {
	SchemaVersion string         `json:"schema_version"`
	ExternalIDs   map[string]any `json:"external_ids"`
	LogicalSwitch map[string]any `json:"logical_switch"`
	LogicalPort   map[string]any `json:"logical_switch_port"`
}

func PlanPort(input PortIntentInput) ([]byte, string, error) {
	_, macErr := net.ParseMAC(input.MACAddress)
	if input.IntentID == "" || input.ProjectID == "" || input.NetworkID == "" || input.PortID == "" || input.SegmentClaimID == "" || input.HostID == "" || input.IntentGeneration == 0 || input.NetworkGeneration == 0 || input.PortGeneration == 0 || input.SegmentGeneration == 0 || input.HostMappingGeneration == 0 || input.BindingGeneration == 0 || macErr != nil || net.ParseIP(input.IPAddress) == nil {
		return nil, "", errors.New("complete typed OVN Port intent is required")
	}
	markers := map[string]any{
		"kim.intent_id": input.IntentID, "kim.intent_generation": input.IntentGeneration,
		"kim.network_id": input.NetworkID, "kim.network_generation": input.NetworkGeneration,
		"kim.port_id": input.PortID, "kim.port_generation": input.PortGeneration,
		"kim.segment_claim_id": input.SegmentClaimID, "kim.segment_generation": input.SegmentGeneration,
		"kim.host_mapping_generation": input.HostMappingGeneration,
		"kim.binding_generation":      input.BindingGeneration,
	}
	plan := PortPlan{
		SchemaVersion: PortIntentSchema,
		ExternalIDs:   markers,
		LogicalSwitch: map[string]any{"network_id": input.NetworkID, "project_id": input.ProjectID},
		LogicalPort:   map[string]any{"port_id": input.PortID, "host_id": input.HostID, "mac_address": input.MACAddress, "ip_address": input.IPAddress},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	return raw, hex.EncodeToString(digest[:]), nil
}

type Observation struct {
	OwnershipMarkerMatches, ObjectSetDigestMatches bool
	LogicalSwitchPresent, LogicalSwitchPortPresent bool
	PortBindingPresent, DatapathPresent            bool
	ExpectedChassisMatches                         bool
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
