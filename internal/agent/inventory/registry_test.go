package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestRegistryCollectsNormalizedTypedSnapshot(t *testing.T) {
	registry := NewRegistry()
	compute := &testModule{descriptor: descriptor("compute", DomainCompute, "kim.host.cpu-topology.v1"), fragment: Fragment{
		Domain: DomainCompute, Capabilities: []Capability{{Name: "kim.host.cpu-topology.v1", Version: "v1", State: AvailabilityAvailable}},
		Compute: &Compute{Architecture: "x86_64", CPUModel: "fixture", Threads: []CPUThread{{LinuxID: 1, CoreID: 0, SocketID: 0, NUMANodeID: 0, Online: true}, {LinuxID: 0, CoreID: 0, SocketID: 0, NUMANodeID: 0, Online: true}}},
	}}
	memory := &testModule{descriptor: descriptor("memory", DomainMemory, "kim.host.numa.v1", "kim.host.hugepages.v1"), fragment: Fragment{
		Domain: DomainMemory, Capabilities: []Capability{{Name: "kim.host.hugepages.v1", Version: "v1", State: AvailabilityAvailable}, {Name: "kim.host.numa.v1", Version: "v1", State: AvailabilityAvailable}},
		Memory: &Memory{TotalBytes: 16 << 30, NUMANodes: []NUMANode{{LinuxID: 0, CPUThreadIDs: []int{0, 1}, MemoryTotalBytes: 16 << 30}}},
	}}
	if err := registry.Register(memory); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(compute); err != nil {
		t.Fatal(err)
	}
	advertised, err := registry.AdvertisedCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if len(advertised) != 3 || advertised[0] != "kim.host.cpu-topology.v1" || advertised[2] != "kim.host.numa.v1" {
		t.Fatalf("advertised capabilities = %#v", advertised)
	}
	snapshot, err := registry.Collect(context.Background(), "host-1", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Fragments) != 2 || snapshot.Fragments[0].Domain != DomainCompute || len(snapshot.Capabilities) != 3 {
		t.Fatalf("normalized snapshot = %#v", snapshot)
	}
	first, err := snapshot.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshot.MarshalCanonical()
	if err != nil || string(first) != string(second) {
		t.Fatalf("canonical snapshot changed: %v", err)
	}
	envelope, err := NewEnvelope(snapshot, 7, "inventory-42")
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ResourceGeneration != 42 || envelope.SessionGeneration != 7 || envelope.SchemaVersion != SnapshotSchemaV2 {
		t.Fatalf("inventory envelope = %#v", envelope)
	}
}

func TestRegistryRejectsDomainAndCapabilityEscape(t *testing.T) {
	registry := NewRegistry()
	module := &testModule{descriptor: descriptor("compute", DomainCompute, "kim.host.cpu.v1"), fragment: Fragment{
		Domain: DomainMemory, Capabilities: []Capability{{Name: "kim.host.unapproved.v1", Version: "v1", State: AvailabilityAvailable}}, Memory: &Memory{},
	}}
	if err := registry.Register(module); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Collect(context.Background(), "host-1", 1); err == nil {
		t.Fatal("domain escape was accepted")
	}
}

func TestRegistryCancelsSnapshotOnModuleFailure(t *testing.T) {
	registry := NewRegistry()
	module := &testModule{descriptor: descriptor("compute", DomainCompute, "kim.host.cpu.v1"), collectErr: errors.New("sysfs unavailable")}
	if err := registry.Register(module); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Collect(context.Background(), "host-1", 1); err == nil {
		t.Fatal("module failure produced an authoritative snapshot")
	}
}

type testModule struct {
	descriptor ModuleDescriptor
	fragment   Fragment
	collectErr error
}

func (module *testModule) Descriptor() ModuleDescriptor { return module.descriptor }
func (module *testModule) Collect(context.Context) (Fragment, error) {
	return module.fragment, module.collectErr
}

func descriptor(name string, domain Domain, capabilities ...string) ModuleDescriptor {
	digest := sha256.Sum256([]byte(name))
	return ModuleDescriptor{Name: name, Version: "v1", Domain: domain, SchemaVersion: "v1", ArtifactDigest: hex.EncodeToString(digest[:]), CapabilityNames: capabilities}
}
