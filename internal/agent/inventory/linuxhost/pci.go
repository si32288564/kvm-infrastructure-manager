package linuxhost

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

const (
	CapabilityPCI          = "kim.host.pci-observation.v1"
	CapabilityIOMMU        = "kim.host.iommu-observation.v1"
	CapabilityPCINUMA      = "kim.host.pci-numa-locality.v1"
	CapabilitySRIOVObserve = "kim.host.sriov-observation.v1"
)

type PCIModule struct {
	Adapter        Adapter
	ArtifactDigest string
}

func (module PCIModule) Descriptor() inventory.ModuleDescriptor {
	return inventory.ModuleDescriptor{Name: "linux-pci", Version: "v1", Domain: inventory.DomainPCI, SchemaVersion: "kim.inventory.linux-pci/v1", ArtifactDigest: module.ArtifactDigest, CapabilityNames: []string{CapabilityPCI, CapabilityIOMMU, CapabilityPCINUMA, CapabilitySRIOVObserve}}
}

func (module PCIModule) Collect(ctx context.Context) (inventory.Fragment, error) {
	if err := ctx.Err(); err != nil {
		return inventory.Fragment{}, err
	}
	raw := module.Adapter.readPCI()
	return normalizePCI(raw), nil
}

type rawPCI struct {
	Devices                            []inventory.PCIDevice
	Evidence                           []inventory.EvidenceRef
	PCIState, IOMMUState, NUMAState    inventory.Availability
	SRIOVState                         inventory.Availability
	PCIReason, IOMMUReason, NUMAReason string
	SRIOVReason                        string
}

type pciLink struct {
	PFAddress string
	VFAddress string
	Index     uint32
	Source    observation[string]
}

func (adapter Adapter) readPCI() rawPCI {
	raw := rawPCI{PCIState: inventory.AvailabilityAvailable, IOMMUState: inventory.AvailabilityUnavailable, NUMAState: inventory.AvailabilityUnavailable, SRIOVState: inventory.AvailabilityUnavailable, IOMMUReason: "no_iommu_group_observed", NUMAReason: "no_pci_numa_locality_observed", SRIOVReason: "no_sriov_pf_observed"}
	entries, err := sortedEntries(adapter.FS, "sys/bus/pci/devices")
	if err != nil {
		raw.PCIState, raw.PCIReason = inventory.AvailabilityUnknown, classifyReadError(err)
		raw.IOMMUState, raw.IOMMUReason = inventory.AvailabilityUnknown, "pci_inventory_unavailable"
		raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnknown, "pci_inventory_unavailable"
		raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "pci_inventory_unavailable"
		raw.Evidence = append(raw.Evidence, inventory.EvidenceRef{Field: "/devices", SourcePath: "/sys/bus/pci/devices", State: raw.PCIState, ReasonCode: raw.PCIReason})
		return raw
	}

	deviceIndex := map[string]int{}
	physicalFunctions := map[string]bool{}
	physicalFunctionLinks := []pciLink{}
	physfnByVF := map[string]observation[string]{}
	for _, entry := range entries {
		address := strings.ToLower(entry.Name())
		if !validBDF(address) {
			continue
		}
		root := join("sys/bus/pci/devices", entry.Name())
		vendor := readHexID(adapter.read(join(root, "vendor"), inventory.AvailabilityUnknown))
		device := readHexID(adapter.read(join(root, "device"), inventory.AvailabilityUnknown))
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/vendor_id", vendor), evidence("/devices/"+address+"/device_id", device))
		if vendor.State != inventory.AvailabilityAvailable || device.State != inventory.AvailabilityAvailable {
			raw.PCIState, raw.PCIReason = inventory.AvailabilityUnknown, "pci_identity_incomplete"
			continue
		}
		numa := adapter.read(join(root, "numa_node"), inventory.AvailabilityUnknown)
		numaID := -1
		if numa.State == inventory.AvailabilityAvailable {
			value, parseErr := strconv.Atoi(numa.Value)
			if parseErr != nil || value < -1 {
				numa.State, numa.ReasonCode = inventory.AvailabilityUnknown, "parse_failed"
				raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnknown, "pci_numa_incomplete"
			} else {
				numaID = value
				if value >= 0 {
					raw.NUMAState, raw.NUMAReason = inventory.AvailabilityAvailable, ""
				}
			}
		} else if numa.State == inventory.AvailabilityUnknown {
			raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnknown, "pci_numa_incomplete"
		}
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/numa_node_id", numa))

		driver := adapter.readLink(join(root, "driver"), inventory.AvailabilityUnavailable)
		if driver.State == inventory.AvailabilityUnavailable {
			driver.ReasonCode = "driver_not_bound"
		}
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/driver", driver))
		iommu := adapter.readLink(join(root, "iommu_group"), inventory.AvailabilityUnavailable)
		if iommu.State == inventory.AvailabilityUnavailable {
			iommu.ReasonCode = "iommu_group_not_assigned"
		}
		group := ""
		if iommu.State == inventory.AvailabilityAvailable {
			group = path.Base(iommu.Value)
			if _, parseErr := strconv.ParseUint(group, 10, 32); parseErr != nil {
				iommu.State, iommu.ReasonCode = inventory.AvailabilityUnknown, "invalid_iommu_group"
				group = ""
				raw.IOMMUState, raw.IOMMUReason = inventory.AvailabilityUnknown, "iommu_relationship_incomplete"
			} else {
				raw.IOMMUState, raw.IOMMUReason = inventory.AvailabilityAvailable, ""
			}
		} else if iommu.State == inventory.AvailabilityUnknown {
			raw.IOMMUState, raw.IOMMUReason = inventory.AvailabilityUnknown, "iommu_relationship_incomplete"
		}
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/iommu_group", iommu))

		subsystemVendor := readOptionalHexID(adapter.read(join(root, "subsystem_vendor"), inventory.AvailabilityUnsupported))
		subsystemDevice := readOptionalHexID(adapter.read(join(root, "subsystem_device"), inventory.AvailabilityUnsupported))
		revision := readOptionalHexByte(adapter.read(join(root, "revision"), inventory.AvailabilityUnsupported))
		for _, optional := range []observation[string]{subsystemVendor, subsystemDevice, revision, driver} {
			if optional.State == inventory.AvailabilityUnknown {
				raw.PCIState, raw.PCIReason = inventory.AvailabilityUnknown, "pci_device_observation_incomplete"
			}
		}
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/subsystem_vendor_id", subsystemVendor), evidence("/devices/"+address+"/subsystem_device_id", subsystemDevice), evidence("/devices/"+address+"/device_revision", revision))

		totalSource := adapter.read(join(root, "sriov_totalvfs"), inventory.AvailabilityUnsupported)
		total, totalSource := parseUint32(totalSource)
		enabledSource := adapter.read(join(root, "sriov_numvfs"), inventory.AvailabilityUnsupported)
		enabled, enabledSource := parseUint32(enabledSource)
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/sriov_total_vfs", totalSource), evidence("/devices/"+address+"/sriov_enabled_vfs", enabledSource))
		relationState, relationReason := inventory.AvailabilityUnsupported, "not_sriov_function"
		if totalSource.State == inventory.AvailabilityAvailable && total > 0 {
			physicalFunctions[address] = true
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityAvailable, ""
			relationState, relationReason = inventory.AvailabilityAvailable, ""
			if enabledSource.State != inventory.AvailabilityAvailable || enabled > total {
				raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "sriov_capacity_incomplete"
				relationState, relationReason = inventory.AvailabilityUnknown, "sriov_capacity_incomplete"
			}
		} else if totalSource.State == inventory.AvailabilityUnknown || enabledSource.State == inventory.AvailabilityUnknown {
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "sriov_observation_incomplete"
		}

		physfn := adapter.readLink(join(root, "physfn"), inventory.AvailabilityUnsupported)
		physfnByVF[address] = physfn
		if physfn.State == inventory.AvailabilityUnsupported {
			physfn.ReasonCode = "not_virtual_function"
			physfnByVF[address] = physfn
		}
		raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/physfn", physfn))
		if physfn.State == inventory.AvailabilityAvailable {
			relationState, relationReason = inventory.AvailabilityUnknown, "vf_reciprocal_relationship_unverified"
		} else if physfn.State == inventory.AvailabilityUnknown {
			relationState, relationReason = inventory.AvailabilityUnknown, "physfn_observation_incomplete"
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "sriov_observation_incomplete"
		}
		item := inventory.PCIDevice{Address: address, VendorID: vendor.Value, DeviceID: device.Value, SubsystemVendorID: subsystemVendor.Value, SubsystemDeviceID: subsystemDevice.Value, Driver: path.Base(driver.Value), DeviceRevision: revision.Value, IOMMUGroup: group, NUMANodeID: numaID, SRIOVTotalVFs: total, SRIOVEnabledVFs: enabled, RelationshipState: relationState, RelationshipReason: relationReason}
		deviceIndex[address] = len(raw.Devices)
		raw.Devices = append(raw.Devices, item)

		deviceEntries, readErr := sortedEntries(adapter.FS, root)
		if readErr != nil {
			if physicalFunctions[address] {
				raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "virtfn_observation_incomplete"
			}
			continue
		}
		for _, child := range deviceEntries {
			if !strings.HasPrefix(child.Name(), "virtfn") {
				continue
			}
			index, parseErr := strconv.ParseUint(strings.TrimPrefix(child.Name(), "virtfn"), 10, 32)
			target := adapter.readLink(join(root, child.Name()), inventory.AvailabilityUnknown)
			if parseErr != nil || target.State != inventory.AvailabilityAvailable || !validBDF(strings.ToLower(path.Base(target.Value))) {
				raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "invalid_virtfn_relationship"
				continue
			}
			physicalFunctionLinks = append(physicalFunctionLinks, pciLink{PFAddress: address, VFAddress: strings.ToLower(path.Base(target.Value)), Index: uint32(index), Source: target})
			raw.Evidence = append(raw.Evidence, evidence("/devices/"+address+"/"+child.Name(), target))
		}
	}

	linksByPF := map[string]int{}
	for _, link := range physicalFunctionLinks {
		linksByPF[link.PFAddress]++
		vfIndex, exists := deviceIndex[link.VFAddress]
		if !exists || !physicalFunctions[link.PFAddress] {
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "pf_vf_device_missing"
			continue
		}
		back := physfnByVF[link.VFAddress]
		if back.State != inventory.AvailabilityAvailable || strings.ToLower(path.Base(back.Value)) != link.PFAddress {
			raw.Devices[vfIndex].RelationshipState = inventory.AvailabilityUnknown
			raw.Devices[vfIndex].RelationshipReason = "physfn_virtfn_conflict"
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "physfn_virtfn_conflict"
			continue
		}
		raw.Devices[vfIndex].PFAddress = link.PFAddress
		vfNumber := link.Index
		raw.Devices[vfIndex].VFIndex = &vfNumber
		raw.Devices[vfIndex].RelationshipState = inventory.AvailabilityAvailable
		raw.Devices[vfIndex].RelationshipReason = ""
	}
	for address := range physicalFunctions {
		index := deviceIndex[address]
		if uint32(linksByPF[address]) != raw.Devices[index].SRIOVEnabledVFs {
			raw.Devices[index].RelationshipState = inventory.AvailabilityUnknown
			raw.Devices[index].RelationshipReason = "enabled_vf_count_mismatch"
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "enabled_vf_count_mismatch"
		}
	}
	for vfAddress, link := range physfnByVF {
		if link.State != inventory.AvailabilityAvailable {
			continue
		}
		index := deviceIndex[vfAddress]
		if raw.Devices[index].PFAddress == "" {
			raw.Devices[index].RelationshipState = inventory.AvailabilityUnknown
			raw.Devices[index].RelationshipReason = "orphan_physfn_relationship"
			raw.SRIOVState, raw.SRIOVReason = inventory.AvailabilityUnknown, "orphan_physfn_relationship"
		}
	}
	if len(raw.Devices) == 0 && raw.PCIState == inventory.AvailabilityAvailable {
		raw.PCIState, raw.PCIReason = inventory.AvailabilityUnavailable, "no_pci_devices_observed"
	}
	return raw
}

func normalizePCI(raw rawPCI) inventory.Fragment {
	sort.Slice(raw.Devices, func(i, j int) bool { return raw.Devices[i].Address < raw.Devices[j].Address })
	return inventory.Fragment{Domain: inventory.DomainPCI, Evidence: raw.Evidence, Capabilities: []inventory.Capability{
		capability(CapabilityPCI, raw.PCIState, raw.PCIReason),
		capability(CapabilityIOMMU, raw.IOMMUState, raw.IOMMUReason),
		capability(CapabilityPCINUMA, raw.NUMAState, raw.NUMAReason),
		capability(CapabilitySRIOVObserve, raw.SRIOVState, raw.SRIOVReason),
	}, PCI: &inventory.PCI{IOMMUEnabled: raw.IOMMUState == inventory.AvailabilityAvailable, Devices: raw.Devices}}
}

func readHexID(source observation[string]) observation[string] {
	if source.State != inventory.AvailabilityAvailable {
		return source
	}
	value := strings.ToLower(strings.TrimPrefix(source.Value, "0x"))
	if len(value) != 4 {
		source.State, source.ReasonCode, source.Value = inventory.AvailabilityUnknown, "invalid_pci_id", ""
		return source
	}
	if _, err := strconv.ParseUint(value, 16, 16); err != nil {
		source.State, source.ReasonCode, source.Value = inventory.AvailabilityUnknown, "invalid_pci_id", ""
		return source
	}
	source.Value = value
	return source
}

func readOptionalHexID(source observation[string]) observation[string] {
	if source.State == inventory.AvailabilityUnsupported {
		source.ReasonCode = "field_not_supported"
		return source
	}
	return readHexID(source)
}

func readOptionalHexByte(source observation[string]) observation[string] {
	if source.State == inventory.AvailabilityUnsupported {
		source.ReasonCode = "field_not_supported"
		return source
	}
	if source.State != inventory.AvailabilityAvailable {
		return source
	}
	value := strings.ToLower(strings.TrimPrefix(source.Value, "0x"))
	if len(value) != 2 {
		source.State, source.ReasonCode, source.Value = inventory.AvailabilityUnknown, "invalid_pci_revision", ""
		return source
	}
	if _, err := strconv.ParseUint(value, 16, 8); err != nil {
		source.State, source.ReasonCode, source.Value = inventory.AvailabilityUnknown, "invalid_pci_revision", ""
		return source
	}
	source.Value = value
	return source
}

func parseUint32(source observation[string]) (uint32, observation[string]) {
	if source.State == inventory.AvailabilityUnsupported {
		source.ReasonCode = "field_not_supported"
		return 0, source
	}
	if source.State != inventory.AvailabilityAvailable {
		return 0, source
	}
	value, err := strconv.ParseUint(source.Value, 10, 32)
	if err != nil {
		source.State, source.ReasonCode = inventory.AvailabilityUnknown, "parse_failed"
		return 0, source
	}
	return uint32(value), source
}

func validBDF(value string) bool {
	var domain, bus, slot, function uint64
	if _, err := fmt.Sscanf(value, "%04x:%02x:%02x.%1x", &domain, &bus, &slot, &function); err != nil {
		return false
	}
	return slot <= 0x1f && function <= 7 && fmt.Sprintf("%04x:%02x:%02x.%x", domain, bus, slot, function) == value
}
