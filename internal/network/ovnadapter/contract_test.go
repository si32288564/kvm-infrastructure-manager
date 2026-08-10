package ovnadapter

import (
	"bytes"
	"maps"
	"testing"
)

func TestClosedTypedPortPlanAndLayerStates(t *testing.T) {
	input := PortIntentInput{IntentID: "intent-1", IntentGeneration: 1, ProjectID: "project", NetworkID: "network-1", NetworkGeneration: 1, PortID: "port-1", PortGeneration: 1, SegmentClaimID: "segment-1", SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, HostID: "host-1", OVNChassisName: "chassis-1", MACAddress: "02:00:00:00:00:10", IPAddress: "192.0.2.10"}
	first, firstDigest, err := PlanPort(input)
	if err != nil || len(firstDigest) != 64 {
		t.Fatalf("plan=%s digest=%s err=%v", first, firstDigest, err)
	}
	second, secondDigest, err := PlanPort(input)
	if err != nil || secondDigest != firstDigest || !bytes.Equal(second, first) {
		t.Fatalf("non-deterministic plan=%s/%s digest=%s/%s err=%v", first, second, firstDigest, secondDigest, err)
	}
	if bytes.Contains(first, []byte("ovn-nbctl")) || bytes.Contains(first, []byte("argv")) || bytes.Contains(first, []byte("raw_column")) {
		t.Fatalf("typed plan exposed arbitrary OVN control: %s", first)
	}
	firstPlan, err := DecodePortPlan(first, firstDigest)
	if err != nil {
		t.Fatal(err)
	}
	secondPort := input
	secondPort.IntentID, secondPort.PortID, secondPort.MACAddress, secondPort.IPAddress = "intent-2", "port-2", "02:00:00:00:00:11", "192.0.2.11"
	secondPortRaw, secondPortDigest, err := PlanPort(secondPort)
	if err != nil {
		t.Fatal(err)
	}
	secondPortPlan, err := DecodePortPlan(secondPortRaw, secondPortDigest)
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.LogicalSwitch != secondPortPlan.LogicalSwitch || !maps.Equal(firstPlan.NetworkExternalIDs, secondPortPlan.NetworkExternalIDs) || firstPlan.LogicalPort.Name == secondPortPlan.LogicalPort.Name || maps.Equal(firstPlan.PortExternalIDs, secondPortPlan.PortExternalIDs) {
		t.Fatalf("shared Network/object-specific Port ownership is not separated: first=%#v second=%#v", firstPlan, secondPortPlan)
	}
	matched := Observation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true}
	if matched.NBState() != "MATCHED" || matched.SBState() != "MATCHED" {
		t.Fatalf("matched layers=%s/%s", matched.NBState(), matched.SBState())
	}
	foreign := matched
	foreign.OwnershipMarkerMatches = false
	if foreign.NBState() != "CONFLICTING" || foreign.SBState() != "UNKNOWN" {
		t.Fatalf("foreign layers=%s/%s", foreign.NBState(), foreign.SBState())
	}
}

func TestLogicalFlowAndChassisEncapStates(t *testing.T) {
	matched := ControlPlaneObservation{
		LogicalDatapathPresent: true, ExpectedDatapathMatches: true,
		RequiredIngressFlowsPresent: true, RequiredEgressFlowsPresent: true,
		RequiredPortIdentityFlowsPresent: true,
		ExpectedChassisMatches:           true, ChassisRegistered: true, EncapPresent: true,
		EncapTypeAllowed: true, TunnelEndpointKnown: true,
	}
	if matched.LogicalFlowState() != "MATCHED" || matched.ChassisEncapState() != "MATCHED" {
		t.Fatalf("matched control-plane=%s/%s", matched.LogicalFlowState(), matched.ChassisEncapState())
	}
	missingFlow := matched
	missingFlow.RequiredEgressFlowsPresent = false
	if missingFlow.LogicalFlowState() != "UNKNOWN" {
		t.Fatalf("missing egress flow state=%s", missingFlow.LogicalFlowState())
	}
	foreignDatapath := matched
	foreignDatapath.ExpectedDatapathMatches = false
	if foreignDatapath.LogicalFlowState() != "CONFLICTING" {
		t.Fatalf("foreign datapath state=%s", foreignDatapath.LogicalFlowState())
	}
	foreignChassis := matched
	foreignChassis.ExpectedChassisMatches = false
	if foreignChassis.ChassisEncapState() != "CONFLICTING" {
		t.Fatalf("foreign chassis state=%s", foreignChassis.ChassisEncapState())
	}
}

func TestTunnelObservationState(t *testing.T) {
	verified := TunnelObservation{SourceChassisMatches: true, DestinationChassisMatches: true, SourceTunnelPresent: true, DestinationTunnelPresent: true, PacketsSent: 3, PacketsReceived: 3}
	if verified.State() != "VERIFIED" {
		t.Fatalf("verified tunnel state=%s", verified.State())
	}
	degraded := verified
	degraded.PacketsReceived = 2
	if degraded.State() != "DEGRADED" {
		t.Fatalf("degraded tunnel state=%s", degraded.State())
	}
	foreign := verified
	foreign.DestinationChassisMatches = false
	if foreign.State() != "CONFLICTING" {
		t.Fatalf("foreign tunnel state=%s", foreign.State())
	}
}
