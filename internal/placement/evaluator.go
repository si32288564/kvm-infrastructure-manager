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
	PCI                                                         []PCIRequirement
	Network                                                     []NetworkRequirement
	Storage                                                     []StorageRequirement
}

type PCIRequirement struct {
	DeviceAddress, PolicyID, QualificationID, RequiredIOMMUGroup string
	PolicyGeneration, QualificationRevision                      uint64
	RequiredNUMANodeID                                           *int
}

type NetworkRequirement struct {
	PortID, NetworkID, SubnetID, SegmentClaimID            string
	IPAddress, MACAddress, BindingType, DeviceAddress      string
	AllocationSource                                       string
	NetworkGeneration, SubnetGeneration, SegmentGeneration uint64
	HostMappingGeneration                                  uint64
	RequiredMTU                                            uint32
}

type StorageRequirement struct {
	VolumeID, AttachmentID, BackendID, VGUUID, StorageClassID string
	BackendGeneration, StorageClassRevision                   uint64
	CapacityGeneration, AttachmentGeneration                  uint64
	FencingPolicyRevision                                     uint64
	SizeBytes                                                 uint64
	AccessMode                                                string
	Bootable                                                  bool
}

type PCIDeviceAuthority struct {
	DeviceAddress, ObservationState, RelationshipState, PFAddress, IOMMUGroup string
	NUMANodeID, VFIndex                                                       int
	ObservationGeneration                                                     uint64
	QualificationID, BindingState, BindingProfile                             string
	QualificationRevision, BindingGeneration                                  uint64
	PolicyID, PolicyState, PolicyProfile                                      string
	PolicyGeneration                                                          uint64
	AssignmentQualified                                                       bool
	ActiveClaim                                                               bool
}

type NetworkAuthority struct {
	PortID, NetworkID, NetworkState, NetworkProjectID      string
	SubnetID, SubnetState, SegmentClaimID, SegmentState    string
	MappingState                                           string
	NetworkGeneration, SubnetGeneration, SegmentGeneration uint64
	HostMappingGeneration                                  uint64
	NetworkMTU, MappingMaximumMTU                          uint32
	IPAddressAllowed, IdentityConflict, PortConflict       bool
	BindingSupported                                       bool
}

type StorageAuthority struct {
	VolumeID, AttachmentID, BackendID, BackendType, BackendState string
	BackendHostID, VGUUID, CapabilityState, SupportTier          string
	StorageClassID, StorageClassState, AllowedBackendType        string
	Locality, CapacityState, HealthState                         string
	BackendGeneration, BackendCapabilityGeneration               uint64
	StorageClassRevision, FencingPolicyRevision                  uint64
	CapacityGeneration                                           uint64
	TotalBytes, ObservedFreeBytes, ExternalOrUnknownBytes        uint64
	HardReserveBytes, ClaimedBytes                               uint64
	SingleWriterAllowed                                          bool
	ThinProvisioning, EncryptionRequired                         bool
	VolumeConflict, AttachmentConflict                           bool
}

type AuthoritySnapshot struct {
	DatabaseMode                                                            string
	HostID, PoolID                                                          string
	PoolGeneration, MembershipSetGeneration, MembershipGeneration           uint64
	HierarchyID                                                             string
	HierarchyGeneration                                                     uint64
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
	PCIDevices                                                              map[string]PCIDeviceAuthority
	Networks                                                                map[string]NetworkAuthority
	Storage                                                                 map[string]StorageAuthority
}

type RequiredClaim struct {
	VCPUs, MemoryMiB uint64
	HugePageSizeKiB  *uint64
	HugePagePages    uint64
	CPUAllocation    string
}

type Evaluation struct {
	RequestID, RequestDigest, HostID, PoolID                      string
	ImageID, FlavorID                                             string
	ImageRevision, FlavorRevision                                 uint64
	FlavorShapeDigest                                             string
	Eligible                                                      bool
	ReasonCodes                                                   []string
	Score                                                         int64
	EvaluationDigest                                              string
	PoolGeneration, MembershipSetGeneration, MembershipGeneration uint64
	HierarchyID                                                   string
	HierarchyGeneration                                           uint64
	PoolPolicyID                                                  string
	PoolPolicyGeneration                                          uint64
	CapabilityGeneration                                          uint64
	BaselineAssignmentGeneration                                  uint64
	PreflightGeneration, ComplianceGeneration                     uint64
	RequiredClaim                                                 RequiredClaim
}

func Evaluate(request Request, authority AuthoritySnapshot) (Evaluation, error) {
	request.PCI = append([]PCIRequirement(nil), request.PCI...)
	sort.Slice(request.PCI, func(i, j int) bool { return request.PCI[i].DeviceAddress < request.PCI[j].DeviceAddress })
	for index, required := range request.PCI {
		if required.DeviceAddress == "" || required.PolicyID == "" || required.PolicyGeneration == 0 || required.QualificationID == "" || required.QualificationRevision == 0 || (index > 0 && request.PCI[index-1].DeviceAddress == required.DeviceAddress) {
			return Evaluation{}, fmt.Errorf("invalid or duplicate PCI requirement for device %q", required.DeviceAddress)
		}
	}
	request.Network = append([]NetworkRequirement(nil), request.Network...)
	sort.Slice(request.Network, func(i, j int) bool { return request.Network[i].PortID < request.Network[j].PortID })
	ipIdentities, macIdentities := map[string]struct{}{}, map[string]struct{}{}
	for index, required := range request.Network {
		allocationSource := required.AllocationSource
		if allocationSource == "" {
			allocationSource = "EXPLICIT"
		}
		identitiesValid := (allocationSource == "EXPLICIT" && required.IPAddress != "" && required.MACAddress != "") || (allocationSource == "AUTOMATIC" && required.IPAddress == "" && required.MACAddress == "")
		if required.PortID == "" || required.NetworkID == "" || required.NetworkGeneration == 0 || required.SubnetID == "" || required.SubnetGeneration == 0 || required.SegmentClaimID == "" || required.SegmentGeneration == 0 || required.HostMappingGeneration == 0 || !identitiesValid || required.RequiredMTU < 576 || (required.BindingType != "OVS" && required.BindingType != "SRIOV_DIRECT") || (index > 0 && request.Network[index-1].PortID == required.PortID) {
			return Evaluation{}, fmt.Errorf("invalid or duplicate Network requirement for Port %q", required.PortID)
		}
		if required.BindingType == "OVS" && required.DeviceAddress != "" {
			return Evaluation{}, fmt.Errorf("OVS Port %q cannot request a PCI device", required.PortID)
		}
		if required.BindingType == "SRIOV_DIRECT" {
			matched := false
			for _, pci := range request.PCI {
				matched = matched || pci.DeviceAddress == required.DeviceAddress
			}
			if required.DeviceAddress == "" || !matched {
				return Evaluation{}, fmt.Errorf("SR-IOV Port %q requires the same qualified PCI device", required.PortID)
			}
		}
		if allocationSource == "EXPLICIT" {
			ipKey, macKey := required.SubnetID+"/"+required.IPAddress, required.NetworkID+"/"+required.MACAddress
			if _, duplicate := ipIdentities[ipKey]; duplicate {
				return Evaluation{}, fmt.Errorf("duplicate IP identity %q in Placement request", required.IPAddress)
			}
			if _, duplicate := macIdentities[macKey]; duplicate {
				return Evaluation{}, fmt.Errorf("duplicate MAC identity %q in Placement request", required.MACAddress)
			}
			ipIdentities[ipKey], macIdentities[macKey] = struct{}{}, struct{}{}
		}
	}
	request.Storage = append([]StorageRequirement(nil), request.Storage...)
	sort.Slice(request.Storage, func(i, j int) bool { return request.Storage[i].VolumeID < request.Storage[j].VolumeID })
	attachments := map[string]struct{}{}
	for index, required := range request.Storage {
		if required.VolumeID == "" || required.AttachmentID == "" || required.BackendID == "" || required.VGUUID == "" || required.BackendGeneration == 0 || required.StorageClassID == "" || required.StorageClassRevision == 0 || required.CapacityGeneration == 0 || required.AttachmentGeneration == 0 || required.FencingPolicyRevision == 0 || required.SizeBytes == 0 || required.AccessMode != "SINGLE_WRITER" || (index > 0 && request.Storage[index-1].VolumeID == required.VolumeID) {
			return Evaluation{}, fmt.Errorf("invalid or duplicate Storage requirement for Volume %q", required.VolumeID)
		}
		if _, duplicate := attachments[required.AttachmentID]; duplicate {
			return Evaluation{}, fmt.Errorf("duplicate Storage Attachment %q", required.AttachmentID)
		}
		attachments[required.AttachmentID] = struct{}{}
	}
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
		PoolGeneration:    authority.PoolGeneration, MembershipSetGeneration: authority.MembershipSetGeneration,
		MembershipGeneration: authority.MembershipGeneration,
		HierarchyID:          authority.HierarchyID, HierarchyGeneration: authority.HierarchyGeneration,
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
	for _, required := range request.PCI {
		device, present := authority.PCIDevices[required.DeviceAddress]
		prefix := "pci:" + required.DeviceAddress + ":"
		addReason(!present, prefix+"observation_missing")
		if !present {
			continue
		}
		addReason(device.ObservationGeneration != authority.CapabilityGeneration || device.ObservationState != "AVAILABLE" || device.RelationshipState != "AVAILABLE" || device.PFAddress == "" || device.VFIndex < 0, prefix+"observation_not_eligible")
		addReason(device.BindingState != "CURRENT" || device.BindingGeneration != authority.CapabilityGeneration || device.QualificationID != required.QualificationID || device.QualificationRevision != required.QualificationRevision || !device.AssignmentQualified, prefix+"qualification_not_current")
		addReason(device.PolicyID != required.PolicyID || device.PolicyState != "ALLOWED" || device.PolicyGeneration != required.PolicyGeneration || device.PolicyProfile != device.BindingProfile, prefix+"policy_not_allowed")
		addReason(required.RequiredNUMANodeID != nil && device.NUMANodeID != *required.RequiredNUMANodeID, prefix+"numa_mismatch")
		addReason(required.RequiredIOMMUGroup != "" && device.IOMMUGroup != required.RequiredIOMMUGroup, prefix+"iommu_group_mismatch")
		addReason(device.ActiveClaim, prefix+"already_claimed")
	}
	for _, required := range request.Network {
		network, present := authority.Networks[required.PortID]
		prefix := "network:" + required.PortID + ":"
		addReason(!present, prefix+"authority_missing")
		if !present {
			continue
		}
		addReason(network.NetworkID != required.NetworkID || network.NetworkProjectID != request.ProjectID || network.NetworkState != "ACTIVE" || network.NetworkGeneration != required.NetworkGeneration, prefix+"network_not_current")
		addReason(network.SubnetID != required.SubnetID || network.SubnetState != "ACTIVE" || network.SubnetGeneration != required.SubnetGeneration, prefix+"subnet_not_current")
		addReason(network.SegmentClaimID != required.SegmentClaimID || network.SegmentState != "ACTIVE" || network.SegmentGeneration != required.SegmentGeneration, prefix+"segment_not_current")
		addReason(network.MappingState != "CURRENT" || network.HostMappingGeneration != required.HostMappingGeneration, prefix+"host_mapping_not_current")
		addReason(network.NetworkMTU < required.RequiredMTU || network.MappingMaximumMTU < required.RequiredMTU, prefix+"mtu_not_eligible")
		addReason(!network.IPAddressAllowed, prefix+"ip_not_allowed")
		addReason(network.IdentityConflict, prefix+"identity_claim_conflict")
		addReason(network.PortConflict, prefix+"port_conflict")
		addReason(!network.BindingSupported, prefix+"binding_not_supported")
	}
	for _, required := range request.Storage {
		storage, present := authority.Storage[required.VolumeID]
		prefix := "storage:" + required.VolumeID + ":"
		addReason(!present, prefix+"authority_missing")
		if !present {
			continue
		}
		addReason(storage.BackendID != required.BackendID || storage.BackendType != "LOCAL_LVM" || storage.BackendState != "ACTIVE" || storage.BackendGeneration != required.BackendGeneration || (storage.SupportTier != "VALIDATED" && storage.SupportTier != "SUPPORTED"), prefix+"backend_not_current")
		addReason(storage.BackendHostID != authority.HostID || storage.VGUUID != required.VGUUID || storage.CapabilityState != "CURRENT" || storage.BackendCapabilityGeneration != authority.CapabilityGeneration, prefix+"locality_or_capability_not_current")
		addReason(storage.StorageClassID != required.StorageClassID || storage.StorageClassRevision != required.StorageClassRevision || storage.StorageClassState != "ACTIVE" || storage.AllowedBackendType != "LOCAL_LVM" || storage.Locality != "HOST_LOCAL" || !storage.SingleWriterAllowed || storage.ThinProvisioning || storage.EncryptionRequired || storage.FencingPolicyRevision != required.FencingPolicyRevision, prefix+"storage_class_not_current")
		addReason(storage.CapacityGeneration != required.CapacityGeneration || storage.CapacityState != "CURRENT" || storage.HealthState != "HEALTHY", prefix+"capacity_not_current")
		ledgerAvailable := saturatingSubtract(storage.TotalBytes, saturatingAdd(storage.HardReserveBytes, storage.ClaimedBytes))
		observedAvailable := saturatingSubtract(storage.ObservedFreeBytes, storage.ExternalOrUnknownBytes)
		if observedAvailable < ledgerAvailable {
			ledgerAvailable = observedAvailable
		}
		addReason(ledgerAvailable < required.SizeBytes, prefix+"insufficient_capacity")
		addReason(storage.VolumeConflict, prefix+"volume_conflict")
		addReason(storage.AttachmentConflict, prefix+"attachment_conflict")
	}

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

func saturatingAdd(left, right uint64) uint64 {
	result := left + right
	if result < left {
		return ^uint64(0)
	}
	return result
}

func ceilDivide(value, divisor uint64) uint64 {
	if divisor == 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}
