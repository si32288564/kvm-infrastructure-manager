package ovnadapter

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

type scriptedCommand struct {
	output string
	err    error
}

type scriptedRunner struct {
	commands []scriptedCommand
	calls    [][]string
}

func (runner *scriptedRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string{name}, args...))
	if len(runner.commands) == 0 {
		return nil, errors.New("unexpected OVN command")
	}
	command := runner.commands[0]
	runner.commands = runner.commands[1:]
	return []byte(command.output), command.err
}

func TestRuntimeReconcilesTypedPortAndReadsBackLayers(t *testing.T) {
	raw, digest, plan := runtimePlan(t)
	networkMarkers := markerOutput(plan.NetworkExternalIDs, "")
	portMarkers := markerOutput(plan.PortExternalIDs, digest)
	runner := &scriptedRunner{commands: []scriptedCommand{
		{output: emptyMarkerOutput()}, {output: emptyMarkerOutput()}, {}, {output: networkMarkers}, {output: portMarkers},
		{output: "datapath-uuid"}, {output: "chassis-uuid"}, {output: `"chassis-1"`},
	}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcilePort(context.Background(), raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	if result.ApplyResponseState != "RECEIVED" || result.Observation.NBState() != "MATCHED" || result.Observation.SBState() != "MATCHED" || len(result.NBObservationDigest) != 64 || len(result.SBObservationDigest) != 64 || len(result.ChassisIdentityDigest) != 64 {
		t.Fatalf("runtime result=%#v", result)
	}
	if len(runner.calls) != 8 || !containsCommand(runner.calls[2], "--may-exist", "ls-add", plan.LogicalSwitch.Name) ||
		!containsCommand(runner.calls[2], "--may-exist", "lsp-add", plan.LogicalSwitch.Name, plan.LogicalPort.Name) ||
		!containsCommand(runner.calls[2], "options:requested-chassis=chassis-1") {
		t.Fatalf("closed apply command=%#v", runner.calls)
	}
}

func TestRuntimeReadsBackAfterApplyResponseLoss(t *testing.T) {
	raw, digest, plan := runtimePlan(t)
	runner := &scriptedRunner{commands: []scriptedCommand{
		{output: emptyMarkerOutput()}, {output: emptyMarkerOutput()}, {err: errors.New("response lost")},
		{output: markerOutput(plan.NetworkExternalIDs, "")}, {output: markerOutput(plan.PortExternalIDs, digest)},
		{output: "datapath-uuid"}, {output: "chassis-uuid"}, {output: `"chassis-1"`},
	}}
	result, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcilePort(context.Background(), raw, digest)
	if err != nil || result.ApplyResponseState != "LOST" || result.Observation.NBState() != "MATCHED" || result.Observation.SBState() != "MATCHED" {
		t.Fatalf("response-loss result=%#v err=%v", result, err)
	}
}

func TestRuntimeDoesNotOverwriteForeignSharedObject(t *testing.T) {
	raw, digest, _ := runtimePlan(t)
	runner := &scriptedRunner{commands: []scriptedCommand{{output: markerOutput(map[string]string{"kim.owner": "FOREIGN"}, "")}}}
	_, err := (Runtime{Config: testRuntimeConfig(), Runner: runner}).ReconcilePort(context.Background(), raw, digest)
	if !errors.Is(err, ErrForeignOVNObject) || len(runner.calls) != 1 {
		t.Fatalf("foreign object error/calls=%v/%d", err, len(runner.calls))
	}
}

func TestRuntimeRejectsUntrustedTransportAndExecutable(t *testing.T) {
	raw, digest, _ := runtimePlan(t)
	for _, config := range []RuntimeConfig{
		{NBDatabase: "tcp:127.0.0.1:6641", SBDatabase: "unix:/run/ovn/sb.sock", NBCTL: "ovn-nbctl", SBCTL: "ovn-sbctl", CommandTimeout: time.Second},
		{NBDatabase: "unix:/run/ovn/nb.sock", SBDatabase: "unix:/run/ovn/sb.sock", NBCTL: "ovn-nbctl", SBCTL: "ovn-sbctl", CommandTimeout: time.Second},
		{NBDatabase: "unix:/run/ovn/nb.sock", SBDatabase: "unix:/run/ovn/sb.sock", NBCTL: "sh", SBCTL: "ovn-sbctl", CommandTimeout: time.Second},
		{NBDatabase: "ssl:127.0.0.1:6641", SBDatabase: "ssl:127.0.0.1:6642", NBCTL: "ovn-nbctl", SBCTL: "ovn-sbctl", CommandTimeout: time.Second},
	} {
		runner := &scriptedRunner{}
		if _, err := (Runtime{Config: config, Runner: runner}).ReconcilePort(context.Background(), raw, digest); err == nil || len(runner.calls) != 0 {
			t.Fatalf("unsafe runtime config accepted: %#v err=%v", config, err)
		}
	}
}

func runtimePlan(t *testing.T) ([]byte, string, PortPlan) {
	t.Helper()
	raw, digest, err := PlanPort(PortIntentInput{IntentID: "intent-1", IntentGeneration: 1, ProjectID: "project", NetworkID: "network-1", NetworkGeneration: 1, PortID: "port-1", PortGeneration: 1, SegmentClaimID: "segment-1", SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, HostID: "host-1", OVNChassisName: "chassis-1", MACAddress: "02:00:00:00:00:10", IPAddress: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodePortPlan(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest, plan
}

func testRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{NBDatabase: "unix:/run/ovn/ovnnb_db.sock", SBDatabase: "unix:/run/ovn/ovnsb_db.sock", NBCTL: "/usr/bin/ovn-nbctl", SBCTL: "/usr/bin/ovn-sbctl", CommandTimeout: 10 * time.Second}
}

func markerOutput(markers map[string]string, digest string) string {
	keys := make([]string, 0, len(markers)+1)
	for key := range markers {
		keys = append(keys, key)
	}
	if digest != "" {
		markers = cloneMarkers(markers)
		markers["kim.object_set_digest"] = digest
		keys = append(keys, "kim.object_set_digest")
	}
	sort.Strings(keys)
	pairs := make([][]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, []string{key, markers[key]})
	}
	raw, _ := json.Marshal(map[string]any{"data": []any{[]any{[]any{"map", pairs}}}, "headings": []string{"external_ids"}})
	return string(raw)
}

func emptyMarkerOutput() string {
	return `{"data":[],"headings":["external_ids"]}`
}

func cloneMarkers(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source)+1)
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func containsCommand(command []string, expected ...string) bool {
	joined := strings.Join(command, "\x00")
	return strings.Contains(joined, strings.Join(expected, "\x00"))
}
