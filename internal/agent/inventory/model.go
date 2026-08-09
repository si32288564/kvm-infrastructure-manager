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

const SnapshotSchemaV1 = "kim.inventory.snapshot/v1"

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
	Available   bool         `json:"available"`
	Constraints []Constraint `json:"constraints,omitempty"`
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
}

type Memory struct {
	TotalBytes    uint64         `json:"total_bytes"`
	NUMANodes     []NUMANode     `json:"numa_nodes"`
	HugePagePools []HugePagePool `json:"hugepage_pools"`
}

type PCIDevice struct {
	Address         string `json:"address"`
	VendorID        string `json:"vendor_id"`
	DeviceID        string `json:"device_id"`
	IOMMUGroup      string `json:"iommu_group,omitempty"`
	NUMANodeID      int    `json:"numa_node_id"`
	SRIOVTotalVFs   uint32 `json:"sriov_total_vfs"`
	SRIOVEnabledVFs uint32 `json:"sriov_enabled_vfs"`
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
	if snapshot.SchemaVersion != SnapshotSchemaV1 || snapshot.HostIdentity == "" || snapshot.ObservationGeneration == 0 {
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
	for index := range fragment.Capabilities {
		capability := &fragment.Capabilities[index]
		if capability.Name == "" || capability.Version == "" {
			return errors.New("Host capability name and version are required")
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
		if fragment.Compute.Architecture == "" || fragment.Compute.CPUModel == "" || len(fragment.Compute.Threads) == 0 {
			return errors.New("Compute inventory requires architecture, CPU model, and threads")
		}
		sort.Slice(fragment.Compute.Threads, func(i, j int) bool { return fragment.Compute.Threads[i].LinuxID < fragment.Compute.Threads[j].LinuxID })
		for index, thread := range fragment.Compute.Threads {
			if thread.LinuxID < 0 || thread.CoreID < 0 || thread.SocketID < 0 || thread.NUMANodeID < 0 || (index > 0 && fragment.Compute.Threads[index-1].LinuxID == thread.LinuxID) {
				return errors.New("Compute thread topology contains invalid or duplicate IDs")
			}
		}
	case DomainMemory:
		if fragment.Memory.TotalBytes == 0 {
			return errors.New("Memory inventory total bytes must be positive")
		}
		sort.Slice(fragment.Memory.NUMANodes, func(i, j int) bool {
			return fragment.Memory.NUMANodes[i].LinuxID < fragment.Memory.NUMANodes[j].LinuxID
		})
		for index := range fragment.Memory.NUMANodes {
			if fragment.Memory.NUMANodes[index].LinuxID < 0 || fragment.Memory.NUMANodes[index].MemoryTotalBytes == 0 || (index > 0 && fragment.Memory.NUMANodes[index-1].LinuxID == fragment.Memory.NUMANodes[index].LinuxID) {
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
			if pool.PageSizeBytes == 0 || pool.FreePages > pool.TotalPages || (pool.NUMANodeID != nil && *pool.NUMANodeID < 0) {
				return errors.New("HugePage pool contains invalid capacity or NUMA locality")
			}
		}
	case DomainPCI:
		sort.Slice(fragment.PCI.Devices, func(i, j int) bool { return fragment.PCI.Devices[i].Address < fragment.PCI.Devices[j].Address })
		for index, device := range fragment.PCI.Devices {
			if device.Address == "" || device.VendorID == "" || device.DeviceID == "" || device.NUMANodeID < -1 || device.SRIOVEnabledVFs > device.SRIOVTotalVFs || (index > 0 && fragment.PCI.Devices[index-1].Address == device.Address) {
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

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
