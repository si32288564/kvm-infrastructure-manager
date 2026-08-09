// Package inventory defines the transport-neutral normalized Host inventory
// contract produced by typed KIM Host Agent modules.
package inventory

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

const SnapshotSchemaV3 = "kim.inventory.snapshot/v3"

type Domain string

const (
	DomainCompute        Domain = "COMPUTE"
	DomainMemory         Domain = "MEMORY"
	DomainPCI            Domain = "PCI"
	DomainNetwork        Domain = "NETWORK"
	DomainStorage        Domain = "STORAGE"
	DomainVirtualization Domain = "VIRTUALIZATION"
)

var knownDomains = map[Domain]struct{}{
	DomainCompute: {}, DomainMemory: {}, DomainPCI: {}, DomainNetwork: {}, DomainStorage: {}, DomainVirtualization: {},
}

type Capability struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	State       Availability `json:"state"`
	ReasonCode  string       `json:"reason_code,omitempty"`
	Constraints []Constraint `json:"constraints,omitempty"`
}

// Availability preserves the difference between known absence, observation
// uncertainty, and an interface that the Host does not implement.
type Availability string

const (
	AvailabilityAvailable   Availability = "AVAILABLE"
	AvailabilityUnavailable Availability = "UNAVAILABLE"
	AvailabilityUnknown     Availability = "UNKNOWN"
	AvailabilityUnsupported Availability = "UNSUPPORTED"
)

func (state Availability) Valid() bool {
	switch state {
	case AvailabilityAvailable, AvailabilityUnavailable, AvailabilityUnknown, AvailabilityUnsupported:
		return true
	default:
		return false
	}
}

type Constraint struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Source struct {
	ModuleName     string `json:"module_name"`
	ModuleVersion  string `json:"module_version"`
	SchemaVersion  string `json:"schema_version"`
	ArtifactDigest string `json:"artifact_digest"`
}

// EvidenceRef records the typed source and observation outcome used by a
// normalizer. Raw bytes stay inside the OS Integration Adapter boundary.
type EvidenceRef struct {
	Field      string       `json:"field"`
	SourcePath string       `json:"source_path"`
	State      Availability `json:"state"`
	ReasonCode string       `json:"reason_code,omitempty"`
}

type CPUThread struct {
	LinuxID    int  `json:"linux_id"`
	CoreID     int  `json:"core_id"`
	SocketID   int  `json:"socket_id"`
	NUMANodeID int  `json:"numa_node_id"`
	Online     bool `json:"online"`
	Isolated   bool `json:"isolated"`
}

type Compute struct {
	Architecture string      `json:"architecture"`
	CPUModel     string      `json:"cpu_model"`
	Threads      []CPUThread `json:"threads"`
}

type NUMANode struct {
	LinuxID          int    `json:"linux_id"`
	CPUThreadIDs     []int  `json:"cpu_thread_ids"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes"`
}

type HugePagePool struct {
	NUMANodeID    *int   `json:"numa_node_id,omitempty"`
	PageSizeBytes uint64 `json:"page_size_bytes"`
	TotalPages    uint64 `json:"total_pages"`
	FreePages     uint64 `json:"free_pages"`
	ReservedPages uint64 `json:"reserved_pages"`
	SurplusPages  uint64 `json:"surplus_pages"`
}

type Memory struct {
	TotalBytes     uint64         `json:"total_bytes"`
	AvailableBytes uint64         `json:"available_bytes"`
	NUMANodes      []NUMANode     `json:"numa_nodes"`
	HugePagePools  []HugePagePool `json:"hugepage_pools"`
}

type PCIDevice struct {
	Address            string       `json:"address"`
	VendorID           string       `json:"vendor_id"`
	DeviceID           string       `json:"device_id"`
	SubsystemVendorID  string       `json:"subsystem_vendor_id,omitempty"`
	SubsystemDeviceID  string       `json:"subsystem_device_id,omitempty"`
	Driver             string       `json:"driver,omitempty"`
	DeviceRevision     string       `json:"device_revision,omitempty"`
	FirmwareRevision   string       `json:"firmware_revision,omitempty"`
	IOMMUGroup         string       `json:"iommu_group,omitempty"`
	NUMANodeID         int          `json:"numa_node_id"`
	SRIOVTotalVFs      uint32       `json:"sriov_total_vfs"`
	SRIOVEnabledVFs    uint32       `json:"sriov_enabled_vfs"`
	PFAddress          string       `json:"pf_address,omitempty"`
	VFIndex            *uint32      `json:"vf_index,omitempty"`
	RelationshipState  Availability `json:"relationship_state"`
	RelationshipReason string       `json:"relationship_reason,omitempty"`
}

type PCI struct {
	IOMMUEnabled bool        `json:"iommu_enabled"`
	Devices      []PCIDevice `json:"devices"`
}

type NetworkInterface struct {
	Name       string `json:"name"`
	MACAddress string `json:"mac_address"`
	PCIAddress string `json:"pci_address,omitempty"`
	Driver     string `json:"driver"`
	NUMANodeID int    `json:"numa_node_id"`
}

type Network struct {
	Interfaces []NetworkInterface `json:"interfaces"`
}

type StorageDevice struct {
	StableID  string `json:"stable_id"`
	Kind      string `json:"kind"`
	SizeBytes uint64 `json:"size_bytes"`
}

type Storage struct {
	Devices []StorageDevice `json:"devices"`
}

type Virtualization struct {
	KVMAvailable   bool     `json:"kvm_available"`
	LibvirtVersion string   `json:"libvirt_version"`
	QEMUVersion    string   `json:"qemu_version"`
	MachineTypes   []string `json:"machine_types"`
}

// Fragment is a closed typed union. A module emits exactly one domain and
// cannot smuggle arbitrary transport-specific payload through the contract.
type Fragment struct {
	Domain         Domain          `json:"domain"`
	Source         Source          `json:"source"`
	Evidence       []EvidenceRef   `json:"evidence"`
	Capabilities   []Capability    `json:"capabilities"`
	Compute        *Compute        `json:"compute,omitempty"`
	Memory         *Memory         `json:"memory,omitempty"`
	PCI            *PCI            `json:"pci,omitempty"`
	Network        *Network        `json:"network,omitempty"`
	Storage        *Storage        `json:"storage,omitempty"`
	Virtualization *Virtualization `json:"virtualization,omitempty"`
}

type Snapshot struct {
	SchemaVersion         string       `json:"schema_version"`
	HostIdentity          string       `json:"host_identity"`
	ObservationGeneration uint64       `json:"observation_generation"`
	CollectionStatus      string       `json:"collection_status"`
	Capabilities          []Capability `json:"capabilities"`
	Fragments             []Fragment   `json:"fragments"`
}

func (snapshot *Snapshot) NormalizeAndValidate() error {
	if snapshot.SchemaVersion != SnapshotSchemaV3 || snapshot.HostIdentity == "" || snapshot.ObservationGeneration == 0 {
		return errors.New("complete Host inventory snapshot identity is required")
	}
	if snapshot.CollectionStatus != "COMPLETE" && snapshot.CollectionStatus != "DEGRADED" {
		return errors.New("Host inventory collection status must be COMPLETE or DEGRADED")
	}
	capabilities := make(map[string]Capability)
	for index := range snapshot.Fragments {
		fragment := &snapshot.Fragments[index]
		if err := normalizeFragment(fragment); err != nil {
			return err
		}
		for _, capability := range fragment.Capabilities {
			if _, duplicate := capabilities[capability.Name]; duplicate {
				return fmt.Errorf("duplicate Host capability %s", capability.Name)
			}
			capabilities[capability.Name] = capability
		}
	}
	sort.Slice(snapshot.Fragments, func(i, j int) bool {
		if snapshot.Fragments[i].Domain != snapshot.Fragments[j].Domain {
			return snapshot.Fragments[i].Domain < snapshot.Fragments[j].Domain
		}
		return snapshot.Fragments[i].Source.ModuleName < snapshot.Fragments[j].Source.ModuleName
	})
	snapshot.Capabilities = snapshot.Capabilities[:0]
	for _, capability := range capabilities {
		snapshot.Capabilities = append(snapshot.Capabilities, capability)
	}
	sort.Slice(snapshot.Capabilities, func(i, j int) bool { return snapshot.Capabilities[i].Name < snapshot.Capabilities[j].Name })
	return nil
}

func DecodeSnapshot(payload []byte) (Snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode Host inventory snapshot: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Snapshot{}, errors.New("Host inventory snapshot contains trailing JSON or invalid data")
	}
	presentedCapabilities, err := json.Marshal(snapshot.Capabilities)
	if err != nil {
		return Snapshot{}, err
	}
	if err := snapshot.NormalizeAndValidate(); err != nil {
		return Snapshot{}, err
	}
	normalizedCapabilities, err := json.Marshal(snapshot.Capabilities)
	if err != nil {
		return Snapshot{}, err
	}
	if !bytes.Equal(presentedCapabilities, normalizedCapabilities) {
		return Snapshot{}, errors.New("Host inventory top-level capabilities do not match typed fragments")
	}
	return snapshot, nil
}

func (snapshot Snapshot) MarshalCanonical() ([]byte, error) {
	if err := snapshot.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

func normalizeFragment(fragment *Fragment) error {
	if _, ok := knownDomains[fragment.Domain]; !ok || fragment.Source.ModuleName == "" || fragment.Source.ModuleVersion == "" || fragment.Source.SchemaVersion == "" || !validDigest(fragment.Source.ArtifactDigest) {
		return fmt.Errorf("invalid Host inventory fragment source for domain %s", fragment.Domain)
	}
	nonNil := 0
	for _, present := range []bool{fragment.Compute != nil, fragment.Memory != nil, fragment.PCI != nil, fragment.Network != nil, fragment.Storage != nil, fragment.Virtualization != nil} {
		if present {
			nonNil++
		}
	}
	if nonNil != 1 || (fragment.Domain == DomainCompute) != (fragment.Compute != nil) || (fragment.Domain == DomainMemory) != (fragment.Memory != nil) || (fragment.Domain == DomainPCI) != (fragment.PCI != nil) || (fragment.Domain == DomainNetwork) != (fragment.Network != nil) || (fragment.Domain == DomainStorage) != (fragment.Storage != nil) || (fragment.Domain == DomainVirtualization) != (fragment.Virtualization != nil) {
		return fmt.Errorf("Host inventory domain %s does not match its typed payload", fragment.Domain)
	}
	sort.Slice(fragment.Evidence, func(i, j int) bool {
		if fragment.Evidence[i].Field != fragment.Evidence[j].Field {
			return fragment.Evidence[i].Field < fragment.Evidence[j].Field
		}
		return fragment.Evidence[i].SourcePath < fragment.Evidence[j].SourcePath
	})
	for index, evidence := range fragment.Evidence {
		if evidence.Field == "" || evidence.SourcePath == "" || !evidence.State.Valid() || (evidence.State == AvailabilityAvailable && evidence.ReasonCode != "") || (evidence.State != AvailabilityAvailable && evidence.ReasonCode == "") {
			return errors.New("Host inventory evidence requires a typed field, source, state, and failure reason")
		}
		if index > 0 && fragment.Evidence[index-1].Field == evidence.Field && fragment.Evidence[index-1].SourcePath == evidence.SourcePath {
			return errors.New("duplicate Host inventory evidence reference")
		}
	}
	for index := range fragment.Capabilities {
		capability := &fragment.Capabilities[index]
		if capability.Name == "" || capability.Version == "" || !capability.State.Valid() {
			return errors.New("Host capability name, version, and availability state are required")
		}
		if capability.State == AvailabilityAvailable && capability.ReasonCode != "" {
			return errors.New("available Host capability cannot carry a failure reason")
		}
		if capability.State != AvailabilityAvailable && capability.ReasonCode == "" {
			return errors.New("non-available Host capability requires a reason code")
		}
		sort.Slice(capability.Constraints, func(i, j int) bool { return capability.Constraints[i].Name < capability.Constraints[j].Name })
		for i, constraint := range capability.Constraints {
			if constraint.Name == "" || (i > 0 && capability.Constraints[i-1].Name == constraint.Name) {
				return errors.New("Host capability constraint names must be non-empty and unique")
			}
		}
	}
	sort.Slice(fragment.Capabilities, func(i, j int) bool { return fragment.Capabilities[i].Name < fragment.Capabilities[j].Name })
	switch fragment.Domain {
	case DomainCompute:
		if capabilityIsAvailable(fragment.Capabilities, "kim.host.cpu-topology.v1") && (fragment.Compute.Architecture == "" || len(fragment.Compute.Threads) == 0) {
			return errors.New("available Compute topology requires architecture and threads")
		}
		sort.Slice(fragment.Compute.Threads, func(i, j int) bool { return fragment.Compute.Threads[i].LinuxID < fragment.Compute.Threads[j].LinuxID })
		for index, thread := range fragment.Compute.Threads {
			if thread.LinuxID < 0 || thread.CoreID < 0 || thread.SocketID < 0 || thread.NUMANodeID < -1 || (index > 0 && fragment.Compute.Threads[index-1].LinuxID == thread.LinuxID) {
				return errors.New("Compute thread topology contains invalid or duplicate IDs")
			}
		}
	case DomainMemory:
		if capabilityIsAvailable(fragment.Capabilities, "kim.host.memory.v1") && fragment.Memory.TotalBytes == 0 {
			return errors.New("Memory inventory total bytes must be positive")
		}
		sort.Slice(fragment.Memory.NUMANodes, func(i, j int) bool {
			return fragment.Memory.NUMANodes[i].LinuxID < fragment.Memory.NUMANodes[j].LinuxID
		})
		for index := range fragment.Memory.NUMANodes {
			if fragment.Memory.NUMANodes[index].LinuxID < 0 || (capabilityIsAvailable(fragment.Capabilities, "kim.host.numa.v1") && fragment.Memory.NUMANodes[index].MemoryTotalBytes == 0) || (index > 0 && fragment.Memory.NUMANodes[index-1].LinuxID == fragment.Memory.NUMANodes[index].LinuxID) {
				return errors.New("NUMA topology contains invalid or duplicate nodes")
			}
			sort.Ints(fragment.Memory.NUMANodes[index].CPUThreadIDs)
		}
		sort.Slice(fragment.Memory.HugePagePools, func(i, j int) bool {
			left, right := fragment.Memory.HugePagePools[i], fragment.Memory.HugePagePools[j]
			if left.NUMANodeID == nil {
				return right.NUMANodeID != nil
			}
			if right.NUMANodeID == nil || *left.NUMANodeID != *right.NUMANodeID {
				return right.NUMANodeID == nil || *left.NUMANodeID < *right.NUMANodeID
			}
			return left.PageSizeBytes < right.PageSizeBytes
		})
		for _, pool := range fragment.Memory.HugePagePools {
			if pool.PageSizeBytes == 0 || (capabilityIsAvailable(fragment.Capabilities, "kim.host.hugepages.v1") && pool.FreePages > pool.TotalPages) || (pool.NUMANodeID != nil && *pool.NUMANodeID < 0) {
				return errors.New("HugePage pool contains invalid capacity or NUMA locality")
			}
		}
	case DomainPCI:
		sort.Slice(fragment.PCI.Devices, func(i, j int) bool { return fragment.PCI.Devices[i].Address < fragment.PCI.Devices[j].Address })
		for index, device := range fragment.PCI.Devices {
			if !validPCIAddress(device.Address) || !validPCIID(device.VendorID) || !validPCIID(device.DeviceID) || (device.SubsystemVendorID != "" && !validPCIID(device.SubsystemVendorID)) || (device.SubsystemDeviceID != "" && !validPCIID(device.SubsystemDeviceID)) || (device.DeviceRevision != "" && !validPCIRevision(device.DeviceRevision)) || device.NUMANodeID < -1 || device.SRIOVEnabledVFs > device.SRIOVTotalVFs || !device.RelationshipState.Valid() || (device.RelationshipState == AvailabilityAvailable && device.RelationshipReason != "") || (device.RelationshipState != AvailabilityAvailable && device.RelationshipReason == "") || (device.PFAddress != "" && (!validPCIAddress(device.PFAddress) || device.VFIndex == nil)) || (device.PFAddress == "" && device.VFIndex != nil) || (index > 0 && fragment.PCI.Devices[index-1].Address == device.Address) {
				return errors.New("PCI inventory contains invalid or duplicate devices")
			}
		}
	case DomainNetwork:
		sort.Slice(fragment.Network.Interfaces, func(i, j int) bool { return fragment.Network.Interfaces[i].Name < fragment.Network.Interfaces[j].Name })
		for index, device := range fragment.Network.Interfaces {
			if device.Name == "" || device.MACAddress == "" || device.Driver == "" || device.NUMANodeID < -1 || (index > 0 && fragment.Network.Interfaces[index-1].Name == device.Name) {
				return errors.New("Network inventory contains invalid or duplicate interfaces")
			}
		}
	case DomainStorage:
		sort.Slice(fragment.Storage.Devices, func(i, j int) bool {
			return fragment.Storage.Devices[i].StableID < fragment.Storage.Devices[j].StableID
		})
		for index, device := range fragment.Storage.Devices {
			if device.StableID == "" || device.Kind == "" || device.SizeBytes == 0 || (index > 0 && fragment.Storage.Devices[index-1].StableID == device.StableID) {
				return errors.New("Storage inventory contains invalid or duplicate devices")
			}
		}
	case DomainVirtualization:
		sort.Strings(fragment.Virtualization.MachineTypes)
		if fragment.Virtualization.KVMAvailable && (fragment.Virtualization.LibvirtVersion == "" || fragment.Virtualization.QEMUVersion == "") {
			return errors.New("available KVM capability requires libvirt and QEMU versions")
		}
	}
	return nil
}

func validPCIID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 2 && hex.EncodeToString(decoded) == value
}

func validPCIRevision(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 1 && hex.EncodeToString(decoded) == value
}

func validPCIAddress(value string) bool {
	var domain, bus, slot, function uint64
	if _, err := fmt.Sscanf(value, "%04x:%02x:%02x.%1x", &domain, &bus, &slot, &function); err != nil {
		return false
	}
	return slot <= 0x1f && function <= 7 && fmt.Sprintf("%04x:%02x:%02x.%x", domain, bus, slot, function) == value
}

func capabilityIsAvailable(capabilities []Capability, name string) bool {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.State == AvailabilityAvailable
		}
	}
	return false
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
