//go:build geneve

package ovnadapter

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestIsolatedGenevePacketPath proves that the verifier can observe packets
// crossing a real kernel Geneve path between two isolated endpoints. Both
// endpoints intentionally run on one physical Host; this is not cross-Host or
// OVN-created-tunnel qualification.
func TestIsolatedGenevePacketPath(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Geneve network namespace fixture requires root")
	}
	for _, binary := range []string{"ip", "ping"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is unavailable: %v", binary, err)
		}
	}
	const sourceNS, destinationNS = "kimgva", "kimgvb"
	cleanup := func() {
		_ = exec.Command("ip", "netns", "del", sourceNS).Run()
		_ = exec.Command("ip", "netns", "del", destinationNS).Run()
	}
	cleanup()
	t.Cleanup(cleanup)

	commands := [][]string{
		{"ip", "netns", "add", sourceNS},
		{"ip", "netns", "add", destinationNS},
		{"ip", "link", "add", "kva-u", "type", "veth", "peer", "name", "kvb-u"},
		{"ip", "link", "set", "kva-u", "netns", sourceNS},
		{"ip", "link", "set", "kvb-u", "netns", destinationNS},
		{"ip", "-n", sourceNS, "addr", "add", "10.254.42.1/30", "dev", "kva-u"},
		{"ip", "-n", destinationNS, "addr", "add", "10.254.42.2/30", "dev", "kvb-u"},
		{"ip", "-n", sourceNS, "link", "set", "lo", "up"},
		{"ip", "-n", destinationNS, "link", "set", "lo", "up"},
		{"ip", "-n", sourceNS, "link", "set", "kva-u", "up"},
		{"ip", "-n", destinationNS, "link", "set", "kvb-u", "up"},
		{"ip", "-n", sourceNS, "link", "add", "gnv0", "type", "geneve", "id", "4242", "remote", "10.254.42.2", "dstport", "6081"},
		{"ip", "-n", destinationNS, "link", "add", "gnv0", "type", "geneve", "id", "4242", "remote", "10.254.42.1", "dstport", "6081"},
		{"ip", "-n", sourceNS, "addr", "add", "192.0.2.1/30", "dev", "gnv0"},
		{"ip", "-n", destinationNS, "addr", "add", "192.0.2.2/30", "dev", "gnv0"},
		{"ip", "-n", sourceNS, "link", "set", "gnv0", "up"},
		{"ip", "-n", destinationNS, "link", "set", "gnv0", "up"},
	}
	for _, command := range commands {
		if output, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(command, " "), err, output)
		}
	}
	output, err := exec.Command("ip", "netns", "exec", sourceNS, "ping", "-c", "3", "-W", "1", "192.0.2.2").CombinedOutput()
	if err != nil {
		t.Fatalf("isolated Geneve packet probe: %v: %s", err, output)
	}
	summary := regexp.MustCompile(`(?m)([0-9]+) packets transmitted, ([0-9]+) received`).FindSubmatch(output)
	if len(summary) != 3 {
		t.Fatalf("cannot parse packet evidence: %s", output)
	}
	packetsSent, _ := strconv.ParseUint(string(summary[1]), 10, 64)
	packetsReceived, _ := strconv.ParseUint(string(summary[2]), 10, 64)
	observation := TunnelObservation{
		SourceChassisMatches: true, DestinationChassisMatches: true,
		SourceTunnelPresent: true, DestinationTunnelPresent: true,
		PacketsSent: packetsSent, PacketsReceived: packetsReceived,
	}
	if state := observation.State(); state != "VERIFIED" {
		t.Fatalf("packet path state=%s: %s", state, output)
	}
}
