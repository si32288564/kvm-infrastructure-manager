package placement

import (
	"testing"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestEvaluateAndSelectExcludeIneligibleHighCapacityHost(t *testing.T) {
	request := placementRequestFixture()
	eligibleAuthority := authorityFixture("host-b", "READY", 8, 4, 16*1024, 8*1024, 8)
	eligible, err := Evaluate(request, eligibleAuthority)
	if err != nil || !eligible.Eligible {
		t.Fatalf("eligible evaluation/error = %#v/%v", eligible, err)
	}
	ineligibleAuthority := authorityFixture("host-a", "BLOCKED", 128, 128, 1024*1024, 1024*1024, 1024)
	ineligible, err := Evaluate(request, ineligibleAuthority)
	if err != nil || ineligible.Eligible {
		t.Fatalf("ineligible evaluation/error = %#v/%v", ineligible, err)
	}
	selected := Select([]Evaluation{ineligible, eligible})
	if len(selected) != 1 || selected[0].HostID != "host-b" {
		t.Fatalf("selected candidates = %#v", selected)
	}
	deniedRequest := request
	deniedRequest.CatalogAccessAllowed = false
	denied, err := Evaluate(deniedRequest, eligibleAuthority)
	if err != nil || denied.Eligible || !contains(denied.ReasonCodes, "catalog_access_denied") {
		t.Fatalf("cross-Project catalog evaluation/error = %#v/%v", denied, err)
	}
}

func TestEvaluateHugePageAndPinnedCPUClaims(t *testing.T) {
	request := placementRequestFixture()
	authority := authorityFixture("host-a", "READY", 8, 4, 16*1024, 8*1024, 8)
	evaluation, err := Evaluate(request, authority)
	if err != nil || !evaluation.Eligible {
		t.Fatalf("evaluation/error = %#v/%v", evaluation, err)
	}
	if evaluation.RequiredClaim.HugePagePages != 4 || evaluation.RequiredClaim.CPUAllocation != "DEDICATED" {
		t.Fatalf("required claim = %#v", evaluation.RequiredClaim)
	}
	authority.ClaimedVCPUs = 1
	authority.ClaimedHugePages[1048576] = 5
	blocked, err := Evaluate(request, authority)
	if err != nil || blocked.Eligible {
		t.Fatalf("oversubscribed evaluation/error = %#v/%v", blocked, err)
	}
	if !contains(blocked.ReasonCodes, "insufficient_vcpu_capacity") || !contains(blocked.ReasonCodes, "insufficient_hugepage_capacity") {
		t.Fatalf("bounded reasons = %#v", blocked.ReasonCodes)
	}
}

func placementRequestFixture() Request {
	numa := uint32(2)
	huge := uint64(1048576)
	return Request{
		RequestID: "request", ProjectID: "project", WorkloadID: "vm",
		ImageID: "image", ImageRevision: 1, FlavorID: "flavor", FlavorRevision: 1,
		FlavorShapeDigest: "shape", PoolID: "pool", VCPUs: 4, MemoryMiB: 4096,
		RootDiskGiB: 20, NUMAPolicy: "REQUIRED", NUMANodes: &numa,
		HugePageSizeKiB: &huge, CPUAllocation: "DEDICATED", CPUPinning: true,
		CatalogAccessAllowed: true,
	}
}

func authorityFixture(hostID, readiness string, threads, isolated, totalMemoryMiB, availableMemoryMiB, hugePages uint64) AuthoritySnapshot {
	computeThreads := make([]agentinventory.CPUThread, threads)
	for index := range computeThreads {
		computeThreads[index] = agentinventory.CPUThread{LinuxID: index, CoreID: index / 2, SocketID: 0, NUMANodeID: index % 2, Online: true, Isolated: uint64(index) < isolated}
	}
	return AuthoritySnapshot{
		DatabaseMode: "ACTIVE", HostID: hostID, PoolID: "pool", PoolGeneration: 1,
		PoolPolicyID: "default", PoolPolicyGeneration: 1,
		MembershipGeneration: 1, PoolState: "ACTIVE", MembershipState: "ACTIVE",
		CapabilityGeneration: 1, CapabilityState: "CURRENT", ReadinessState: readiness,
		PreflightState: "PASSED", ComplianceState: "COMPLIANT",
		ReadinessCapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, ComplianceGeneration: 1,
		ClaimedHugePages: map[uint64]uint64{},
		Inventory: agentinventory.Snapshot{
			ObservationGeneration: 1,
			Fragments: []agentinventory.Fragment{
				{
					Domain:       agentinventory.DomainCompute,
					Capabilities: []agentinventory.Capability{{Name: "kim.host.cpu-topology.v1", State: agentinventory.AvailabilityAvailable}},
					Compute:      &agentinventory.Compute{Threads: computeThreads},
				},
				{
					Domain:       agentinventory.DomainMemory,
					Capabilities: []agentinventory.Capability{{Name: "kim.host.memory.v1", State: agentinventory.AvailabilityAvailable}},
					Memory: &agentinventory.Memory{
						TotalBytes: totalMemoryMiB * 1024 * 1024, AvailableBytes: availableMemoryMiB * 1024 * 1024,
						NUMANodes:     []agentinventory.NUMANode{{LinuxID: 0}, {LinuxID: 1}},
						HugePagePools: []agentinventory.HugePagePool{{PageSizeBytes: 1024 * 1024 * 1024, TotalPages: hugePages, FreePages: hugePages}},
					},
				},
			},
		},
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
