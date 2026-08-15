package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

var ErrNoSubnetRealizationWork = errors.New("no Subnet realization work available")

type SubnetResourceRequest struct {
	SubnetID, ProjectID, NetworkID, Name, IPFamily, CIDR string
	GatewayPolicy, GatewayAddress, AllocationPolicy      string
	AllocationStart, AllocationEnd                       string
	ReservedAddresses, DNSServers                        []string
	DHCPEnabled, DeleteProtection                        bool
}

type SubnetResource struct {
	SubnetID, ProjectID, NetworkID, Name, IPFamily, CIDR string
	GatewayPolicy, GatewayAddress, AllocationPolicy      string
	AllocationStart, AllocationEnd, Lifecycle            string
	ReservedAddresses, DNSServers                        []string
	Revision, NetworkRevision, PoolGeneration            uint64
	RealizationGeneration                                uint64
	PoolID, PoolState, RealizationState, OperationID     string
	DHCPEnabled, DeleteProtection                        bool
	CreatedAt, UpdatedAt                                 time.Time
}

type SubnetIPAllocationRequest struct {
	AllocationID, SubnetID, Mode, RequestedAddress string
	OwnerResourceType, OwnerResourceID             string
	ExpectedSubnetRevision                         uint64
}

type SubnetIPAllocation struct {
	AllocationID, SubnetID, PoolID, AssignedAddress      string
	OwnerResourceType, OwnerResourceID, State            string
	AllocationGeneration, SubnetRevision, PoolGeneration uint64
}

type SubnetRealizationClaim struct {
	OperationID, Owner, ClaimMode, OperationKind, PlanDigest string
	OperationGeneration, ClaimGeneration                     uint64
	CanonicalPlan                                            []byte
	LeaseExpiresAt                                           time.Time
}

type SubnetRealizationObservation struct {
	ObservationID, OperationID, DHCPObjectName, BackendUUID string
	ObservationDigest, AdapterArtifactDigest                string
	OperationGeneration, ObservationGeneration              uint64
	ApplyResponseState                                      string
	Observation                                             ovnadapter.SubnetObservation
}

func canonicalSubnet(request SubnetResourceRequest) (SubnetResourceRequest, error) {
	if !networkResourceIDPattern.MatchString(request.SubnetID) || request.ProjectID == "" || request.NetworkID == "" || request.Name == "" || len(request.Name) > 255 || request.IPFamily != "IPV4" || request.AllocationPolicy != "RANGE" || (request.GatewayPolicy != "NONE" && request.GatewayPolicy != "AUTO" && request.GatewayPolicy != "EXPLICIT") {
		return request, errors.New("complete logical IPv4 Subnet desired authority is required")
	}
	prefix, err := netip.ParsePrefix(request.CIDR)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 16 || prefix.Bits() > 30 {
		return request, errors.New("bounded IPv4 CIDR is required")
	}
	prefix = prefix.Masked()
	request.CIDR = prefix.String()
	first := nextIPv4(prefix.Addr())
	last := previousIPv4(lastIPv4(prefix))
	if request.GatewayPolicy == "NONE" {
		if request.GatewayAddress != "" {
			return request, errors.New("NONE gateway cannot carry an address")
		}
	} else if request.GatewayPolicy == "AUTO" {
		request.GatewayAddress = first.String()
	} else {
		gateway, err := netip.ParseAddr(request.GatewayAddress)
		if err != nil || !gateway.Is4() || !prefix.Contains(gateway) || gateway == prefix.Addr() || gateway == lastIPv4(prefix) {
			return request, errors.New("explicit gateway must be usable in the CIDR")
		}
		request.GatewayAddress = gateway.String()
	}
	if request.AllocationStart == "" {
		request.AllocationStart = first.String()
	}
	if request.AllocationEnd == "" {
		request.AllocationEnd = last.String()
	}
	start, e1 := netip.ParseAddr(request.AllocationStart)
	end, e2 := netip.ParseAddr(request.AllocationEnd)
	if e1 != nil || e2 != nil || !start.Is4() || !end.Is4() || !prefix.Contains(start) || !prefix.Contains(end) || start.Less(prefix.Addr()) || end.Less(start) || lastIPv4(prefix).Less(end) || start == prefix.Addr() || end == lastIPv4(prefix) {
		return request, errors.New("allocation range must contain only usable addresses")
	}
	request.AllocationStart, request.AllocationEnd = start.String(), end.String()
	reserved := map[string]struct{}{}
	if request.GatewayAddress != "" {
		reserved[request.GatewayAddress] = struct{}{}
	}
	for _, raw := range request.ReservedAddresses {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is4() || !prefix.Contains(address) || address == prefix.Addr() || address == lastIPv4(prefix) {
			return request, errors.New("reserved address is invalid")
		}
		reserved[address.String()] = struct{}{}
	}
	request.ReservedAddresses = make([]string, 0, len(reserved))
	for address := range reserved {
		request.ReservedAddresses = append(request.ReservedAddresses, address)
	}
	sort.Strings(request.ReservedAddresses)
	if len(request.DNSServers) > 8 {
		return request, errors.New("at most eight DNS servers are supported")
	}
	request.DNSServers = append(make([]string, 0, len(request.DNSServers)), request.DNSServers...)
	for index, raw := range request.DNSServers {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is4() {
			return request, errors.New("DNS server must be IPv4")
		}
		request.DNSServers[index] = address.String()
	}
	sort.Strings(request.DNSServers)
	return request, nil
}

func nextIPv4(address netip.Addr) netip.Addr     { return address.Next() }
func previousIPv4(address netip.Addr) netip.Addr { return address.Prev() }
func lastIPv4(prefix netip.Prefix) netip.Addr {
	b := prefix.Addr().As4()
	bits := 32 - prefix.Bits()
	value := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	value |= (uint32(1) << bits) - 1
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

func subnetDesiredDigest(request SubnetResourceRequest, revision uint64, lifecycle string) string {
	raw, _ := json.Marshal(struct {
		Request   SubnetResourceRequest
		Revision  uint64
		Lifecycle string
	}{request, revision, lifecycle})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func CreateSubnetResource(ctx context.Context, db TxBeginner, request SubnetResourceRequest) (SubnetResource, error) {
	request, err := canonicalSubnet(request)
	if err != nil {
		return SubnetResource{}, err
	}
	digest := subnetDesiredDigest(request, 1, "ACTIVE")
	err = RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `subnet-resource/`+request.SubnetID); err != nil {
			return err
		}
		var existing, source string
		if err := tx.QueryRow(ctx, `SELECT desired_digest,authority_source FROM kim.network_subnets_current WHERE subnet_id=$1`, request.SubnetID).Scan(&existing, &source); err == nil {
			if source == "SUBNET_RESOURCE" && existing == digest {
				return nil
			}
			return ErrPlacementConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var networkRevision int64
		if err := tx.QueryRow(ctx, `SELECT n.network_revision FROM kim.networks_current n JOIN kim.network_realizations_current r ON r.network_id=n.network_id AND r.network_revision=n.network_revision WHERE n.network_id=$1 AND n.project_id=$2 AND n.authority_source='NETWORK_RESOURCE' AND n.lifecycle_state='ACTIVE' AND r.realization_state='VERIFIED' AND r.terminal_evidence_id IS NOT NULL FOR SHARE OF n,r`, request.NetworkID, request.ProjectID).Scan(&networkRevision); err != nil {
			return ErrPlacementConflict
		}
		var overlap bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_subnets_current WHERE network_id=$1 AND lifecycle_state IN ('ACTIVE','DRAINING','RETIRE_PENDING') AND cidr && $2::cidr)`, request.NetworkID, request.CIDR).Scan(&overlap); err != nil || overlap {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_resource_revision_evidence(subnet_id,subnet_revision,project_id,network_id,network_revision,subnet_name,ip_family,cidr,gateway_policy,gateway_address,allocation_policy,allocation_start,allocation_end,reserved_addresses,dhcp_enabled,dns_servers,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,1,$2,$3,$4,$5,'IPV4',$6,$7,NULLIF($8,'')::inet,'RANGE',$9::inet,$10::inet,$11::inet[],$12,$13::inet[],$14,'ACTIVE',NULL,$15)`, request.SubnetID, request.ProjectID, request.NetworkID, networkRevision, request.Name, request.CIDR, request.GatewayPolicy, request.GatewayAddress, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses, request.DHCPEnabled, request.DNSServers, request.DeleteProtection, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_subnets_current(subnet_id,network_id,subnet_generation,lifecycle_state,cidr,allocation_start,allocation_end,excluded_addresses,project_id,subnet_revision,subnet_name,ip_family,gateway_policy,gateway_address,allocation_policy,dhcp_enabled,dns_servers,delete_protection,desired_digest,authority_source,created_at) VALUES($1,$2,1,'ACTIVE',$3,$4,$5,$6,$7,1,$8,'IPV4',$9,NULLIF($10,'')::inet,'RANGE',$11,$12::inet[],$13,$14,'SUBNET_RESOURCE',statement_timestamp())`, request.SubnetID, request.NetworkID, request.CIDR, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses, request.ProjectID, request.Name, request.GatewayPolicy, request.GatewayAddress, request.DHCPEnabled, request.DNSServers, request.DeleteProtection, digest); err != nil {
			return err
		}
		poolID := `subnet-pool:` + request.SubnetID
		poolDigest := digestNetworkResource(fmt.Sprintf("%s/1/%s/%s/%v", poolID, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ipam_pool_decision_evidence(pool_id,pool_generation,subnet_id,subnet_revision,address_family,allocation_start,allocation_end,reserved_addresses,decision_state,decision_digest) VALUES($1,1,$2,1,'IPV4',$3,$4,$5,'ACTIVE',$6)`, poolID, request.SubnetID, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses, poolDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ipam_pools_current(subnet_id,subnet_revision,pool_id,pool_generation,address_family,allocation_start,allocation_end,reserved_addresses,lifecycle_state) VALUES($1,1,$2,1,'IPV4',$3,$4,$5,'ACTIVE')`, request.SubnetID, poolID, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses); err != nil {
			return err
		}
		return insertSubnetRealizationOperationTx(ctx, tx, request, 1, uint64(networkRevision), 1, "REALIZE", "PRESENT")
	})
	if err != nil {
		return SubnetResource{}, err
	}
	return GetSubnetResource(ctx, db, request.SubnetID)
}

// UpdateSubnetResource authorizes a complete replacement revision. Address
// semantics are frozen while any allocation from the old pool still exists.
func UpdateSubnetResource(ctx context.Context, db TxBeginner, request SubnetResourceRequest, expectedRevision uint64) (SubnetResource, error) {
	request, err := canonicalSubnet(request)
	if err != nil || expectedRevision == 0 {
		return SubnetResource{}, errors.New("complete Subnet replacement revision is required")
	}
	err = RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var revision, networkRevision, poolGeneration, realizationGeneration int64
		var projectID, networkID, lifecycle string
		if err := tx.QueryRow(ctx, `SELECT s.project_id,s.network_id,s.lifecycle_state,s.subnet_revision,p.pool_generation,r.realization_generation FROM kim.network_subnets_current s JOIN kim.subnet_ipam_pools_current p USING(subnet_id) JOIN kim.subnet_realizations_current r USING(subnet_id) WHERE s.subnet_id=$1 AND s.authority_source='SUBNET_RESOURCE' FOR UPDATE OF s,p,r`, request.SubnetID).Scan(&projectID, &networkID, &lifecycle, &revision, &poolGeneration, &realizationGeneration); err != nil || lifecycle != "ACTIVE" || uint64(revision) != expectedRevision || projectID != request.ProjectID || networkID != request.NetworkID {
			return ErrPlacementConflict
		}
		var allocated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current WHERE subnet_id=$1 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING'))`, request.SubnetID).Scan(&allocated); err != nil || allocated {
			return ErrPlacementConflict
		}
		if err := tx.QueryRow(ctx, `SELECT n.network_revision FROM kim.networks_current n JOIN kim.network_realizations_current r ON r.network_id=n.network_id AND r.network_revision=n.network_revision WHERE n.network_id=$1 AND n.project_id=$2 AND n.lifecycle_state='ACTIVE' AND n.authority_source='NETWORK_RESOURCE' AND r.realization_state='VERIFIED' AND r.terminal_evidence_id IS NOT NULL FOR SHARE OF n,r`, request.NetworkID, request.ProjectID).Scan(&networkRevision); err != nil {
			return ErrPlacementConflict
		}
		var overlap bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_subnets_current WHERE network_id=$1 AND subnet_id<>$2 AND lifecycle_state IN ('ACTIVE','DRAINING','RETIRE_PENDING') AND cidr && $3::cidr)`, request.NetworkID, request.SubnetID, request.CIDR).Scan(&overlap); err != nil || overlap {
			return ErrPlacementConflict
		}
		next := uint64(revision + 1)
		nextPool := uint64(poolGeneration + 1)
		digest := subnetDesiredDigest(request, next, "ACTIVE")
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_resource_revision_evidence(subnet_id,subnet_revision,project_id,network_id,network_revision,subnet_name,ip_family,cidr,gateway_policy,gateway_address,allocation_policy,allocation_start,allocation_end,reserved_addresses,dhcp_enabled,dns_servers,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,'IPV4',$7,$8,NULLIF($9,'')::inet,'RANGE',$10,$11,$12::inet[],$13,$14::inet[],$15,'ACTIVE',$16,$17)`, request.SubnetID, next, request.ProjectID, request.NetworkID, networkRevision, request.Name, request.CIDR, request.GatewayPolicy, request.GatewayAddress, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses, request.DHCPEnabled, request.DNSServers, request.DeleteProtection, revision, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_subnets_current SET subnet_generation=$2,subnet_revision=$2,subnet_name=$3,cidr=$4,allocation_start=$5,allocation_end=$6,excluded_addresses=$7,gateway_policy=$8,gateway_address=NULLIF($9,'')::inet,dhcp_enabled=$10,dns_servers=$11::inet[],delete_protection=$12,desired_digest=$13,updated_at=statement_timestamp() WHERE subnet_id=$1 AND subnet_revision=$14`, request.SubnetID, next, request.Name, request.CIDR, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses, request.GatewayPolicy, request.GatewayAddress, request.DHCPEnabled, request.DNSServers, request.DeleteProtection, digest, revision); err != nil {
			return err
		}
		poolID := "subnet-pool:" + request.SubnetID
		poolDigest := digestNetworkResource(fmt.Sprintf("%s/%d/%s/%s/%v", poolID, nextPool, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ipam_pool_decision_evidence(pool_id,pool_generation,subnet_id,subnet_revision,address_family,allocation_start,allocation_end,reserved_addresses,decision_state,decision_digest) VALUES($1,$2,$3,$4,'IPV4',$5,$6,$7,'ACTIVE',$8)`, poolID, nextPool, request.SubnetID, next, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses, poolDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ipam_pools_current SET subnet_revision=$2,pool_generation=$3,allocation_start=$4,allocation_end=$5,reserved_addresses=$6,updated_at=statement_timestamp() WHERE subnet_id=$1`, request.SubnetID, next, nextPool, request.AllocationStart, request.AllocationEnd, request.ReservedAddresses); err != nil {
			return err
		}
		return insertSubnetRealizationOperationTx(ctx, tx, request, next, uint64(networkRevision), uint64(realizationGeneration+1), "REALIZE", "PRESENT")
	})
	if err != nil {
		return SubnetResource{}, err
	}
	return GetSubnetResource(ctx, db, request.SubnetID)
}

func insertSubnetRealizationOperationTx(ctx context.Context, tx pgx.Tx, desired SubnetResourceRequest, revision, networkRevision, generation uint64, kind, state string) error {
	operationID := fmt.Sprintf("subnet-realization:%s:%d", desired.SubnetID, generation)
	plan, planDigest, err := ovnadapter.PlanSubnet(ovnadapter.SubnetIntentInput{OperationID: operationID, OperationGeneration: 1, ProjectID: desired.ProjectID, NetworkID: desired.NetworkID, NetworkRevision: networkRevision, SubnetID: desired.SubnetID, SubnetRevision: revision, RealizationGeneration: generation, CIDR: desired.CIDR, GatewayAddress: desired.GatewayAddress, DNSServiceAddresses: desired.DNSServers, DHCPEnabled: desired.DHCPEnabled, DesiredState: state})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.subnet_realization_operation_evidence(operation_id,operation_generation,operation_kind,subnet_id,subnet_revision,network_id,network_revision,realization_generation,schema_version,canonical_plan,plan_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, operationID, kind, desired.SubnetID, revision, desired.NetworkID, networkRevision, generation, ovnadapter.SubnetIntentSchema, plan, planDigest); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.subnet_realization_operations_current(operation_id,operation_generation,subnet_id,subnet_revision,network_id,network_revision,realization_generation,operation_kind,phase) VALUES($1,1,$2,$3,$4,$5,$6,$7,'PENDING') ON CONFLICT(subnet_id) DO UPDATE SET operation_id=EXCLUDED.operation_id,operation_generation=1,subnet_revision=EXCLUDED.subnet_revision,network_id=EXCLUDED.network_id,network_revision=EXCLUDED.network_revision,realization_generation=EXCLUDED.realization_generation,operation_kind=EXCLUDED.operation_kind,phase='PENDING',last_claim_generation=0,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state=NULL,terminal_evidence_id=NULL,updated_at=statement_timestamp()`, operationID, desired.SubnetID, revision, desired.NetworkID, networkRevision, generation, kind); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.subnet_realizations_current(subnet_id,subnet_revision,network_revision,realization_generation,operation_id,operation_generation,realization_state) VALUES($1,$2,$3,$4,$5,1,'PENDING') ON CONFLICT(subnet_id) DO UPDATE SET subnet_revision=EXCLUDED.subnet_revision,network_revision=EXCLUDED.network_revision,realization_generation=EXCLUDED.realization_generation,operation_id=EXCLUDED.operation_id,operation_generation=1,realization_state='PENDING',terminal_evidence_id=NULL,updated_at=statement_timestamp()`, desired.SubnetID, revision, networkRevision, generation, operationID)
	return err
}

func AllocateSubnetIP(ctx context.Context, db TxBeginner, request SubnetIPAllocationRequest) (SubnetIPAllocation, error) {
	if !networkResourceIDPattern.MatchString(request.AllocationID) || request.SubnetID == "" || request.ExpectedSubnetRevision == 0 || request.OwnerResourceType != "PORT" || request.OwnerResourceID == "" || (request.Mode != "AUTO" && request.Mode != "EXPLICIT") || (request.Mode == "AUTO" && request.RequestedAddress != "") || (request.Mode == "EXPLICIT" && request.RequestedAddress == "") {
		return SubnetIPAllocation{}, errors.New("complete exact Subnet IP allocation request is required")
	}
	digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s/%s/%s/%s", request.AllocationID, request.SubnetID, request.ExpectedSubnetRevision, request.Mode, request.RequestedAddress, request.OwnerResourceType, request.OwnerResourceID))
	var result SubnetIPAllocation
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `network-ipam/`+request.SubnetID); err != nil {
			return err
		}
		// Touching the pool projection turns a waiter with an older Serializable
		// snapshot into SQLSTATE 40001, so RunSerializable retries and observes
		// the preceding allocation instead of selecting the same address.
		if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ipam_pools_current SET updated_at=statement_timestamp() WHERE subnet_id=$1 AND lifecycle_state='ACTIVE'`, request.SubnetID); err != nil {
			return err
		}
		var existingDigest string
		if err := tx.QueryRow(ctx, `SELECT e.request_digest,c.allocation_id,c.subnet_id,c.pool_id,host(c.assigned_address),c.owner_resource_type,c.owner_resource_id,c.allocation_state,c.allocation_generation,c.subnet_revision,c.pool_generation FROM kim.subnet_ip_allocations_current c JOIN kim.subnet_ip_allocation_decision_evidence e USING(allocation_id,allocation_generation) WHERE c.allocation_id=$1`, request.AllocationID).Scan(&existingDigest, &result.AllocationID, &result.SubnetID, &result.PoolID, &result.AssignedAddress, &result.OwnerResourceType, &result.OwnerResourceID, &result.State, &result.AllocationGeneration, &result.SubnetRevision, &result.PoolGeneration); err == nil {
			if existingDigest == digest {
				return nil
			}
			return ErrPlacementConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var start, end string
		var reserved []string
		var rev, poolGen int64
		if err := tx.QueryRow(ctx, `SELECT p.pool_id,p.pool_generation,p.subnet_revision,host(p.allocation_start),host(p.allocation_end),ARRAY(SELECT host(x) FROM unnest(p.reserved_addresses) x) FROM kim.subnet_ipam_pools_current p JOIN kim.network_subnets_current s USING(subnet_id) JOIN kim.subnet_realizations_current r ON r.subnet_id=s.subnet_id AND r.subnet_revision=s.subnet_revision JOIN kim.networks_current n ON n.network_id=s.network_id AND n.network_revision=r.network_revision JOIN kim.network_realizations_current nr ON nr.network_id=n.network_id AND nr.network_revision=n.network_revision WHERE p.subnet_id=$1 AND p.subnet_revision=$2 AND p.lifecycle_state='ACTIVE' AND s.lifecycle_state='ACTIVE' AND s.authority_source='SUBNET_RESOURCE' AND r.realization_state='VERIFIED' AND r.terminal_evidence_id IS NOT NULL AND n.lifecycle_state='ACTIVE' AND nr.realization_state='VERIFIED' AND nr.terminal_evidence_id IS NOT NULL FOR UPDATE OF p,n,nr`, request.SubnetID, request.ExpectedSubnetRevision).Scan(&result.PoolID, &poolGen, &rev, &start, &end, &reserved); err != nil {
			return ErrPlacementConflict
		}
		assigned := request.RequestedAddress
		if request.Mode == "AUTO" {
			if err := tx.QueryRow(ctx, `SELECT host(($1::inet+candidate)::inet) FROM generate_series(0,($2::inet-$1::inet)) candidate WHERE NOT(($1::inet+candidate)::inet=ANY($3::inet[])) AND NOT EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current a WHERE a.subnet_id=$4 AND a.assigned_address=($1::inet+candidate)::inet AND a.allocation_state IN ('ALLOCATED','RELEASE_PENDING')) ORDER BY candidate LIMIT 1`, start, end, reserved, request.SubnetID).Scan(&assigned); err != nil {
				return ErrPlacementConflict
			}
		} else {
			address, err := netip.ParseAddr(assigned)
			s, _ := netip.ParseAddr(start)
			e, _ := netip.ParseAddr(end)
			if err != nil || address.Less(s) || e.Less(address) {
				return ErrPlacementConflict
			}
			for _, r := range reserved {
				if address.String() == r {
					return ErrPlacementConflict
				}
			}
		}
		var collision bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current WHERE subnet_id=$1 AND assigned_address=$2 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING'))`, request.SubnetID, assigned).Scan(&collision); err != nil || collision {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocation_decision_evidence(allocation_id,allocation_generation,subnet_id,subnet_revision,pool_id,pool_generation,allocation_mode,requested_address,assigned_address,owner_resource_type,owner_resource_id,request_digest,decision_state) VALUES($1,1,$2,$3,$4,$5,$6,NULLIF($7,'')::inet,$8,$9,$10,$11,'ALLOCATED')`, request.AllocationID, request.SubnetID, rev, result.PoolID, poolGen, request.Mode, request.RequestedAddress, assigned, request.OwnerResourceType, request.OwnerResourceID, digest); err != nil {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocations_current(allocation_id,allocation_generation,subnet_id,subnet_revision,pool_id,pool_generation,assigned_address,owner_resource_type,owner_resource_id,allocation_state) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,'ALLOCATED')`, request.AllocationID, request.SubnetID, rev, result.PoolID, poolGen, assigned, request.OwnerResourceType, request.OwnerResourceID); err != nil {
			return ErrPlacementConflict
		}
		result = SubnetIPAllocation{AllocationID: request.AllocationID, AllocationGeneration: 1, SubnetID: request.SubnetID, SubnetRevision: uint64(rev), PoolID: result.PoolID, PoolGeneration: uint64(poolGen), AssignedAddress: assigned, OwnerResourceType: request.OwnerResourceType, OwnerResourceID: request.OwnerResourceID, State: "ALLOCATED"}
		return nil
	})
	return result, err
}

// recordSubnetPortAllocationTx binds Final Admission's exact IP decision to
// the current verified Subnet/IPAM incarnation. Legacy foundation Subnets are
// deliberately left on their historical claim-only contract.
func recordSubnetPortAllocationTx(ctx context.Context, tx pgx.Tx, requestID, portID, subnetID, mode, address string) (*SubnetIPAllocation, error) {
	var source string
	if err := tx.QueryRow(ctx, `SELECT authority_source FROM kim.network_subnets_current WHERE subnet_id=$1`, subnetID).Scan(&source); err != nil {
		return nil, err
	}
	if source == "LEGACY_FOUNDATION" {
		return nil, nil
	}
	if source != "SUBNET_RESOURCE" {
		return nil, ErrPlacementConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ipam_pools_current SET updated_at=statement_timestamp() WHERE subnet_id=$1 AND lifecycle_state='ACTIVE'`, subnetID); err != nil {
		return nil, err
	}
	var allocation SubnetIPAllocation
	var revision, poolGeneration int64
	var reserved []string
	if err := tx.QueryRow(ctx, `SELECT s.subnet_revision,p.pool_id,p.pool_generation,ARRAY(SELECT host(x) FROM unnest(p.reserved_addresses) x) FROM kim.network_subnets_current s JOIN kim.subnet_ipam_pools_current p USING(subnet_id) JOIN kim.subnet_realizations_current r ON r.subnet_id=s.subnet_id AND r.subnet_revision=s.subnet_revision JOIN kim.networks_current n ON n.network_id=s.network_id AND n.network_revision=r.network_revision JOIN kim.network_realizations_current nr ON nr.network_id=n.network_id AND nr.network_revision=n.network_revision WHERE s.subnet_id=$1 AND s.lifecycle_state='ACTIVE' AND p.lifecycle_state='ACTIVE' AND r.realization_state='VERIFIED' AND r.terminal_evidence_id IS NOT NULL AND n.lifecycle_state='ACTIVE' AND nr.realization_state='VERIFIED' AND nr.terminal_evidence_id IS NOT NULL FOR UPDATE OF s,p,r,n,nr`, subnetID).Scan(&revision, &allocation.PoolID, &poolGeneration, &reserved); err != nil {
		return nil, ErrPlacementConflict
	}
	for _, blocked := range reserved {
		if blocked == address {
			return nil, ErrPlacementConflict
		}
	}
	allocationID := "subnet-ip:" + requestID + ":" + portID
	digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s/%s/PORT/%s", allocationID, subnetID, revision, mode, address, portID))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocation_decision_evidence(allocation_id,allocation_generation,subnet_id,subnet_revision,pool_id,pool_generation,allocation_mode,requested_address,assigned_address,owner_resource_type,owner_resource_id,request_digest,decision_state) VALUES($1,1,$2,$3,$4,$5,$6,CASE WHEN $6='EXPLICIT' THEN $7::inet END,$7,'PORT',$8,$9,'ALLOCATED')`, allocationID, subnetID, revision, allocation.PoolID, poolGeneration, mode, address, portID, digest); err != nil {
		return nil, ErrPlacementConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocations_current(allocation_id,allocation_generation,subnet_id,subnet_revision,pool_id,pool_generation,assigned_address,owner_resource_type,owner_resource_id,allocation_state) VALUES($1,1,$2,$3,$4,$5,$6,'PORT',$7,'ALLOCATED')`, allocationID, subnetID, revision, allocation.PoolID, poolGeneration, address, portID); err != nil {
		return nil, ErrPlacementConflict
	}
	allocation.AllocationID = allocationID
	allocation.AllocationGeneration = 1
	allocation.SubnetID = subnetID
	allocation.SubnetRevision = uint64(revision)
	allocation.PoolGeneration = uint64(poolGeneration)
	allocation.AssignedAddress = address
	allocation.OwnerResourceType = "PORT"
	allocation.OwnerResourceID = portID
	allocation.State = "ALLOCATED"
	return &allocation, nil
}

func ClaimSubnetRealization(ctx context.Context, db TxBeginner, operationID, owner string, lease time.Duration) (SubnetRealizationClaim, error) {
	if owner == "" || lease <= 0 || lease > MaxOVNRuntimeClaimLifetime {
		return SubnetRealizationClaim{}, errors.New("bounded Subnet realization claim is required")
	}
	var c SubnetRealizationClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var phase string
		var generation int64
		if err := tx.QueryRow(ctx, `SELECT c.operation_id,c.operation_generation,c.operation_kind,c.phase,e.canonical_plan,e.plan_digest FROM kim.subnet_realization_operations_current c JOIN kim.subnet_realization_operation_evidence e USING(operation_id,operation_generation) WHERE ($1='' OR c.operation_id=$1) AND (c.phase IN ('PENDING','DISPATCH_UNKNOWN') OR (c.phase='CLAIMED' AND c.claim_expires_at<=statement_timestamp())) ORDER BY c.updated_at,c.operation_id FOR UPDATE OF c SKIP LOCKED LIMIT 1`, operationID).Scan(&c.OperationID, &c.OperationGeneration, &c.OperationKind, &phase, &c.CanonicalPlan, &c.PlanDigest); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSubnetRealizationWork
		} else if err != nil {
			return err
		}
		c.ClaimMode = "APPLY_ALLOWED"
		if phase != "PENDING" {
			c.ClaimMode = "READ_BACK_FIRST"
		}
		if err := tx.QueryRow(ctx, `UPDATE kim.subnet_realization_operations_current SET phase='CLAIMED',claim_owner=$2,claim_generation=last_claim_generation+1,last_claim_generation=last_claim_generation+1,claim_expires_at=statement_timestamp()+($3*interval '1 microsecond'),updated_at=statement_timestamp() WHERE operation_id=$1 RETURNING claim_generation,claim_expires_at`, c.OperationID, owner, lease.Microseconds()).Scan(&generation, &c.LeaseExpiresAt); err != nil {
			return err
		}
		c.Owner = owner
		c.ClaimGeneration = uint64(generation)
		_, err := tx.Exec(ctx, `INSERT INTO kim.subnet_realization_attempt_evidence(operation_id,operation_generation,claim_generation,claim_owner,claim_mode,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6)`, c.OperationID, c.OperationGeneration, generation, owner, c.ClaimMode, c.LeaseExpiresAt)
		return err
	})
	return c, err
}

func lockSubnetClaim(ctx context.Context, tx pgx.Tx, c SubnetRealizationClaim) error {
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return err
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.subnet_realization_operations_current WHERE operation_id=$1 AND operation_generation=$2 AND phase='CLAIMED' AND claim_owner=$3 AND claim_generation=$4 AND claim_expires_at>statement_timestamp())`, c.OperationID, c.OperationGeneration, c.Owner, c.ClaimGeneration).Scan(&ok); err != nil || !ok {
		return ErrPlacementConflict
	}
	return nil
}
func MarkSubnetRealizationDispatchUnknown(ctx context.Context, db TxBeginner, c SubnetRealizationClaim) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSubnetClaim(ctx, tx, c); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.subnet_realization_operations_current SET phase='DISPATCH_UNKNOWN',response_state='UNKNOWN',claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID)
		return err
	})
}
func RecordSubnetRealizationReadBackStarted(ctx context.Context, db TxBeginner, c SubnetRealizationClaim) error {
	return subnetAttemptEvent(ctx, db, c, "READ_BACK_STARTED", true)
}
func AuthorizeSubnetRealizationApply(ctx context.Context, db TxBeginner, c SubnetRealizationClaim) error {
	return subnetAttemptEvent(ctx, db, c, "APPLY_AUTHORIZED", false)
}
func subnetAttemptEvent(ctx context.Context, db TxBeginner, c SubnetRealizationClaim, event string, requireReadbackMode bool) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSubnetClaim(ctx, tx, c); err != nil {
			return err
		}
		var mode string
		if err := tx.QueryRow(ctx, `SELECT claim_mode FROM kim.subnet_realization_attempt_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3`, c.OperationID, c.OperationGeneration, c.ClaimGeneration).Scan(&mode); err != nil {
			return err
		}
		if requireReadbackMode && mode != "READ_BACK_FIRST" {
			return ErrPlacementConflict
		}
		if !requireReadbackMode && mode == "READ_BACK_FIRST" {
			var seen bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.subnet_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='READ_BACK_STARTED')`, c.OperationID, c.OperationGeneration, c.ClaimGeneration).Scan(&seen); err != nil || !seen {
				return ErrPlacementConflict
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.subnet_realization_attempt_event_evidence(operation_id,operation_generation,claim_generation,event_type) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, c.OperationID, c.OperationGeneration, c.ClaimGeneration, event)
		return err
	})
}

func AcceptSubnetRealizationObservation(ctx context.Context, db TxBeginner, c SubnetRealizationClaim, o SubnetRealizationObservation) (string, error) {
	if o.ObservationID == "" || o.OperationID != c.OperationID || o.OperationGeneration != c.OperationGeneration || o.ObservationGeneration == 0 || len(o.ObservationDigest) != 64 || len(o.AdapterArtifactDigest) != 64 {
		return "", ErrPlacementConflict
	}
	terminal := ""
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSubnetClaim(ctx, tx, c); err != nil {
			return err
		}
		var subnetID, networkID, kind, planDigest string
		var subnetRev, networkRev, realGen int64
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT c.subnet_id,c.subnet_revision,c.network_id,c.network_revision,c.realization_generation,c.operation_kind,e.canonical_plan,e.plan_digest FROM kim.subnet_realization_operations_current c JOIN kim.subnet_realization_operation_evidence e USING(operation_id,operation_generation) WHERE c.operation_id=$1`, c.OperationID).Scan(&subnetID, &subnetRev, &networkID, &networkRev, &realGen, &kind, &raw, &planDigest); err != nil {
			return err
		}
		_, plan, err := ovnadapter.RestoreStoredSubnetPlan(raw, planDigest)
		if err != nil || o.DHCPObjectName != plan.DHCPObjectName {
			return ErrPlacementConflict
		}
		var mode string
		if err := tx.QueryRow(ctx, `SELECT claim_mode FROM kim.subnet_realization_attempt_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3`, c.OperationID, c.OperationGeneration, c.ClaimGeneration).Scan(&mode); err != nil {
			return err
		}
		var rb, apply bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.subnet_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='READ_BACK_STARTED'),EXISTS(SELECT 1 FROM kim.subnet_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='APPLY_AUTHORIZED')`, c.OperationID, c.OperationGeneration, c.ClaimGeneration).Scan(&rb, &apply); err != nil {
			return err
		}
		if mode == "READ_BACK_FIRST" && !rb || o.ApplyResponseState != "UNKNOWN" && !apply {
			return ErrPlacementConflict
		}
		state := o.Observation.State(plan)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_realization_observation_evidence(observation_id,operation_id,operation_generation,subnet_id,subnet_revision,network_revision,realization_generation,observation_generation,apply_response_state,dhcp_object_name,backend_uuid,object_present,ownership_marker_matches,plan_digest_matches,cidr_matches,options_match,network_association_matches,observation_digest,adapter_artifact_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16,$17,$18,$19)`, o.ObservationID, c.OperationID, c.OperationGeneration, subnetID, subnetRev, networkRev, realGen, o.ObservationGeneration, o.ApplyResponseState, o.DHCPObjectName, o.BackendUUID, o.Observation.ObjectPresent, o.Observation.OwnershipMarkerMatches, o.Observation.PlanDigestMatches, o.Observation.CIDRMatches, o.Observation.OptionsMatch, o.Observation.NetworkAssociationMatches, o.ObservationDigest, o.AdapterArtifactDigest); err != nil {
			return err
		}
		if (kind == "REALIZE" && state == "VERIFIED") || (kind == "RETIRE" && state == "ABSENT") {
			var parentOK bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.networks_current n JOIN kim.network_realizations_current r ON r.network_id=n.network_id AND r.network_revision=n.network_revision WHERE n.network_id=$1 AND n.network_revision=$2 AND n.lifecycle_state='ACTIVE' AND r.realization_state='VERIFIED')`, networkID, networkRev).Scan(&parentOK); err != nil || !parentOK {
				return ErrPlacementConflict
			}
			terminal = state
			tid := fmt.Sprintf("subnet-terminal:%s:%d", c.OperationID, c.OperationGeneration)
			td := digestNetworkResource(fmt.Sprintf("%s/%d/%d/%s", subnetID, subnetRev, realGen, state))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_realization_terminal_evidence(terminal_evidence_id,operation_id,operation_generation,observation_id,subnet_id,subnet_revision,network_revision,realization_generation,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, tid, c.OperationID, c.OperationGeneration, o.ObservationID, subnetID, subnetRev, networkRev, realGen, state, td); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.subnet_realization_operations_current SET phase='SUCCEEDED',response_state=$2,terminal_evidence_id=$3,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID, o.ApplyResponseState, tid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.subnet_realizations_current SET realization_state=$2,terminal_evidence_id=$3,updated_at=statement_timestamp() WHERE subnet_id=$1 AND subnet_revision=$4 AND realization_generation=$5`, subnetID, state, tid, subnetRev, realGen); err != nil {
				return err
			}
			if kind == "RETIRE" {
				if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ipam_pool_lifecycle_evidence(lifecycle_evidence_id,pool_id,pool_generation,subnet_id,subnet_revision,lifecycle_state,realization_terminal_evidence_id,lifecycle_digest) SELECT 'subnet-pool-retired:'||pool_id||':'||pool_generation,pool_id,pool_generation,subnet_id,$2,'RETIRED',$3,$4 FROM kim.subnet_ipam_pools_current WHERE subnet_id=$1 AND lifecycle_state='RETIRE_PENDING'`, subnetID, subnetRev, tid, digestNetworkResource(fmt.Sprintf("%s/%d/%s", subnetID, subnetRev, tid))); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ipam_pools_current SET lifecycle_state='RETIRED',updated_at=statement_timestamp() WHERE subnet_id=$1 AND subnet_revision=$2 AND lifecycle_state='RETIRE_PENDING'`, subnetID, subnetRev); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE kim.network_subnets_current SET lifecycle_state='DELETED',updated_at=statement_timestamp() WHERE subnet_id=$1 AND subnet_revision=$2 AND lifecycle_state='RETIRE_PENDING'`, subnetID, subnetRev); err != nil {
					return err
				}
			}
			return nil
		}
		phase := "DISPATCH_UNKNOWN"
		if state == "CONFLICTING" {
			phase = "FAILED"
		}
		_, err = tx.Exec(ctx, `UPDATE kim.subnet_realization_operations_current SET phase=$2,response_state=$3,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID, phase, o.ApplyResponseState)
		return err
	})
	return terminal, err
}

func RequestSubnetRetirement(ctx context.Context, db TxBeginner, subnetID string, expectedRevision uint64) (SubnetResource, error) {
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var r SubnetResource
		var revision, networkRevision, realGen int64
		if err := tx.QueryRow(ctx, `SELECT s.project_id,s.network_id,s.subnet_name,s.ip_family,s.cidr::text,s.gateway_policy,COALESCE(host(s.gateway_address),''),s.allocation_policy,host(s.allocation_start),host(s.allocation_end),ARRAY(SELECT host(x) FROM unnest(s.excluded_addresses) x),s.dhcp_enabled,ARRAY(SELECT host(x) FROM unnest(s.dns_servers) x),s.delete_protection,s.lifecycle_state,s.subnet_revision,e.network_revision,r.realization_generation FROM kim.network_subnets_current s JOIN kim.subnet_resource_revision_evidence e ON e.subnet_id=s.subnet_id AND e.subnet_revision=s.subnet_revision JOIN kim.subnet_realizations_current r ON r.subnet_id=s.subnet_id WHERE s.subnet_id=$1 AND s.authority_source='SUBNET_RESOURCE' FOR UPDATE OF s,r`, subnetID).Scan(&r.ProjectID, &r.NetworkID, &r.Name, &r.IPFamily, &r.CIDR, &r.GatewayPolicy, &r.GatewayAddress, &r.AllocationPolicy, &r.AllocationStart, &r.AllocationEnd, &r.ReservedAddresses, &r.DHCPEnabled, &r.DNSServers, &r.DeleteProtection, &r.Lifecycle, &revision, &networkRevision, &realGen); err != nil || r.Lifecycle != "ACTIVE" || uint64(revision) != expectedRevision || r.DeleteProtection {
			return ErrPlacementConflict
		}
		var parentCurrent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.networks_current n JOIN kim.network_realizations_current nr ON nr.network_id=n.network_id AND nr.network_revision=n.network_revision WHERE n.network_id=$1 AND n.network_revision=$2 AND n.lifecycle_state='ACTIVE' AND n.authority_source='NETWORK_RESOURCE' AND nr.realization_state='VERIFIED' AND nr.terminal_evidence_id IS NOT NULL)`, r.NetworkID, networkRevision).Scan(&parentCurrent); err != nil || !parentCurrent {
			return ErrPlacementConflict
		}
		var dependent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current WHERE subnet_id=$1 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING')) OR EXISTS(SELECT 1 FROM kim.network_ports_current WHERE subnet_id=$1 AND desired_state<>'RELEASED')`, subnetID).Scan(&dependent); err != nil || dependent {
			return ErrPlacementConflict
		}
		request := SubnetResourceRequest{SubnetID: subnetID, ProjectID: r.ProjectID, NetworkID: r.NetworkID, Name: r.Name, IPFamily: r.IPFamily, CIDR: r.CIDR, GatewayPolicy: r.GatewayPolicy, GatewayAddress: r.GatewayAddress, AllocationPolicy: r.AllocationPolicy, AllocationStart: r.AllocationStart, AllocationEnd: r.AllocationEnd, ReservedAddresses: r.ReservedAddresses, DNSServers: r.DNSServers, DHCPEnabled: r.DHCPEnabled}
		next := uint64(revision + 1)
		digest := subnetDesiredDigest(request, next, "RETIRE_PENDING")
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_resource_revision_evidence(subnet_id,subnet_revision,project_id,network_id,network_revision,subnet_name,ip_family,cidr,gateway_policy,gateway_address,allocation_policy,allocation_start,allocation_end,reserved_addresses,dhcp_enabled,dns_servers,delete_protection,lifecycle_state,previous_revision,desired_digest) SELECT subnet_id,$2,project_id,network_id,network_revision,subnet_name,ip_family,cidr,gateway_policy,gateway_address,allocation_policy,allocation_start,allocation_end,reserved_addresses,dhcp_enabled,dns_servers,false,'RETIRE_PENDING',$3,$4 FROM kim.subnet_resource_revision_evidence WHERE subnet_id=$1 AND subnet_revision=$3`, subnetID, next, revision, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_subnets_current SET subnet_revision=$2,subnet_generation=$2,lifecycle_state='RETIRE_PENDING',desired_digest=$3,updated_at=statement_timestamp() WHERE subnet_id=$1`, subnetID, next, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ipam_pool_lifecycle_evidence(lifecycle_evidence_id,pool_id,pool_generation,subnet_id,subnet_revision,lifecycle_state,realization_terminal_evidence_id,lifecycle_digest) SELECT 'subnet-pool-freeze:'||pool_id||':'||pool_generation,pool_id,pool_generation,subnet_id,$2,'RETIRE_PENDING',NULL,$3 FROM kim.subnet_ipam_pools_current WHERE subnet_id=$1 AND lifecycle_state='ACTIVE'`, subnetID, next, digestNetworkResource(fmt.Sprintf("%s/%d/RETIRE_PENDING", subnetID, next))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ipam_pools_current SET subnet_revision=$2,lifecycle_state='RETIRE_PENDING',updated_at=statement_timestamp() WHERE subnet_id=$1`, subnetID, next); err != nil {
			return err
		}
		return insertSubnetRealizationOperationTx(ctx, tx, request, next, uint64(networkRevision), uint64(realGen+1), "RETIRE", "ABSENT")
	})
	if err != nil {
		return SubnetResource{}, err
	}
	return GetSubnetResource(ctx, db, subnetID)
}

func GetSubnetResource(ctx context.Context, db TxBeginner, subnetID string) (SubnetResource, error) {
	var r SubnetResource
	var rev, nrev, pgen, rgen int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT s.subnet_id,s.project_id,s.network_id,s.subnet_name,s.ip_family,s.cidr::text,s.gateway_policy,COALESCE(host(s.gateway_address),''),s.allocation_policy,host(s.allocation_start),host(s.allocation_end),ARRAY(SELECT host(x) FROM unnest(s.excluded_addresses) x),s.dhcp_enabled,ARRAY(SELECT host(x) FROM unnest(s.dns_servers) x),s.delete_protection,s.lifecycle_state,s.subnet_revision,e.network_revision,s.created_at,s.updated_at,p.pool_id,p.pool_generation,p.lifecycle_state,r.realization_generation,r.realization_state,r.operation_id FROM kim.network_subnets_current s JOIN kim.subnet_resource_revision_evidence e ON e.subnet_id=s.subnet_id AND e.subnet_revision=s.subnet_revision JOIN kim.subnet_ipam_pools_current p ON p.subnet_id=s.subnet_id JOIN kim.subnet_realizations_current r ON r.subnet_id=s.subnet_id WHERE s.subnet_id=$1 AND s.authority_source='SUBNET_RESOURCE'`, subnetID).Scan(&r.SubnetID, &r.ProjectID, &r.NetworkID, &r.Name, &r.IPFamily, &r.CIDR, &r.GatewayPolicy, &r.GatewayAddress, &r.AllocationPolicy, &r.AllocationStart, &r.AllocationEnd, &r.ReservedAddresses, &r.DHCPEnabled, &r.DNSServers, &r.DeleteProtection, &r.Lifecycle, &rev, &nrev, &r.CreatedAt, &r.UpdatedAt, &r.PoolID, &pgen, &r.PoolState, &rgen, &r.RealizationState, &r.OperationID)
	})
	r.Revision = uint64(rev)
	r.NetworkRevision = uint64(nrev)
	r.PoolGeneration = uint64(pgen)
	r.RealizationGeneration = uint64(rgen)
	return r, err
}
