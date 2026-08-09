// Package linuxhost implements the Linux OS Integration Adapter for normalized
// CPU, NUMA, memory, and HugePage Host inventory.
package linuxhost

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

type Adapter struct {
	FS           fs.FS
	Architecture string
}

type observation[T any] struct {
	Value      T
	State      inventory.Availability
	ReasonCode string
	Path       string
}

type rawCPUThread struct {
	LinuxID, CoreID, SocketID, NUMANodeID int
	Online                                bool
}

type rawCompute struct {
	Architecture   string
	CPUModel       observation[string]
	Threads        []rawCPUThread
	TopologyState  inventory.Availability
	TopologyReason string
	Isolated       observation[[]int]
	Evidence       []inventory.EvidenceRef
}

type rawNUMANode struct {
	LinuxID      int
	CPUThreadIDs []int
	MemoryBytes  uint64
}

type rawHugePagePool struct {
	NUMANodeID                 *int
	PageSizeBytes, Total, Free uint64
	Reserved, Surplus          uint64
}

type rawMemory struct {
	TotalBytes, AvailableBytes uint64
	MemoryState                inventory.Availability
	MemoryReason               string
	NUMANodes                  []rawNUMANode
	NUMAState                  inventory.Availability
	NUMAReason                 string
	HugePagePools              []rawHugePagePool
	HugePageState              inventory.Availability
	HugePageReason             string
	Evidence                   []inventory.EvidenceRef
}

func (adapter Adapter) read(pathName string, missing inventory.Availability) observation[string] {
	if adapter.FS == nil {
		return observation[string]{State: inventory.AvailabilityUnknown, ReasonCode: "filesystem_not_configured", Path: "/" + pathName}
	}
	data, err := fs.ReadFile(adapter.FS, pathName)
	if err == nil {
		return observation[string]{Value: strings.TrimSpace(string(data)), State: inventory.AvailabilityAvailable, Path: "/" + pathName}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return observation[string]{State: missing, ReasonCode: "interface_not_present", Path: "/" + pathName}
	}
	if errors.Is(err, fs.ErrPermission) {
		return observation[string]{State: inventory.AvailabilityUnknown, ReasonCode: "permission_denied", Path: "/" + pathName}
	}
	return observation[string]{State: inventory.AvailabilityUnknown, ReasonCode: "read_failed", Path: "/" + pathName}
}

func (adapter Adapter) readLink(pathName string, missing inventory.Availability) observation[string] {
	if adapter.FS == nil {
		return observation[string]{State: inventory.AvailabilityUnknown, ReasonCode: "filesystem_not_configured", Path: "/" + pathName}
	}
	target, err := fs.ReadLink(adapter.FS, pathName)
	if err == nil {
		return observation[string]{Value: target, State: inventory.AvailabilityAvailable, Path: "/" + pathName}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return observation[string]{State: missing, ReasonCode: "interface_not_present", Path: "/" + pathName}
	}
	if errors.Is(err, fs.ErrPermission) {
		return observation[string]{State: inventory.AvailabilityUnknown, ReasonCode: "permission_denied", Path: "/" + pathName}
	}
	return observation[string]{State: inventory.AvailabilityUnknown, ReasonCode: "read_failed", Path: "/" + pathName}
}

func evidence(field string, source observation[string]) inventory.EvidenceRef {
	return inventory.EvidenceRef{Field: field, SourcePath: source.Path, State: source.State, ReasonCode: source.ReasonCode}
}

func parseCPUList(value string) ([]int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []int{}, nil
	}
	seen := map[int]struct{}{}
	result := []int{}
	for _, item := range strings.Split(value, ",") {
		bounds := strings.SplitN(strings.TrimSpace(item), "-", 2)
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 0 {
			return nil, fmt.Errorf("invalid CPU list %q", value)
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(bounds[1])
			if err != nil || end < start {
				return nil, fmt.Errorf("invalid CPU list %q", value)
			}
		}
		for id := start; id <= end; id++ {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		}
	}
	sort.Ints(result)
	return result, nil
}

func parseUint(source observation[string]) (uint64, observation[string]) {
	if source.State != inventory.AvailabilityAvailable {
		return 0, source
	}
	value, err := strconv.ParseUint(source.Value, 10, 64)
	if err != nil {
		source.State, source.ReasonCode = inventory.AvailabilityUnknown, "parse_failed"
		return 0, source
	}
	return value, source
}

func parseInt(source observation[string]) (int, observation[string]) {
	value, parsed := parseUint(source)
	if parsed.State != inventory.AvailabilityAvailable {
		return 0, parsed
	}
	if value > uint64(^uint(0)>>1) {
		parsed.State, parsed.ReasonCode = inventory.AvailabilityUnknown, "parse_overflow"
		return 0, parsed
	}
	return int(value), parsed
}

func cpuID(name string) (int, bool) {
	if !strings.HasPrefix(name, "cpu") || len(name) == 3 {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
	return id, err == nil && id >= 0
}

func hugePageSize(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "hugepages-") || !strings.HasSuffix(name, "kB") {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, "hugepages-"), "kB"), 10, 64)
	return value * 1024, err == nil && value > 0
}

func meminfoValue(content, key string) (uint64, bool) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimSuffix(fields[0], ":") == key {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			return value * 1024, err == nil
		}
	}
	return 0, false
}

func nodeMemTotal(content string) uint64 {
	for _, line := range strings.Split(content, "\n") {
		if index := strings.Index(line, "MemTotal:"); index >= 0 {
			if value, ok := meminfoValue(line[index:], "MemTotal"); ok {
				return value
			}
		}
	}
	return 0
}

func sortedEntries(fsys fs.FS, name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(fsys, name)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func join(parts ...string) string { return path.Join(parts...) }
