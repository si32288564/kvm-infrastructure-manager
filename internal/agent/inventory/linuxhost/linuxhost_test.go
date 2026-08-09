package linuxhost

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestLinuxEvidenceChainNormalizesComputeMemoryNUMAAndHugePages(t *testing.T) {
	registry := inventory.NewRegistry()
	adapter := Adapter{FS: normalFixture(), Architecture: "x86_64"}
	if err := registry.Register(ComputeModule{Adapter: adapter, ArtifactDigest: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(MemoryModule{Adapter: adapter, ArtifactDigest: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Collect(context.Background(), "host-fixture", 1)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CollectionStatus != "COMPLETE" {
		t.Fatalf("collection status = %s", snapshot.CollectionStatus)
	}
	states := capabilityStates(snapshot)
	for _, name := range []string{CapabilityCPUTopology, CapabilityCPUModel, CapabilityIsolation, CapabilityMemory, CapabilityNUMA, CapabilityHugePages} {
		if states[name] != inventory.AvailabilityAvailable {
			t.Fatalf("capability %s state = %s", name, states[name])
		}
	}
	compute := snapshot.Fragments[0].Compute
	memory := snapshot.Fragments[1].Memory
	if compute == nil || len(compute.Threads) != 2 || !compute.Threads[1].Isolated || compute.Threads[1].NUMANodeID != 0 {
		t.Fatalf("compute projection = %#v", compute)
	}
	if memory == nil || memory.TotalBytes != 16*1024*1024 || len(memory.NUMANodes) != 1 || len(memory.HugePagePools) != 2 || memory.HugePagePools[0].ReservedPages != 1 {
		t.Fatalf("memory projection = %#v", memory)
	}
	if len(snapshot.Fragments[0].Evidence) == 0 || len(snapshot.Fragments[1].Evidence) == 0 {
		t.Fatal("normalized fragments lost source evidence")
	}
}

func TestLinuxEvidenceChainPreservesKnownAbsenceAndUnsupported(t *testing.T) {
	fixture := normalFixture()
	for name := range fixture {
		if strings.HasPrefix(name, "sys/devices/system/node/") {
			delete(fixture, name)
		}
	}
	fixture["sys/kernel/mm/hugepages/hugepages-2048kB/nr_hugepages"] = &fstest.MapFile{Data: []byte("0\n")}
	fixture["sys/kernel/mm/hugepages/hugepages-2048kB/free_hugepages"] = &fstest.MapFile{Data: []byte("0\n")}
	fixture["sys/kernel/mm/hugepages/hugepages-2048kB/resv_hugepages"] = &fstest.MapFile{Data: []byte("0\n")}
	adapter := Adapter{FS: fixture, Architecture: "x86_64"}
	snapshot := collectFixture(t, adapter)
	states := capabilityStates(snapshot)
	if states[CapabilityNUMA] != inventory.AvailabilityUnsupported {
		t.Fatalf("NUMA state = %s", states[CapabilityNUMA])
	}
	if states[CapabilityHugePages] != inventory.AvailabilityUnavailable {
		t.Fatalf("HugePages state = %s", states[CapabilityHugePages])
	}
	if snapshot.CollectionStatus != "COMPLETE" {
		t.Fatalf("known absence was treated as uncertainty: %s", snapshot.CollectionStatus)
	}
}

func TestLinuxEvidenceChainMarksPermissionAndMissingTopologyUnknown(t *testing.T) {
	for _, test := range []struct {
		name string
		fs   fs.FS
		cap  string
	}{
		{name: "permission denied", fs: faultFS{FS: normalFixture(), path: "sys/devices/system/cpu/isolated", err: fs.ErrPermission}, cap: CapabilityIsolation},
		{name: "procfs permission denied", fs: faultFS{FS: normalFixture(), path: "proc/meminfo", err: fs.ErrPermission}, cap: CapabilityMemory},
		{name: "missing mandatory topology", fs: without(normalFixture(), "sys/devices/system/cpu/cpu1/topology/core_id"), cap: CapabilityCPUTopology},
		{name: "malformed NUMA cpulist", fs: withFile(normalFixture(), "sys/devices/system/node/node0/cpulist", "0-bad\n"), cap: CapabilityNUMA},
		{name: "kernel pool interface difference", fs: without(normalFixture(), "sys/kernel/mm/hugepages/hugepages-2048kB/resv_hugepages"), cap: CapabilityHugePages},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := collectFixture(t, Adapter{FS: test.fs, Architecture: "x86_64"})
			if state := capabilityStates(snapshot)[test.cap]; state != inventory.AvailabilityUnknown {
				t.Fatalf("capability %s state = %s", test.cap, state)
			}
			if snapshot.CollectionStatus != "DEGRADED" {
				t.Fatalf("unknown evidence produced status %s", snapshot.CollectionStatus)
			}
		})
	}
}

func TestCapabilityStateCannotCollapseToZeroValue(t *testing.T) {
	fragment := normalizeMemory(rawMemory{MemoryState: inventory.AvailabilityUnknown, MemoryReason: "permission_denied", NUMAState: inventory.AvailabilityUnsupported, NUMAReason: "interface_not_present", HugePageState: inventory.AvailabilityUnavailable, HugePageReason: "no_pages_configured"})
	fragment.Source = inventory.Source{ModuleName: "fixture", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	snapshot := inventory.Snapshot{SchemaVersion: inventory.SnapshotSchemaV2, HostIdentity: "host", ObservationGeneration: 1, CollectionStatus: "DEGRADED", Fragments: []inventory.Fragment{fragment}}
	if err := snapshot.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	for _, capability := range snapshot.Capabilities {
		if capability.State == "" || capability.ReasonCode == "" {
			t.Fatalf("capability state collapsed: %#v", capability)
		}
	}
}

type faultFS struct {
	fs.FS
	path string
	err  error
}

func (fault faultFS) Open(name string) (fs.File, error) {
	if name == fault.path {
		return nil, fault.err
	}
	return fault.FS.Open(name)
}

func collectFixture(t *testing.T, adapter Adapter) inventory.Snapshot {
	t.Helper()
	registry := inventory.NewRegistry()
	if err := registry.Register(ComputeModule{Adapter: adapter, ArtifactDigest: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(MemoryModule{Adapter: adapter, ArtifactDigest: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Collect(context.Background(), "host-fixture", 1)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func capabilityStates(snapshot inventory.Snapshot) map[string]inventory.Availability {
	states := map[string]inventory.Availability{}
	for _, capability := range snapshot.Capabilities {
		states[capability.Name] = capability.State
	}
	return states
}

func without(source fstest.MapFS, names ...string) fstest.MapFS {
	copy := fstest.MapFS{}
	for name, file := range source {
		copy[name] = file
	}
	for _, name := range names {
		delete(copy, name)
	}
	return copy
}

func withFile(source fstest.MapFS, name, value string) fstest.MapFS {
	copy := without(source)
	copy[name] = &fstest.MapFile{Data: []byte(value)}
	return copy
}

func normalFixture() fstest.MapFS {
	return fstest.MapFS{
		"proc/cpuinfo":                                                               {Data: []byte("processor: 0\nmodel name: KIM Fixture CPU\nprocessor: 1\nmodel name: KIM Fixture CPU\n")},
		"proc/meminfo":                                                               {Data: []byte("MemTotal: 16384 kB\nMemAvailable: 8192 kB\nHugepagesize: 2048 kB\n")},
		"sys/devices/system/cpu/isolated":                                            {Data: []byte("1\n")},
		"sys/devices/system/cpu/cpu0/topology/core_id":                               {Data: []byte("0\n")},
		"sys/devices/system/cpu/cpu0/topology/physical_package_id":                   {Data: []byte("0\n")},
		"sys/devices/system/cpu/cpu1/topology/core_id":                               {Data: []byte("0\n")},
		"sys/devices/system/cpu/cpu1/topology/physical_package_id":                   {Data: []byte("0\n")},
		"sys/devices/system/cpu/cpu1/online":                                         {Data: []byte("1\n")},
		"sys/devices/system/node/node0/cpulist":                                      {Data: []byte("0-1\n")},
		"sys/devices/system/node/node0/meminfo":                                      {Data: []byte("Node 0 MemTotal: 16384 kB\n")},
		"sys/devices/system/node/node0/hugepages/hugepages-2048kB/nr_hugepages":      {Data: []byte("8\n")},
		"sys/devices/system/node/node0/hugepages/hugepages-2048kB/free_hugepages":    {Data: []byte("7\n")},
		"sys/devices/system/node/node0/hugepages/hugepages-2048kB/resv_hugepages":    {Data: []byte("1\n")},
		"sys/devices/system/node/node0/hugepages/hugepages-2048kB/surplus_hugepages": {Data: []byte("0\n")},
		"sys/kernel/mm/hugepages/hugepages-2048kB/nr_hugepages":                      {Data: []byte("8\n")},
		"sys/kernel/mm/hugepages/hugepages-2048kB/free_hugepages":                    {Data: []byte("7\n")},
		"sys/kernel/mm/hugepages/hugepages-2048kB/resv_hugepages":                    {Data: []byte("1\n")},
		"sys/kernel/mm/hugepages/hugepages-2048kB/surplus_hugepages":                 {Data: []byte("0\n")},
	}
}

const fixtureDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
