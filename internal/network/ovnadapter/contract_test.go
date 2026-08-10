package ovnadapter

import (
	"bytes"
	"testing"
)

func TestClosedTypedPortPlanAndLayerStates(t *testing.T) {
	input := PortIntentInput{IntentID: "intent-1", IntentGeneration: 1, ProjectID: "project", NetworkID: "network-1", NetworkGeneration: 1, PortID: "port-1", PortGeneration: 1, SegmentClaimID: "segment-1", SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, HostID: "host-1", MACAddress: "02:00:00:00:00:10", IPAddress: "192.0.2.10"}
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
