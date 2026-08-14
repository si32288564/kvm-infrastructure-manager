package ovnadapter

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkRuntimeResponseLossConvergesFromReadBack(t *testing.T) {
	raw, digest, plan := testNetworkPlan(t, 1, 1, "PRESENT")
	runner := &scriptedRunner{commands: []scriptedCommand{
		{output: emptyMarkerOutput()},
		{err: errors.New("response lost")},
		{output: markerOutput(plan.ExpectedExternalIDs, digest)},
		{output: "network-backend-uuid"},
	}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcileNetwork(context.Background(), raw, digest)
	if err != nil || result.ApplyResponseState != "LOST" || result.NetworkObservation.State("PRESENT") != "VERIFIED" || result.NetworkObservation.BackendUUID != "network-backend-uuid" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(runner.calls) != 4 || !containsCommand(runner.calls[1], "--may-exist", "ls-add", plan.LogicalSwitchName) {
		t.Fatalf("closed Network mutation calls=%#v", runner.calls)
	}
}

func TestNetworkRuntimeUpdatesOwnedIncarnationButRejectsForeignObject(t *testing.T) {
	_, oldDigest, oldPlan := testNetworkPlan(t, 1, 1, "PRESENT")
	raw, digest, plan := testNetworkPlan(t, 2, 2, "PRESENT")
	runner := &scriptedRunner{commands: []scriptedCommand{
		{output: markerOutput(oldPlan.ExpectedExternalIDs, oldDigest)}, {output: "old-backend"},
		{},
		{output: markerOutput(plan.ExpectedExternalIDs, digest)}, {output: "new-backend"},
	}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcileNetwork(context.Background(), raw, digest)
	if err != nil || result.NetworkObservation.State("PRESENT") != "VERIFIED" || result.NetworkObservation.BackendUUID != "new-backend" {
		t.Fatalf("owned update=%+v err=%v", result, err)
	}

	foreign := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(map[string]string{"kim.owner": "FOREIGN"}, "")}, {output: "foreign-backend"}}}
	if _, err := (Runtime{Config: testRuntimeConfig(), Runner: foreign}).ReconcileNetwork(context.Background(), raw, digest); !errors.Is(err, ErrForeignOVNObject) || len(foreign.calls) != 2 {
		t.Fatalf("foreign object err=%v calls=%d", err, len(foreign.calls))
	}
}

func TestNetworkRuntimeRetiresOnlyExactOwnedNetwork(t *testing.T) {
	_, oldDigest, oldPlan := testNetworkPlan(t, 2, 2, "PRESENT")
	raw, digest, plan := testNetworkPlan(t, 3, 3, "ABSENT")
	runner := &scriptedRunner{commands: []scriptedCommand{
		{output: markerOutput(oldPlan.ExpectedExternalIDs, oldDigest)}, {output: "old-backend"},
		{},
		{output: emptyMarkerOutput()},
	}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcileNetwork(context.Background(), raw, digest)
	if err != nil || result.NetworkObservation.State("ABSENT") != "ABSENT" || result.ApplyResponseState != "RECEIVED" {
		t.Fatalf("retirement=%+v err=%v", result, err)
	}
	if len(runner.calls) != 4 || !containsCommand(runner.calls[2], "--if-exists", "ls-del", plan.LogicalSwitchName) {
		t.Fatalf("retirement calls=%#v", runner.calls)
	}
}

func TestNetworkPlanRejectsOpenOrIncompleteIntent(t *testing.T) {
	valid := NetworkIntentInput{OperationID: "operation", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", AuthorityRevision: 1, BackendRevision: 1, AllocationID: "allocation", AllocationGeneration: 1, RealizationGeneration: 1, SegmentType: "VNI", SegmentID: 10000, DesiredState: "PRESENT"}
	invalid := []NetworkIntentInput{valid, valid, valid}
	invalid[0].SegmentType = "VXLAN"
	invalid[1].DesiredState = "READY"
	invalid[2].AllocationID = ""
	for _, input := range invalid {
		if _, _, err := PlanNetwork(input); err == nil {
			t.Fatalf("invalid intent accepted: %+v", input)
		}
	}
}

func testNetworkPlan(t *testing.T, revision, realization uint64, desired string) ([]byte, string, NetworkPlan) {
	t.Helper()
	raw, digest, err := PlanNetwork(NetworkIntentInput{OperationID: "network-operation", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", AuthorityRevision: revision, BackendRevision: revision, AllocationID: "network-allocation", AllocationGeneration: 1, RealizationGeneration: realization, SegmentType: "VNI", SegmentID: 10000, DesiredState: desired})
	if err != nil {
		t.Fatal(err)
	}
	_, plan, err := RestoreStoredNetworkPlan(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest, plan
}
