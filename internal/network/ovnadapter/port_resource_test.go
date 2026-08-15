package ovnadapter

import (
	"context"
	"errors"
	"testing"
)

func TestPortResourcePlanSeparatesLogicalIdentityFromBinding(t *testing.T) {
	raw, digest, err := PlanPortResource(PortResourceIntentInput{OperationID: "op", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", NetworkRevision: 3, PortID: "port", PortRevision: 2, RealizationGeneration: 5, MACAddress: "02:00:00:00:00:01", IPAddress: "192.0.2.10", DesiredState: "PRESENT"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, plan, err := RestoreStoredPortResourcePlan(raw, digest)
	if err != nil || string(canonical) != string(raw) || plan.ExpectedChassis != "" || plan.BindingGeneration != 0 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	attached := PortResourceIntentInput{OperationID: "op2", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", NetworkRevision: 3, PortID: "port", PortRevision: 2, RealizationGeneration: 6, MACAddress: "02:00:00:00:00:01", IPAddress: "192.0.2.10", ExpectedChassis: "chassis-b", BindingGeneration: 4, DesiredState: "PRESENT"}
	_, _, err = PlanPortResource(attached)
	if err != nil {
		t.Fatal(err)
	}
	attached.BindingGeneration = 0
	if _, _, err = PlanPortResource(attached); err == nil {
		t.Fatal("chassis without binding generation accepted")
	}
}

func TestPortResourceObservationRequiresExactReadBack(t *testing.T) {
	matched := PortResourceObservation{ObjectPresent: true, OwnershipMarkerMatches: true, PlanDigestMatches: true, NetworkMatches: true, MACMatches: true, IPMatches: true, BindingMatches: true}
	if matched.State("PRESENT") != "VERIFIED" {
		t.Fatal("exact observation not verified")
	}
	matched.MACMatches = false
	if matched.State("PRESENT") != "UNKNOWN" {
		t.Fatal("wrong MAC accepted")
	}
	if (PortResourceObservation{}).State("ABSENT") != "ABSENT" {
		t.Fatal("absence rejected")
	}
}

func TestPortResourceRuntimeDoesNotCreateParentAndConvergesAfterResponseLoss(t *testing.T) {
	raw, digest, err := PlanPortResource(PortResourceIntentInput{OperationID: "op", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", NetworkRevision: 1, PortID: "port", PortRevision: 1, RealizationGeneration: 1, MACAddress: "02:00:00:00:00:01", IPAddress: "192.0.2.10", DesiredState: "PRESENT"})
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := RestoreStoredPortResourcePlan(raw, digest)
	runner := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(p.NetworkExternalIDs, "")}, {output: emptyMarkerOutput()}, {err: errors.New("response lost")}, {output: markerOutput(p.NetworkExternalIDs, "")}, {output: markerOutput(p.PortExternalIDs, digest)}, {output: p.MACAddress + " " + p.IPAddress}, {output: ""}, {output: "lsp-uuid"}}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcilePortResource(context.Background(), raw, digest)
	if err != nil || result.ApplyResponseState != "LOST" || result.PortResourceObservation.State("PRESENT") != "VERIFIED" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !containsCommand(runner.calls[2], "lsp-add", p.LogicalSwitchName, p.LogicalPortName) || containsCommand(runner.calls[2], "ls-add") {
		t.Fatalf("Port mutation escaped resource boundary: %#v", runner.calls[2])
	}
}

func TestPortResourceAndMaterializationConsumersCanTransitionOnlySameOwnedPort(t *testing.T) {
	raw, digest, err := PlanPortResource(PortResourceIntentInput{OperationID: "op", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", NetworkRevision: 1, PortID: "port", PortRevision: 1, RealizationGeneration: 2, MACAddress: "02:00:00:00:00:01", IPAddress: "192.0.2.10", ExpectedChassis: "chassis-b", BindingGeneration: 1, DesiredState: "PRESENT"})
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := RestoreStoredPortResourcePlan(raw, digest)
	prior := map[string]string{"kim.owner": "KIM", "kim.aggregate_type": "PORT", "kim.port_id": p.PortID, "kim.network_id": p.NetworkID}
	runner := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(p.NetworkExternalIDs, "")}, {output: markerOutput(prior, "old-digest")}, {output: p.MACAddress + " " + p.IPAddress}, {output: "chassis-a"}, {output: "old-uuid"}, {}, {output: markerOutput(p.NetworkExternalIDs, "")}, {output: markerOutput(p.PortExternalIDs, digest)}, {output: p.MACAddress + " " + p.IPAddress}, {output: p.ExpectedChassis}, {output: "new-uuid"}}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcilePortResource(context.Background(), raw, digest)
	if err != nil || result.PortResourceObservation.State("PRESENT") != "VERIFIED" {
		t.Fatalf("transition=%+v err=%v", result, err)
	}
	foreign := prior
	foreign["kim.port_id"] = "other"
	blocked := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(p.NetworkExternalIDs, "")}, {output: markerOutput(foreign, "old")}, {output: p.MACAddress + " " + p.IPAddress}, {output: ""}, {output: "uuid"}}}
	if _, err = (Runtime{Config: testRuntimeConfig(), Runner: blocked}).ReconcilePortResource(context.Background(), raw, digest); !errors.Is(err, ErrForeignOVNObject) {
		t.Fatalf("foreign transition=%v", err)
	}
}
