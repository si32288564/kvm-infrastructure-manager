package linuxhost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

const (
	CapabilityCPUTopology = "kim.host.cpu-topology.v1"
	CapabilityCPUModel    = "kim.host.cpu-model.v1"
	CapabilityIsolation   = "kim.host.cpu-isolation.v1"
)

type ComputeModule struct {
	Adapter        Adapter
	ArtifactDigest string
}

func (module ComputeModule) Descriptor() inventory.ModuleDescriptor {
	return inventory.ModuleDescriptor{Name: "linux-compute", Version: "v1", Domain: inventory.DomainCompute, SchemaVersion: "kim.inventory.linux-compute/v1", ArtifactDigest: module.ArtifactDigest, CapabilityNames: []string{CapabilityCPUTopology, CapabilityCPUModel, CapabilityIsolation}}
}

func (module ComputeModule) Collect(ctx context.Context) (inventory.Fragment, error) {
	if err := ctx.Err(); err != nil {
		return inventory.Fragment{}, err
	}
	raw := module.Adapter.readCompute()
	return normalizeCompute(raw), nil
}

func (adapter Adapter) readCompute() rawCompute {
	raw := rawCompute{Architecture: adapter.Architecture, TopologyState: inventory.AvailabilityAvailable}
	if raw.Architecture == "" {
		raw.TopologyState, raw.TopologyReason = inventory.AvailabilityUnknown, "architecture_not_configured"
	}
	model := adapter.read("proc/cpuinfo", inventory.AvailabilityUnknown)
	if model.State == inventory.AvailabilityAvailable {
		found := false
		for _, line := range strings.Split(model.Value, "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && (strings.TrimSpace(parts[0]) == "model name" || strings.TrimSpace(parts[0]) == "Processor") {
				model.Value = strings.TrimSpace(parts[1])
				found = true
				break
			}
		}
		if !found || model.Value == "" {
			model.Value, model.State, model.ReasonCode = "", inventory.AvailabilityUnavailable, "cpu_model_not_reported"
		}
	}
	raw.CPUModel = model
	raw.Evidence = append(raw.Evidence, evidence("/cpu_model", model))

	nodeByCPU := map[int]int{}
	if entries, err := sortedEntries(adapter.FS, "sys/devices/system/node"); err == nil {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "node") {
				continue
			}
			nodeID, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "node"))
			if err != nil {
				continue
			}
			cpus := adapter.read(join("sys/devices/system/node", entry.Name(), "cpulist"), inventory.AvailabilityUnknown)
			ids, parseErr := parseCPUList(cpus.Value)
			if cpus.State != inventory.AvailabilityAvailable || parseErr != nil {
				continue
			}
			for _, id := range ids {
				nodeByCPU[id] = nodeID
			}
		}
	}

	entries, err := sortedEntries(adapter.FS, "sys/devices/system/cpu")
	if err != nil {
		raw.TopologyState, raw.TopologyReason = inventory.AvailabilityUnknown, classifyReadError(err)
		return raw
	}
	for _, entry := range entries {
		id, ok := cpuID(entry.Name())
		if !ok || !entry.IsDir() {
			continue
		}
		root := join("sys/devices/system/cpu", entry.Name())
		coreSource := adapter.read(join(root, "topology/core_id"), inventory.AvailabilityUnknown)
		socketSource := adapter.read(join(root, "topology/physical_package_id"), inventory.AvailabilityUnknown)
		coreID, coreSource := parseInt(coreSource)
		socketID, socketSource := parseInt(socketSource)
		raw.Evidence = append(raw.Evidence, evidence(fmt.Sprintf("/threads/%d/core_id", id), coreSource), evidence(fmt.Sprintf("/threads/%d/socket_id", id), socketSource))
		if coreSource.State != inventory.AvailabilityAvailable || socketSource.State != inventory.AvailabilityAvailable {
			raw.TopologyState, raw.TopologyReason = inventory.AvailabilityUnknown, "cpu_topology_incomplete"
			continue
		}
		online := true
		onlineSource := adapter.read(join(root, "online"), inventory.AvailabilityUnsupported)
		if onlineSource.State == inventory.AvailabilityAvailable {
			online = onlineSource.Value == "1"
		} else if onlineSource.State == inventory.AvailabilityUnknown {
			raw.TopologyState, raw.TopologyReason = inventory.AvailabilityUnknown, "cpu_online_unknown"
		}
		if onlineSource.State == inventory.AvailabilityUnsupported {
			onlineSource = observation[string]{State: inventory.AvailabilityAvailable, Path: "/" + root}
		}
		raw.Evidence = append(raw.Evidence, evidence(fmt.Sprintf("/threads/%d/online", id), onlineSource))
		nodeID := -1
		if value, exists := nodeByCPU[id]; exists {
			nodeID = value
		}
		raw.Threads = append(raw.Threads, rawCPUThread{LinuxID: id, CoreID: coreID, SocketID: socketID, NUMANodeID: nodeID, Online: online})
	}
	if len(raw.Threads) == 0 {
		raw.TopologyState, raw.TopologyReason = inventory.AvailabilityUnknown, "cpu_topology_empty"
	}
	isolatedSource := adapter.read("sys/devices/system/cpu/isolated", inventory.AvailabilityUnsupported)
	isolated := observation[[]int]{State: isolatedSource.State, ReasonCode: isolatedSource.ReasonCode, Path: isolatedSource.Path}
	if isolatedSource.State == inventory.AvailabilityAvailable {
		ids, err := parseCPUList(isolatedSource.Value)
		if err != nil {
			isolated.State, isolated.ReasonCode = inventory.AvailabilityUnknown, "parse_failed"
		} else {
			isolated.Value = ids
		}
	}
	raw.Isolated = isolated
	raw.Evidence = append(raw.Evidence, inventory.EvidenceRef{Field: "/isolated_cpu_ids", SourcePath: isolated.Path, State: isolated.State, ReasonCode: isolated.ReasonCode})
	return raw
}

func normalizeCompute(raw rawCompute) inventory.Fragment {
	isolated := map[int]struct{}{}
	for _, id := range raw.Isolated.Value {
		isolated[id] = struct{}{}
	}
	threads := make([]inventory.CPUThread, 0, len(raw.Threads))
	for _, thread := range raw.Threads {
		_, isIsolated := isolated[thread.LinuxID]
		threads = append(threads, inventory.CPUThread{LinuxID: thread.LinuxID, CoreID: thread.CoreID, SocketID: thread.SocketID, NUMANodeID: thread.NUMANodeID, Online: thread.Online, Isolated: isIsolated})
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i].LinuxID < threads[j].LinuxID })
	return inventory.Fragment{Domain: inventory.DomainCompute, Evidence: raw.Evidence, Capabilities: []inventory.Capability{
		capability(CapabilityCPUTopology, raw.TopologyState, raw.TopologyReason),
		capability(CapabilityCPUModel, raw.CPUModel.State, raw.CPUModel.ReasonCode),
		capability(CapabilityIsolation, raw.Isolated.State, raw.Isolated.ReasonCode),
	}, Compute: &inventory.Compute{Architecture: raw.Architecture, CPUModel: raw.CPUModel.Value, Threads: threads}}
}

func capability(name string, state inventory.Availability, reason string) inventory.Capability {
	if state == "" {
		state, reason = inventory.AvailabilityUnknown, "observation_missing"
	}
	return inventory.Capability{Name: name, Version: "v1", State: state, ReasonCode: reason}
}

func classifyReadError(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "interface_not_present"
	}
	return "read_failed"
}
