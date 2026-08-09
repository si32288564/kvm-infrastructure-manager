package linuxhost

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestPCIObservedEvidenceNormalizesPFVFNUMAAndIOMMURelationships(t *testing.T) {
	registry := inventory.NewRegistry()
	if err := registry.Register(PCIModule{Adapter: Adapter{FS: pciFixture()}, ArtifactDigest: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Collect(context.Background(), "host-pci", 7)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CollectionStatus != "COMPLETE" {
		t.Fatalf("collection status = %s", snapshot.CollectionStatus)
	}
	states := capabilityStates(snapshot)
	for _, name := range []string{CapabilityPCI, CapabilityIOMMU, CapabilityPCINUMA, CapabilitySRIOVObserve} {
		if states[name] != inventory.AvailabilityAvailable {
			t.Fatalf("capability %s state = %s", name, states[name])
		}
	}
	devices := snapshot.Fragments[0].PCI.Devices
	if len(devices) != 2 {
		t.Fatalf("devices = %#v", devices)
	}
	vf := devices[1]
	if vf.Address != "0000:03:00.1" || vf.PFAddress != "0000:03:00.0" || vf.VFIndex == nil || *vf.VFIndex != 0 || vf.IOMMUGroup != "13" || vf.NUMANodeID != 1 || vf.RelationshipState != inventory.AvailabilityAvailable {
		t.Fatalf("normalized VF = %#v", vf)
	}
	if len(snapshot.Fragments[0].Evidence) < 12 {
		t.Fatalf("PCI evidence refs = %d", len(snapshot.Fragments[0].Evidence))
	}
}

func TestPCIRelationshipConflictDoesNotQualifyObservation(t *testing.T) {
	fixture := pciFixture()
	fixture["sys/bus/pci/devices/0000:03:00.1/physfn"] = symlink("../0000:04:00.0")
	snapshot := collectPCIFixture(t, fixture)
	if state := capabilityStates(snapshot)[CapabilitySRIOVObserve]; state != inventory.AvailabilityUnknown {
		t.Fatalf("SR-IOV state = %s", state)
	}
	if snapshot.CollectionStatus != "DEGRADED" {
		t.Fatalf("relationship conflict status = %s", snapshot.CollectionStatus)
	}
	if snapshot.Fragments[0].PCI.Devices[1].RelationshipState != inventory.AvailabilityUnknown {
		t.Fatalf("VF relationship = %#v", snapshot.Fragments[0].PCI.Devices[1])
	}
}

func TestPCIIOMMUPermissionFailureRemainsUnknown(t *testing.T) {
	fixture := faultFS{FS: pciFixture(), path: "sys/bus/pci/devices/0000:03:00.1/iommu_group", err: fs.ErrPermission}
	snapshot := collectPCIFixture(t, fixture)
	if state := capabilityStates(snapshot)[CapabilityIOMMU]; state != inventory.AvailabilityUnknown {
		t.Fatalf("IOMMU state = %s", state)
	}
	if snapshot.CollectionStatus != "DEGRADED" {
		t.Fatalf("permission failure status = %s", snapshot.CollectionStatus)
	}
}

func TestPCIDiscoveryWithoutPFIsKnownUnavailableNotQualified(t *testing.T) {
	fixture := pciFixture()
	for _, name := range []string{
		"sys/bus/pci/devices/0000:03:00.0/sriov_totalvfs",
		"sys/bus/pci/devices/0000:03:00.0/sriov_numvfs",
		"sys/bus/pci/devices/0000:03:00.0/virtfn0",
		"sys/bus/pci/devices/0000:03:00.1/physfn",
	} {
		delete(fixture, name)
	}
	snapshot := collectPCIFixture(t, fixture)
	if state := capabilityStates(snapshot)[CapabilitySRIOVObserve]; state != inventory.AvailabilityUnavailable {
		t.Fatalf("SR-IOV state = %s", state)
	}
	if snapshot.CollectionStatus != "COMPLETE" {
		t.Fatalf("known absence status = %s", snapshot.CollectionStatus)
	}
}

func collectPCIFixture(t *testing.T, fsys fs.FS) inventory.Snapshot {
	t.Helper()
	registry := inventory.NewRegistry()
	if err := registry.Register(PCIModule{Adapter: Adapter{FS: fsys}, ArtifactDigest: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Collect(context.Background(), "host-pci", 1)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func pciFixture() fstest.MapFS {
	return fstest.MapFS{
		"sys/bus/pci/devices/0000:03:00.0/vendor":           {Data: []byte("0x8086\n")},
		"sys/bus/pci/devices/0000:03:00.0/device":           {Data: []byte("0x10fb\n")},
		"sys/bus/pci/devices/0000:03:00.0/subsystem_vendor": {Data: []byte("0x8086\n")},
		"sys/bus/pci/devices/0000:03:00.0/subsystem_device": {Data: []byte("0x0001\n")},
		"sys/bus/pci/devices/0000:03:00.0/revision":         {Data: []byte("0x01\n")},
		"sys/bus/pci/devices/0000:03:00.0/numa_node":        {Data: []byte("1\n")},
		"sys/bus/pci/devices/0000:03:00.0/driver":           symlink("../../../../bus/pci/drivers/ixgbe"),
		"sys/bus/pci/devices/0000:03:00.0/iommu_group":      symlink("../../../../kernel/iommu_groups/12"),
		"sys/bus/pci/devices/0000:03:00.0/sriov_totalvfs":   {Data: []byte("2\n")},
		"sys/bus/pci/devices/0000:03:00.0/sriov_numvfs":     {Data: []byte("1\n")},
		"sys/bus/pci/devices/0000:03:00.0/virtfn0":          symlink("../0000:03:00.1"),
		"sys/bus/pci/devices/0000:03:00.1/vendor":           {Data: []byte("0x8086\n")},
		"sys/bus/pci/devices/0000:03:00.1/device":           {Data: []byte("0x10ed\n")},
		"sys/bus/pci/devices/0000:03:00.1/subsystem_vendor": {Data: []byte("0x8086\n")},
		"sys/bus/pci/devices/0000:03:00.1/subsystem_device": {Data: []byte("0x0002\n")},
		"sys/bus/pci/devices/0000:03:00.1/revision":         {Data: []byte("0x01\n")},
		"sys/bus/pci/devices/0000:03:00.1/numa_node":        {Data: []byte("1\n")},
		"sys/bus/pci/devices/0000:03:00.1/driver":           symlink("../../../../bus/pci/drivers/ixgbevf"),
		"sys/bus/pci/devices/0000:03:00.1/iommu_group":      symlink("../../../../kernel/iommu_groups/13"),
		"sys/bus/pci/devices/0000:03:00.1/physfn":           symlink("../0000:03:00.0"),
	}
}

func symlink(target string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(target), Mode: fs.ModeSymlink | 0o777}
}
