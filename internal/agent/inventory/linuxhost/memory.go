package linuxhost

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

const (
	CapabilityMemory    = "kim.host.memory.v1"
	CapabilityNUMA      = "kim.host.numa.v1"
	CapabilityHugePages = "kim.host.hugepages.v1"
)

type MemoryModule struct {
	Adapter        Adapter
	ArtifactDigest string
}

func (module MemoryModule) Descriptor() inventory.ModuleDescriptor {
	return inventory.ModuleDescriptor{Name: "linux-memory", Version: "v1", Domain: inventory.DomainMemory, SchemaVersion: "kim.inventory.linux-memory/v1", ArtifactDigest: module.ArtifactDigest, CapabilityNames: []string{CapabilityMemory, CapabilityNUMA, CapabilityHugePages}}
}

func (module MemoryModule) Collect(ctx context.Context) (inventory.Fragment, error) {
	if err := ctx.Err(); err != nil {
		return inventory.Fragment{}, err
	}
	return normalizeMemory(module.Adapter.readMemory()), nil
}

func (adapter Adapter) readMemory() rawMemory {
	raw := rawMemory{MemoryState: inventory.AvailabilityAvailable, NUMAState: inventory.AvailabilityAvailable, HugePageState: inventory.AvailabilityAvailable}
	meminfo := adapter.read("proc/meminfo", inventory.AvailabilityUnknown)
	totalSource, availableSource := meminfo, meminfo
	if meminfo.State != inventory.AvailabilityAvailable {
		raw.MemoryState, raw.MemoryReason = inventory.AvailabilityUnknown, meminfo.ReasonCode
	} else {
		var ok bool
		raw.TotalBytes, ok = meminfoValue(meminfo.Value, "MemTotal")
		if !ok || raw.TotalBytes == 0 {
			raw.MemoryState, raw.MemoryReason = inventory.AvailabilityUnknown, "memtotal_not_reported"
			totalSource.State, totalSource.ReasonCode = inventory.AvailabilityUnknown, "field_not_reported"
		}
		if raw.AvailableBytes, ok = meminfoValue(meminfo.Value, "MemAvailable"); !ok {
			raw.MemoryState, raw.MemoryReason = inventory.AvailabilityUnknown, "memavailable_not_reported"
			availableSource.State, availableSource.ReasonCode = inventory.AvailabilityUnknown, "field_not_reported"
		}
	}
	raw.Evidence = append(raw.Evidence, evidence("/total_bytes", totalSource), evidence("/available_bytes", availableSource))

	nodeEntries, err := sortedEntries(adapter.FS, "sys/devices/system/node")
	if errors.Is(err, fs.ErrNotExist) {
		raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnsupported, "interface_not_present"
		raw.Evidence = append(raw.Evidence, inventory.EvidenceRef{Field: "/numa_nodes", SourcePath: "/sys/devices/system/node", State: raw.NUMAState, ReasonCode: raw.NUMAReason})
	} else if err != nil {
		raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnknown, classifyReadError(err)
		raw.Evidence = append(raw.Evidence, inventory.EvidenceRef{Field: "/numa_nodes", SourcePath: "/sys/devices/system/node", State: raw.NUMAState, ReasonCode: raw.NUMAReason})
	} else {
		for _, entry := range nodeEntries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "node") {
				continue
			}
			id, parseErr := strconv.Atoi(strings.TrimPrefix(entry.Name(), "node"))
			if parseErr != nil {
				continue
			}
			root := join("sys/devices/system/node", entry.Name())
			cpulist := adapter.read(join(root, "cpulist"), inventory.AvailabilityUnknown)
			nodeMeminfo := adapter.read(join(root, "meminfo"), inventory.AvailabilityUnknown)
			ids, listErr := parseCPUList(cpulist.Value)
			memoryBytes := nodeMemTotal(nodeMeminfo.Value)
			if listErr != nil {
				cpulist.State, cpulist.ReasonCode = inventory.AvailabilityUnknown, "parse_failed"
			}
			if nodeMeminfo.State == inventory.AvailabilityAvailable && memoryBytes == 0 {
				nodeMeminfo.State, nodeMeminfo.ReasonCode = inventory.AvailabilityUnknown, "field_not_reported"
			}
			if cpulist.State != inventory.AvailabilityAvailable || nodeMeminfo.State != inventory.AvailabilityAvailable {
				raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnknown, "numa_node_incomplete"
			}
			raw.Evidence = append(raw.Evidence, evidence("/numa_nodes/"+strconv.Itoa(id)+"/cpu_thread_ids", cpulist), evidence("/numa_nodes/"+strconv.Itoa(id)+"/memory_total_bytes", nodeMeminfo))
			raw.NUMANodes = append(raw.NUMANodes, rawNUMANode{LinuxID: id, CPUThreadIDs: ids, MemoryBytes: memoryBytes})
		}
		if len(raw.NUMANodes) == 0 {
			raw.NUMAState, raw.NUMAReason = inventory.AvailabilityUnsupported, "no_numa_nodes"
		}
	}

	entries, err := sortedEntries(adapter.FS, "sys/kernel/mm/hugepages")
	if errors.Is(err, fs.ErrNotExist) {
		raw.HugePageState, raw.HugePageReason = inventory.AvailabilityUnsupported, "interface_not_present"
		raw.Evidence = append(raw.Evidence, inventory.EvidenceRef{Field: "/hugepage_pools", SourcePath: "/sys/kernel/mm/hugepages", State: raw.HugePageState, ReasonCode: raw.HugePageReason})
	} else if err != nil {
		raw.HugePageState, raw.HugePageReason = inventory.AvailabilityUnknown, classifyReadError(err)
		raw.Evidence = append(raw.Evidence, inventory.EvidenceRef{Field: "/hugepage_pools", SourcePath: "/sys/kernel/mm/hugepages", State: raw.HugePageState, ReasonCode: raw.HugePageReason})
	} else {
		configured := false
		for _, entry := range entries {
			size, ok := hugePageSize(entry.Name())
			if !ok || !entry.IsDir() {
				continue
			}
			pool, complete := adapter.readHugePagePool(join("sys/kernel/mm/hugepages", entry.Name()), size, nil, &raw.Evidence)
			raw.HugePagePools = append(raw.HugePagePools, pool)
			configured = configured || pool.Total > 0
			if !complete {
				raw.HugePageState, raw.HugePageReason = inventory.AvailabilityUnknown, "hugepage_pool_incomplete"
			}
		}
		if raw.NUMAState == inventory.AvailabilityAvailable {
			for index := range raw.NUMANodes {
				node := &raw.NUMANodes[index]
				root := join("sys/devices/system/node", "node"+strconv.Itoa(node.LinuxID), "hugepages")
				nodePools, readErr := sortedEntries(adapter.FS, root)
				if readErr != nil {
					raw.HugePageState, raw.HugePageReason = inventory.AvailabilityUnknown, "numa_hugepages_not_observable"
					continue
				}
				for _, entry := range nodePools {
					size, ok := hugePageSize(entry.Name())
					if !ok || !entry.IsDir() {
						continue
					}
					pool, complete := adapter.readHugePagePool(join(root, entry.Name()), size, &node.LinuxID, &raw.Evidence)
					raw.HugePagePools = append(raw.HugePagePools, pool)
					configured = configured || pool.Total > 0
					if !complete {
						raw.HugePageState, raw.HugePageReason = inventory.AvailabilityUnknown, "hugepage_pool_incomplete"
					}
				}
			}
		}
		if raw.HugePageState == inventory.AvailabilityAvailable && !configured {
			raw.HugePageState, raw.HugePageReason = inventory.AvailabilityUnavailable, "no_pages_configured"
		}
	}
	return raw
}

func (adapter Adapter) readHugePagePool(root string, size uint64, nodeID *int, refs *[]inventory.EvidenceRef) (rawHugePagePool, bool) {
	values := make([]uint64, 4)
	complete := true
	for index, name := range []string{"nr_hugepages", "free_hugepages", "resv_hugepages", "surplus_hugepages"} {
		source := adapter.read(join(root, name), inventory.AvailabilityUnknown)
		value, parsed := parseUint(source)
		*refs = append(*refs, evidence("/hugepage_pools/"+strconv.FormatUint(size, 10)+"/"+name, parsed))
		values[index] = value
		complete = complete && parsed.State == inventory.AvailabilityAvailable
	}
	return rawHugePagePool{NUMANodeID: nodeID, PageSizeBytes: size, Total: values[0], Free: values[1], Reserved: values[2], Surplus: values[3]}, complete
}

func normalizeMemory(raw rawMemory) inventory.Fragment {
	nodes := make([]inventory.NUMANode, 0, len(raw.NUMANodes))
	for _, node := range raw.NUMANodes {
		nodes = append(nodes, inventory.NUMANode{LinuxID: node.LinuxID, CPUThreadIDs: node.CPUThreadIDs, MemoryTotalBytes: node.MemoryBytes})
	}
	pools := make([]inventory.HugePagePool, 0, len(raw.HugePagePools))
	for _, pool := range raw.HugePagePools {
		pools = append(pools, inventory.HugePagePool{NUMANodeID: pool.NUMANodeID, PageSizeBytes: pool.PageSizeBytes, TotalPages: pool.Total, FreePages: pool.Free, ReservedPages: pool.Reserved, SurplusPages: pool.Surplus})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].LinuxID < nodes[j].LinuxID })
	return inventory.Fragment{Domain: inventory.DomainMemory, Evidence: raw.Evidence, Capabilities: []inventory.Capability{
		capability(CapabilityMemory, raw.MemoryState, raw.MemoryReason),
		capability(CapabilityNUMA, raw.NUMAState, raw.NUMAReason),
		capability(CapabilityHugePages, raw.HugePageState, raw.HugePageReason),
	}, Memory: &inventory.Memory{TotalBytes: raw.TotalBytes, AvailableBytes: raw.AvailableBytes, NUMANodes: nodes, HugePagePools: pools}}
}
