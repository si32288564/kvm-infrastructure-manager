package ovnadapter

import (
	"context"
	"errors"
	"testing"
)

func TestSubnetRuntimeResponseLossConvergesFromTypedReadBack(t *testing.T) {
	raw, digest, plan := testSubnetPlan(t, "PRESENT", true)
	runner := &scriptedRunner{commands: []scriptedCommand{
		{output: markerOutput(plan.ExpectedNetworkIDs, "")}, {output: emptyMarkerOutput()},
		{err: errors.New("response lost")},
		{output: markerOutput(plan.ExpectedNetworkIDs, "")}, {output: markerOutput(plan.ExpectedExternalIDs, digest)},
		{output: "dhcp-uuid"}, {output: plan.CIDR}, {output: plan.GatewayAddress}, {output: "192.0.2.53"},
	}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcileSubnet(context.Background(), raw, digest)
	if err != nil || result.ApplyResponseState != "LOST" || result.SubnetObservation.State(plan) != "VERIFIED" || result.SubnetObservation.BackendUUID != "dhcp-uuid" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !containsCommand(runner.calls[2], "create", "DHCP_Options") {
		t.Fatalf("mutation is not closed DHCP create: %#v", runner.calls[2])
	}
}

func TestSubnetRuntimeRequiresExactParentNetworkAndRejectsForeignDHCP(t *testing.T) {
	raw, digest, plan := testSubnetPlan(t, "PRESENT", true)
	wrongParent := map[string]string{"kim.owner": "KIM", "kim.aggregate_type": "NETWORK", "kim.network_id": plan.NetworkID, "kim.network_generation": "99"}
	runner := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(wrongParent, "")}, {output: markerOutput(plan.ExpectedExternalIDs, digest)}, {output: "dhcp-uuid"}, {output: plan.CIDR}, {output: plan.GatewayAddress}, {output: "192.0.2.53"}}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ObserveSubnet(context.Background(), raw, digest)
	if err != nil || result.SubnetObservation.State(plan) != "UNKNOWN" {
		t.Fatalf("wrong parent result=%+v err=%v calls=%#v", result, err, runner.calls)
	}
	foreign := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(plan.ExpectedNetworkIDs, "")}, {output: markerOutput(map[string]string{"kim.owner": "FOREIGN", "kim.dhcp_object_name": plan.DHCPObjectName}, "")}, {output: "foreign"}, {output: plan.CIDR}, {output: plan.GatewayAddress}, {output: "192.0.2.53"}}}
	if _, err := (Runtime{Config: testRuntimeConfig(), Runner: foreign}).ReconcileSubnet(context.Background(), raw, digest); !errors.Is(err, ErrForeignOVNObject) {
		t.Fatalf("foreign DHCP err=%v", err)
	}
}

func TestSubnetPlanIsClosedAndCanonical(t *testing.T) {
	raw, digest, plan := testSubnetPlan(t, "PRESENT", true)
	canonical, restored, err := RestoreStoredSubnetPlan(raw, digest)
	if err != nil || string(canonical) != string(raw) || restored.DHCPObjectName != plan.DHCPObjectName {
		t.Fatalf("restore=%+v err=%v", restored, err)
	}
	invalid := SubnetIntentInput{OperationID: "o", OperationGeneration: 1, ProjectID: "p", NetworkID: "n", NetworkRevision: 1, SubnetID: "s", SubnetRevision: 1, RealizationGeneration: 1, CIDR: "192.0.2.0/24", DesiredState: "READY"}
	if _, _, err := PlanSubnet(invalid); err == nil {
		t.Fatal("open desired state accepted")
	}
}

func testSubnetPlan(t *testing.T, desired string, dhcp bool) ([]byte, string, SubnetPlan) {
	t.Helper()
	raw, digest, err := PlanSubnet(SubnetIntentInput{OperationID: "subnet-operation", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", NetworkRevision: 1, SubnetID: "subnet", SubnetRevision: 1, RealizationGeneration: 1, CIDR: "192.0.2.0/24", GatewayAddress: "192.0.2.1", DNSServiceAddresses: []string{"192.0.2.53"}, DHCPEnabled: dhcp, DesiredState: desired})
	if err != nil {
		t.Fatal(err)
	}
	_, plan, err := RestoreStoredSubnetPlan(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest, plan
}
