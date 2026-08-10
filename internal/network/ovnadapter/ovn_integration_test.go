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

func TestDisposableOVNNBSBChassisConvergence(t *testing.T) {
	if os.Getenv("KIM_OVN_QUALIFY") != "1" {
		t.Skip("OVN qualification is not configured")
	}
	const logicalSwitch = "kim-ovn-qualification-20260810"
	const logicalPort = "kim-ovn-port-20260810"
	const ovsInterface = "kimovnq0"
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ctx := context.Background()
	run := func(name string, args ...string) string {
		output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	defer func() {
		_ = exec.Command("ovs-vsctl", "--if-exists", "del-port", "br-int", ovsInterface).Run()
		_ = exec.Command("ovn-nbctl", "--if-exists", "lsp-del", logicalPort).Run()
		_ = exec.Command("ovn-nbctl", "--if-exists", "ls-del", logicalSwitch).Run()
	}()
	run("ovn-nbctl", "--may-exist", "ls-add", logicalSwitch)
	run("ovn-nbctl", "set", "Logical_Switch", logicalSwitch, "external_ids:kim.intent_id=qualification-intent", "external_ids:kim.intent_generation=1", "external_ids:kim.object_set_digest="+digest)
	run("ovn-nbctl", "--may-exist", "lsp-add", logicalSwitch, logicalPort)
	run("ovn-nbctl", "lsp-set-addresses", logicalPort, "02:00:00:99:00:20 192.0.2.250")
	run("ovn-nbctl", "set", "Logical_Switch_Port", logicalPort, "external_ids:kim.intent_id=qualification-intent", "external_ids:kim.intent_generation=1", "external_ids:kim.object_set_digest="+digest)
	run("ovs-vsctl", "--may-exist", "add-port", "br-int", ovsInterface, "--", "set", "Interface", ovsInterface, "type=internal", "external_ids:iface-id="+logicalPort)
	nbSwitch := run("ovn-nbctl", "--data=bare", "--no-heading", "--columns=external_ids", "find", "Logical_Switch", "name="+logicalSwitch)
	nbPort := run("ovn-nbctl", "--data=bare", "--no-heading", "--columns=external_ids", "find", "Logical_Switch_Port", "name="+logicalPort)
	if !strings.Contains(nbSwitch, "qualification-intent") || !strings.Contains(nbSwitch, digest) || !strings.Contains(nbPort, "qualification-intent") || !strings.Contains(nbPort, digest) {
		t.Fatalf("NB ownership/digest mismatch: switch=%q port=%q", nbSwitch, nbPort)
	}
	expectedChassis := run("ovs-vsctl", "--if-exists", "get", "Open_vSwitch", ".", "external_ids:system-id")
	expectedChassis = strings.Trim(expectedChassis, "\"")
	if expectedChassis == "" || expectedChassis == "[]" {
		t.Fatal("OVS system-id is not configured")
	}
	var sbPort, chassisUUID string
	for range 50 {
		sbPort = run("ovn-sbctl", "--data=bare", "--no-heading", "--columns=logical_port,datapath,chassis", "find", "Port_Binding", "logical_port="+logicalPort)
		chassisUUID = run("ovn-sbctl", "--data=bare", "--no-heading", "--columns=chassis", "find", "Port_Binding", "logical_port="+logicalPort)
		if sbPort != "" && chassisUUID != "" && chassisUUID != "[]" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sbPort == "" || chassisUUID == "" || chassisUUID == "[]" {
		t.Fatalf("SB Port_Binding did not converge to a datapath/chassis: %q", sbPort)
	}
	observedChassis := run("ovn-sbctl", "--if-exists", "get", "Chassis", chassisUUID, "name")
	observedChassis = strings.Trim(observedChassis, "\"")
	if observedChassis != expectedChassis {
		t.Fatalf("SB chassis=%q want Host system-id=%q", observedChassis, expectedChassis)
	}
	observation := Observation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true}
	if observation.NBState() != "MATCHED" || observation.SBState() != "MATCHED" {
		t.Fatalf("typed OVN observation=%s/%s", observation.NBState(), observation.SBState())
	}
}
