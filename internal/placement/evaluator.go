// Package placement implements side-effect-free eligibility, scoring, and
// selection over immutable authority snapshots.
package placement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

type Request struct {
	RequestID, ProjectID, WorkloadID, ImageID, FlavorID, PoolID string
	ImageRevision, FlavorRevision                               uint64
	FlavorShapeDigest                                           string
	VCPUs, MemoryMiB, RootDiskGiB                               uint64
	NUMAPolicy, CPUAllocation                                   string
	NUMANodes                                                   *uint32
	HugePageSizeKiB                                             *uint64
	CPUPinning                                                  bool
	CatalogAccessAllowed                                        bool
	ExtraSpecs                                                  map[string]string
}

type AuthoritySnapshot struct {
	DatabaseMode                                                            string
	HostID, PoolID                                                          string
	PoolGeneration, MembershipGeneration                                    uint64
	PoolPolicyID                                                            string
	PoolPolicyGeneration                                                    uint64
	PoolState, MembershipState                                              string
	CapabilityGeneration                                                    uint64
	ReadinessCapabilityGeneration                                           uint64
	CapabilityState, ReadinessState, PreflightState, ComplianceState        string
	BaselineAssignmentGeneration, PreflightGeneration, ComplianceGeneration uint64
	Inventory                                                               agentinventory.Snapshot
	ClaimedVCPUs, ClaimedMemoryMiB                                          uint64
	ClaimedHugePages                                                        map[uint64]uint64
}

type RequiredClaim struct {
	VCPUs, MemoryMiB uint64
	HugePageSizeKiB  *uint64
	HugePagePages    uint64
	CPUAllocation    string
}

type Evaluation struct {
	RequestID, RequestDigest, HostID, PoolID  string
	ImageID, FlavorID                         string
	ImageRevision, FlavorRevision             uint64
	FlavorShapeDigest                         string
	Eligible                                  bool
	ReasonCodes                               []string
	Score                                     int64
	EvaluationDigest                          string
	PoolGeneration, MembershipGeneration      uint64
	PoolPolicyID                              string
	PoolPolicyGeneration                      uint64
	CapabilityGeneration                      uint64
	BaselineAssignmentGeneration              uint64
	PreflightGeneration, ComplianceGeneration uint64
	RequiredClaim                             RequiredClaim
}

func Evaluate(request Request, authority AuthoritySnapshot) (Evaluation, error) {
	requestDigest, err := digest(request)
	if err != nil {
		return Evaluation{}, err
	}
	evaluation := Evaluation{
		RequestID: request.RequestID, RequestDigest: requestDigest,
		HostID: authority.HostID, PoolID: authority.PoolID,
		ImageID: request.ImageID, ImageRevision: request.ImageRevision,
		FlavorID: request.FlavorID, FlavorRevision: request.FlavorRevision,
		FlavorShapeDigest: request.FlavorShapeDigest,
		PoolGeneration:    authority.PoolGeneration, MembershipGeneration: authority.MembershipGeneration,
		PoolPolicyID: authority.PoolPolicyID, PoolPolicyGeneration: authority.PoolPolicyGeneration,
		CapabilityGeneration:         authority.CapabilityGeneration,
		BaselineAssignmentGeneration: authority.BaselineAssignmentGeneration,
		PreflightGeneration:          authority.PreflightGeneration, ComplianceGeneration: authority.ComplianceGeneration,
		RequiredClaim: RequiredClaim{VCPUs: request.VCPUs, MemoryMiB: request.MemoryMiB, HugePageSizeKiB: request.HugePageSizeKiB, CPUAllocation: request.CPUAllocation},
	}
	addReason := func(condition bool, reason string) {
		if condition {
			evaluation.ReasonCodes = append(evaluation.ReasonCodes, reason)
		}
	}
	addReason(authority.DatabaseMode != "ACTIVE", "database_authority_not_active")
	addReason(request.PoolID == "" || authority.PoolID != request.PoolID, "placement_pool_mismatch")
	addReason(authority.PoolState != "ACTIVE", "placement_pool_not_active")
	addReason(authority.MembershipState != "ACTIVE", "placement_membership_not_active")
	addReason(authority.CapabilityState != "CURRENT" || authority.Inventory.ObservationGeneration != authority.CapabilityGeneration, "host_capability_not_current")
	addReason(authority.ReadinessCapabilityGeneration != authority.CapabilityGeneration, "readiness_capability_generation_stale")
	addReason(authority.ReadinessState != "READY" || authority.PreflightState != "PASSED" || authority.ComplianceState != "COMPLIANT", "host_readiness_not_eligible")
	addReason(!request.CatalogAccessAllowed, "catalog_access_denied")

	capacity, capacityReasons := extractCapacity(authority.Inventory, request)
	evaluation.ReasonCodes = append(evaluation.ReasonCodes, capacityReasons...)
	availableVCPUs := saturatingSubtract(capacity.VCPUs, authority.ClaimedVCPUs)
	if request.CPUPinning || request.CPUAllocation == "DEDICATED" {
		availableVCPUs = saturatingSubtract(capacity.IsolatedVCPUs, authority.ClaimedVCPUs)
	}
	addReason(availableVCPUs < request.VCPUs, "insufficient_vcpu_capacity")
	ledgerMemory := saturatingSubtract(capacity.MemoryMiB, authority.ClaimedMemoryMiB)
	if capacity.ObservedAvailableMemoryMiB < ledgerMemory {
		ledgerMemory = capacity.ObservedAvailableMemoryMiB
	}
	addReason(ledgerMemory < request.MemoryMiB, "insufficient_memory_capacity")
	if request.NUMAPolicy == "REQUIRED" && request.NUMANodes != nil {
		addReason(capacity.NUMANodes < uint64(*request.NUMANodes), "insufficient_numa_nodes")
	}
	if request.HugePageSizeKiB != nil {
		pageSize := *request.HugePageSizeKiB
		requiredPages := ceilDivide(request.MemoryMiB*1024, pageSize)
		evaluation.RequiredClaim.HugePagePages = requiredPages
		ledgerPages := saturatingSubtract(capacity.HugePageTotal[pageSize], authority.ClaimedHugePages[pageSize])
		if capacity.HugePageFree[pageSize] < ledgerPages {
			ledgerPages = capacity.HugePageFree[pageSize]
		}
		addReason(ledgerPages < requiredPages, "insufficient_hugepage_capacity")
	}
	sort.Strings(evaluation.ReasonCodes)
	evaluation.Eligible = len(evaluation.ReasonCodes) == 0
	if evaluation.Eligible {
		remainingCPU := availableVCPUs - request.VCPUs
		remainingMemoryGiB := (ledgerMemory - request.MemoryMiB) / 1024
		evaluation.Score = int64(remainingCPU*1000 + remainingMemoryGiB)
	}
	evaluation.EvaluationDigest, err = digest(evaluationWithoutDigest(evaluation))
	return evaluation, err
}

// Select ranks only eligible candidates. Host ID is the stable tie breaker.
func Select(evaluations []Evaluation) []Evaluation {
	selected := make([]Evaluation, 0, len(evaluations))
	for _, evaluation := range evaluations {
		if evaluation.Eligible {
			selected = append(selected, evaluation)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Score != selected[j].Score {
			return selected[i].Score > selected[j].Score
		}
		return selected[i].HostID < selected[j].HostID
	})
	return selected
}

type hostCapacity struct {
	VCPUs, IsolatedVCPUs, MemoryMiB, ObservedAvailableMemoryMiB, NUMANodes uint64
	HugePageTotal, HugePageFree                                            map[uint64]uint64
}

func extractCapacity(snapshot agentinventory.Snapshot, request Request) (hostCapacity, []string) {
	capacity := hostCapacity{HugePageTotal: map[uint64]uint64{}, HugePageFree: map[uint64]uint64{}}
	reasons := make([]string, 0)
	computeSeen, memorySeen := false, false
	for _, fragment := range snapshot.Fragments {
		switch {
		case fragment.Compute != nil:
			if !capabilityAvailable(fragment.Capabilities, "kim.host.cpu-topology.v1") {
				continue
			}
			computeSeen = true
			for _, thread := range fragment.Compute.Threads {
				if thread.Online {
					capacity.VCPUs++
					if thread.Isolated {
						capacity.IsolatedVCPUs++
					}
				}
			}
		case fragment.Memory != nil:
			if !capabilityAvailable(fragment.Capabilities, "kim.host.memory.v1") {
				continue
			}
			memorySeen = true
			capacity.MemoryMiB = fragment.Memory.TotalBytes / (1024 * 1024)
			capacity.ObservedAvailableMemoryMiB = fragment.Memory.AvailableBytes / (1024 * 1024)
			capacity.NUMANodes = uint64(len(fragment.Memory.NUMANodes))
			for _, pool := range fragment.Memory.HugePagePools {
				sizeKiB := pool.PageSizeBytes / 1024
				capacity.HugePageTotal[sizeKiB] += pool.TotalPages
				capacity.HugePageFree[sizeKiB] += pool.FreePages
			}
		}
	}
	if !computeSeen {
		reasons = append(reasons, "cpu_topology_unavailable")
	}
	if !memorySeen {
		reasons = append(reasons, "memory_topology_unavailable")
	}
	return capacity, reasons
}

func capabilityAvailable(capabilities []agentinventory.Capability, name string) bool {
	for _, capability := range capabilities {
		if capability.Name == name && capability.State == agentinventory.AvailabilityAvailable {
			return true
		}
	}
	return false
}

func digest(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal placement fingerprint: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func evaluationWithoutDigest(evaluation Evaluation) Evaluation {
	evaluation.EvaluationDigest = ""
	return evaluation
}

func saturatingSubtract(total, used uint64) uint64 {
	if used >= total {
		return 0
	}
	return total - used
}

func ceilDivide(value, divisor uint64) uint64 {
	if divisor == 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}
