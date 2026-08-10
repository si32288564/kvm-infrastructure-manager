//go:build ovn

package ovnadapter

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProductionShapeOVNRuntimeReconcile(t *testing.T) {
	if os.Getenv("KIM_OVN_RUNTIME_QUALIFY") != "1" {
		t.Skip("production-shape OVN runtime qualification is not configured")
	}
	ctx := context.Background()
	nbDatabase := environmentOr("KIM_OVN_NB_DATABASE", "unix:/var/run/ovn/ovnnb_db.sock")
	sbDatabase := environmentOr("KIM_OVN_SB_DATABASE", "unix:/var/run/ovn/ovnsb_db.sock")
	run := func(name string, args ...string) string {
		output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	chassis := strings.Trim(run("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:system-id"), "\"")
	if chassis == "" {
		t.Fatal("OVS system-id is absent")
	}
	inputs := []PortIntentInput{
		{IntentID: "runtime-intent-a", IntentGeneration: 1, ProjectID: "runtime-project", NetworkID: "runtime-network", NetworkGeneration: 1, PortID: "runtime-port-a", PortGeneration: 1, SegmentClaimID: "runtime-segment", SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, HostID: "runtime-host", OVNChassisName: chassis, MACAddress: "02:00:00:98:00:10", IPAddress: "198.18.98.10"},
		{IntentID: "runtime-intent-b", IntentGeneration: 1, ProjectID: "runtime-project", NetworkID: "runtime-network", NetworkGeneration: 1, PortID: "runtime-port-b", PortGeneration: 1, SegmentClaimID: "runtime-segment", SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, HostID: "runtime-host", OVNChassisName: chassis, MACAddress: "02:00:00:98:00:11", IPAddress: "198.18.98.11"},
	}
	plans := make([]PortPlan, 0, len(inputs))
	for _, input := range inputs {
		raw, digest, err := PlanPort(input)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := DecodePortPlan(raw, digest)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	if plans[0].LogicalSwitch.Name != plans[1].LogicalSwitch.Name {
		t.Fatal("same Network produced different Logical Switch identities")
	}
	defer func() {
		for index, plan := range plans {
			_ = exec.Command("ovs-vsctl", "--if-exists", "del-port", "kimrt"+string(rune('a'+index))).Run()
			_ = exec.Command("ovn-nbctl", "--db="+nbDatabase, "--if-exists", "lsp-del", plan.LogicalPort.Name).Run()
		}
		_ = exec.Command("ovn-nbctl", "--db="+nbDatabase, "--if-exists", "ls-del", plans[0].LogicalSwitch.Name).Run()
	}()
	for index, plan := range plans {
		interfaceName := "kimrt" + string(rune('a'+index))
		run("ovs-vsctl", "--may-exist", "add-port", "br-int", interfaceName, "--", "set", "Interface", interfaceName, "type=internal", "external_ids:iface-id="+plan.LogicalPort.Name)
	}
	runtime := Runtime{Config: RuntimeConfig{NBDatabase: nbDatabase, SBDatabase: sbDatabase, NBCTL: "/usr/bin/ovn-nbctl", SBCTL: "/usr/bin/ovn-sbctl", CommandTimeout: 10 * time.Second}}
	for _, input := range inputs {
		raw, digest, err := PlanPort(input)
		if err != nil {
			t.Fatal(err)
		}
		var result RuntimeResult
		for range 50 {
			result, err = runtime.ReconcilePort(ctx, raw, digest)
			if err == nil && result.Observation.NBState() == "MATCHED" && result.Observation.SBState() == "MATCHED" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil || result.Observation.NBState() != "MATCHED" || result.Observation.SBState() != "MATCHED" {
			t.Fatalf("runtime convergence for %s = %#v err=%v", input.PortID, result, err)
		}
	}
	switchMarkers := run("ovn-nbctl", "--db="+nbDatabase, "--data=bare", "--no-heading", "--columns=external_ids", "find", "Logical_Switch", "name="+plans[0].LogicalSwitch.Name)
	if !strings.Contains(switchMarkers, "runtime-network") || strings.Contains(switchMarkers, "runtime-intent-a") || strings.Contains(switchMarkers, "runtime-intent-b") {
		t.Fatalf("shared Logical Switch ownership markers=%q", switchMarkers)
	}
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
