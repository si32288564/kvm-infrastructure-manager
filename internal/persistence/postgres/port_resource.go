package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

var ErrNoPortRealizationWork = errors.New("no Port realization work available")

type PortResourceRequest struct {
	PortID, ProjectID, NetworkID, Name              string
	MACPolicy, RequestedMAC, SubnetID               string
	IPAllocationMode, RequestedIP, AttachmentPolicy string
	DatapathProfile                                 string
	DeleteProtection                                bool
}

type PortResource struct {
	PortID, ProjectID, NetworkID, Name, MACPolicy          string
	RequestedMAC, SubnetID, IPAllocationMode, RequestedIP  string
	AttachmentPolicy, AttachmentState, DatapathProfile     string
	MACAllocationID, MACAddress, IPAllocationID, IPAddress string
	Lifecycle, RealizationState, OperationID               string
	Revision, NetworkRevision, SubnetRevision              uint64
	MACAllocationGeneration, IPAllocationGeneration        uint64
	RealizationGeneration                                  uint64
	DeleteProtection                                       bool
	CreatedAt, UpdatedAt                                   time.Time
}

type PortAttachmentRequest struct {
	PortID, AttachmentIntentID, WorkloadID string
	ExpectedPortRevision                   uint64
}

type PortRealizationClaim struct {
	OperationID, Owner, ClaimMode, OperationKind, PlanDigest string
	OperationGeneration, ClaimGeneration                     uint64
	CanonicalPlan                                            []byte
	LeaseExpiresAt                                           time.Time
}
type PortRealizationObservation struct {
	ObservationID, OperationID, LogicalPortName, BackendUUID string
	ObservationDigest, AdapterArtifactDigest                 string
	OperationGeneration, ObservationGeneration               uint64
	ApplyResponseState                                       string
	Observation                                              ovnadapter.PortResourceObservation
}

func canonicalPortResource(r PortResourceRequest) (PortResourceRequest, error) {
	if !networkResourceIDPattern.MatchString(r.PortID) || r.ProjectID == "" || r.NetworkID == "" || r.Name == "" || len(r.Name) > 255 || (r.MACPolicy != "AUTO" && r.MACPolicy != "EXPLICIT") || (r.IPAllocationMode != "NONE" && r.IPAllocationMode != "AUTO" && r.IPAllocationMode != "EXPLICIT") || (r.AttachmentPolicy != "ON_DEMAND" && r.AttachmentPolicy != "WORKLOAD") || r.DatapathProfile != "STANDARD" {
		return r, errors.New("complete STANDARD logical Port desired authority is required")
	}
	if r.MACPolicy == "AUTO" {
		if r.RequestedMAC != "" {
			return r, errors.New("AUTO MAC cannot be caller supplied")
		}
	} else {
		m, err := net.ParseMAC(r.RequestedMAC)
		if err != nil || len(m) != 6 || m[0]&1 != 0 || (m[0]|m[1]|m[2]|m[3]|m[4]|m[5]) == 0 {
			return r, errors.New("explicit MAC must be unicast")
		}
		r.RequestedMAC = m.String()
	}
	if r.IPAllocationMode == "NONE" {
		if r.SubnetID != "" || r.RequestedIP != "" {
			return r, errors.New("NONE IP cannot reference Subnet or address")
		}
	} else if r.SubnetID == "" {
		return r, errors.New("IP allocation requires a Subnet")
	}
	if r.IPAllocationMode == "AUTO" && r.RequestedIP != "" {
		return r, errors.New("AUTO IP cannot be caller supplied")
	}
	if r.IPAllocationMode == "EXPLICIT" {
		ip, err := netip.ParseAddr(r.RequestedIP)
		if err != nil || !ip.Is4() {
			return r, errors.New("explicit IPv4 address is required")
		}
		r.RequestedIP = ip.String()
	}
	return r, nil
}
func portResourceDigest(r PortResourceRequest, revision uint64, lifecycle string) string {
	raw, _ := json.Marshal(struct {
		Request   PortResourceRequest
		Revision  uint64
		Lifecycle string
	}{r, revision, lifecycle})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func CreatePortResource(ctx context.Context, db TxBeginner, request PortResourceRequest) (PortResource, error) {
	r, err := canonicalPortResource(request)
	if err != nil {
		return PortResource{}, err
	}
	digest := portResourceDigest(r, 1, "ACTIVE")
	err = RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `port-resource/`+r.PortID); err != nil {
			return err
		}
		var existing, source string
		if err := tx.QueryRow(ctx, `SELECT desired_digest,authority_source FROM kim.network_ports_current WHERE port_id=$1`, r.PortID).Scan(&existing, &source); err == nil {
			if source == "PORT_RESOURCE" && existing == digest {
				return nil
			}
			return ErrPlacementConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var networkRevision int64
		if err := tx.QueryRow(ctx, `SELECT n.network_revision FROM kim.networks_current n JOIN kim.network_realizations_current x ON x.network_id=n.network_id AND x.network_revision=n.network_revision WHERE n.network_id=$1 AND n.project_id=$2 AND n.authority_source='NETWORK_RESOURCE' AND n.lifecycle_state='ACTIVE' AND x.realization_state='VERIFIED' AND x.terminal_evidence_id IS NOT NULL FOR SHARE OF n,x`, r.NetworkID, r.ProjectID).Scan(&networkRevision); err != nil {
			return ErrPlacementConflict
		}
		var subnetRevision any
		if r.SubnetID != "" {
			var rev int64
			if err := tx.QueryRow(ctx, `SELECT s.subnet_revision FROM kim.network_subnets_current s JOIN kim.subnet_ipam_pools_current p USING(subnet_id) JOIN kim.subnet_realizations_current x ON x.subnet_id=s.subnet_id AND x.subnet_revision=s.subnet_revision WHERE s.subnet_id=$1 AND s.network_id=$2 AND s.project_id=$3 AND s.authority_source='SUBNET_RESOURCE' AND s.lifecycle_state='ACTIVE' AND p.lifecycle_state='ACTIVE' AND x.realization_state='VERIFIED' AND x.terminal_evidence_id IS NOT NULL FOR UPDATE OF p`, r.SubnetID, r.NetworkID, r.ProjectID).Scan(&rev); err != nil {
				return ErrPlacementConflict
			}
			subnetRevision = rev
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_resource_revision_evidence(port_id,port_revision,project_id,network_id,network_revision,subnet_id,subnet_revision,port_name,mac_policy,requested_mac,ip_allocation_mode,requested_ip,attachment_policy,datapath_profile,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,NULLIF($9,'')::macaddr,$10,NULLIF($11,'')::inet,$12,$13,$14,'ACTIVE',NULL,$15)`, r.PortID, r.ProjectID, r.NetworkID, networkRevision, r.SubnetID, subnetRevision, r.Name, r.MACPolicy, r.RequestedMAC, r.IPAllocationMode, r.RequestedIP, r.AttachmentPolicy, r.DatapathProfile, r.DeleteProtection, digest); err != nil {
			return err
		}
		macID := "port-mac:" + r.PortID
		mac, err := allocatePortMACTx(ctx, tx, macID, r, 1)
		if err != nil {
			return err
		}
		var ip *SubnetIPAllocation
		if r.IPAllocationMode != "NONE" {
			ip, err = allocatePortResourceIPTx(ctx, tx, "port-ip:"+r.PortID, r, 1)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_ports_current(port_id,placement_admission_id,project_id,workload_id,network_id,subnet_id,port_generation,desired_state,port_revision,port_name,mac_policy,requested_mac,ip_allocation_mode,requested_ip,attachment_policy,attachment_state,datapath_profile,delete_protection,desired_digest,authority_source,updated_at) VALUES($1,NULL,$2,NULL,$3,NULLIF($4,''),1,'ACTIVE',1,$5,$6,NULLIF($7,'')::macaddr,$8,NULLIF($9,'')::inet,$10,'UNATTACHED',$11,$12,$13,'PORT_RESOURCE',statement_timestamp())`, r.PortID, r.ProjectID, r.NetworkID, r.SubnetID, r.Name, r.MACPolicy, r.RequestedMAC, r.IPAllocationMode, r.RequestedIP, r.AttachmentPolicy, r.DatapathProfile, r.DeleteProtection, digest); err != nil {
			return err
		}
		return insertPortRealizationOperationTx(ctx, tx, r, 1, uint64(networkRevision), nullableUint(subnetRevision), mac, ip, 1, "REALIZE", "PRESENT", 0, "")
	})
	if err != nil {
		return PortResource{}, err
	}
	return GetPortResource(ctx, db, r.PortID)
}

func allocatePortMACTx(ctx context.Context, tx pgx.Tx, id string, r PortResourceRequest, revision uint64) (PortResource, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `port-mac/`+r.NetworkID); err != nil {
		return PortResource{}, err
	}
	legacyLock := `network-mac-auto/` + r.NetworkID
	if r.MACPolicy == "EXPLICIT" {
		legacyLock = `network-mac/` + r.NetworkID + `/` + r.RequestedMAC
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, legacyLock); err != nil {
		return PortResource{}, err
	}
	assigned := r.RequestedMAC
	if r.MACPolicy == "AUTO" {
		for i := uint64(0); i < 65536; i++ {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", r.PortID, i)))
			candidate := net.HardwareAddr([]byte{0x02, sum[0], sum[1], sum[2], sum[3], sum[4]}).String()
			var used bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.port_mac_allocations_current WHERE network_id=$1 AND assigned_mac=$2 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING') UNION ALL SELECT 1 FROM kim.network_identity_claims WHERE network_id=$1 AND claim_type='MAC' AND mac_address=$2 AND claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED'))`, r.NetworkID, candidate).Scan(&used); err != nil {
				return PortResource{}, err
			}
			if !used {
				assigned = candidate
				break
			}
		}
		if assigned == "" {
			return PortResource{}, ErrPlacementConflict
		}
	} else {
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.port_mac_allocations_current WHERE network_id=$1 AND assigned_mac=$2 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING') UNION ALL SELECT 1 FROM kim.network_identity_claims WHERE network_id=$1 AND claim_type='MAC' AND mac_address=$2 AND claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED'))`, r.NetworkID, assigned).Scan(&used); err != nil || used {
			return PortResource{}, ErrPlacementConflict
		}
	}
	digest := digestNetworkResource(fmt.Sprintf("%s/1/%s/%d/%s/%s", id, r.PortID, revision, r.MACPolicy, assigned))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.port_mac_allocation_decision_evidence(allocation_id,allocation_generation,port_id,port_revision,network_id,allocation_mode,requested_mac,assigned_mac,decision_digest,decision_state) VALUES($1,1,$2,$3,$4,$5,NULLIF($6,'')::macaddr,$7,$8,'ALLOCATED')`, id, r.PortID, revision, r.NetworkID, r.MACPolicy, r.RequestedMAC, assigned, digest); err != nil {
		return PortResource{}, ErrPlacementConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.port_mac_allocations_current(allocation_id,allocation_generation,port_id,port_revision,network_id,assigned_mac,allocation_state) VALUES($1,1,$2,$3,$4,$5,'ALLOCATED')`, id, r.PortID, revision, r.NetworkID, assigned); err != nil {
		return PortResource{}, ErrPlacementConflict
	}
	return PortResource{MACAllocationID: id, MACAllocationGeneration: 1, MACAddress: assigned}, nil
}

func allocatePortResourceIPTx(ctx context.Context, tx pgx.Tx, id string, r PortResourceRequest, revision uint64) (*SubnetIPAllocation, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `network-ipam/`+r.SubnetID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ipam_pools_current SET updated_at=statement_timestamp() WHERE subnet_id=$1 AND lifecycle_state='ACTIVE'`, r.SubnetID); err != nil {
		return nil, err
	}
	var a SubnetIPAllocation
	var start, end string
	var reserved []string
	var sr, pg int64
	if err := tx.QueryRow(ctx, `SELECT p.pool_id,p.pool_generation,p.subnet_revision,host(p.allocation_start),host(p.allocation_end),ARRAY(SELECT host(x) FROM unnest(p.reserved_addresses)x) FROM kim.subnet_ipam_pools_current p WHERE p.subnet_id=$1 AND p.lifecycle_state='ACTIVE' FOR UPDATE`, r.SubnetID).Scan(&a.PoolID, &pg, &sr, &start, &end, &reserved); err != nil {
		return nil, ErrPlacementConflict
	}
	assigned := r.RequestedIP
	if r.IPAllocationMode == "AUTO" {
		if err := tx.QueryRow(ctx, `SELECT host(($1::inet+c)::inet) FROM generate_series(0,($2::inet-$1::inet))c WHERE NOT(($1::inet+c)::inet=ANY($3::inet[])) AND NOT EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current a WHERE a.subnet_id=$4 AND a.assigned_address=($1::inet+c)::inet AND a.allocation_state IN ('ALLOCATED','RELEASE_PENDING')) ORDER BY c LIMIT 1`, start, end, reserved, r.SubnetID).Scan(&assigned); err != nil {
			return nil, ErrPlacementConflict
		}
	} else {
		ip, _ := netip.ParseAddr(assigned)
		s, _ := netip.ParseAddr(start)
		e, _ := netip.ParseAddr(end)
		if ip.Less(s) || e.Less(ip) {
			return nil, ErrPlacementConflict
		}
		for _, v := range reserved {
			if v == assigned {
				return nil, ErrPlacementConflict
			}
		}
	}
	digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s/%s/PORT/%s", id, r.SubnetID, sr, r.IPAllocationMode, assigned, r.PortID))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocation_decision_evidence(allocation_id,allocation_generation,subnet_id,subnet_revision,pool_id,pool_generation,allocation_mode,requested_address,assigned_address,owner_resource_type,owner_resource_id,request_digest,decision_state) VALUES($1,1,$2,$3,$4,$5,$6,CASE WHEN $6='EXPLICIT' THEN $7::inet END,$7,'PORT',$8,$9,'ALLOCATED')`, id, r.SubnetID, sr, a.PoolID, pg, r.IPAllocationMode, assigned, r.PortID, digest); err != nil {
		return nil, ErrPlacementConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocations_current(allocation_id,allocation_generation,subnet_id,subnet_revision,pool_id,pool_generation,assigned_address,owner_resource_type,owner_resource_id,allocation_state) VALUES($1,1,$2,$3,$4,$5,$6,'PORT',$7,'ALLOCATED')`, id, r.SubnetID, sr, a.PoolID, pg, assigned, r.PortID); err != nil {
		return nil, ErrPlacementConflict
	}
	a = SubnetIPAllocation{AllocationID: id, AllocationGeneration: 1, SubnetID: r.SubnetID, SubnetRevision: uint64(sr), PoolID: a.PoolID, PoolGeneration: uint64(pg), AssignedAddress: assigned, OwnerResourceType: "PORT", OwnerResourceID: r.PortID, State: "ALLOCATED"}
	return &a, nil
}
func nullableUint(v any) uint64 {
	if x, ok := v.(int64); ok {
		return uint64(x)
	}
	return 0
}

func insertPortRealizationOperationTx(ctx context.Context, tx pgx.Tx, r PortResourceRequest, revision, networkRevision, subnetRevision uint64, mac PortResource, ip *SubnetIPAllocation, generation uint64, kind, state string, bindingGeneration uint64, chassis string) error {
	op := fmt.Sprintf("port-realization:%s:%d", r.PortID, generation)
	ipAddr, ipID := "", ""
	var ipGen any
	var subnetID any
	var sr any
	if ip != nil {
		ipAddr, ipID = ip.AssignedAddress, ip.AllocationID
		ipGen = ip.AllocationGeneration
		subnetID = r.SubnetID
		sr = subnetRevision
	}
	plan, digest, err := ovnadapter.PlanPortResource(ovnadapter.PortResourceIntentInput{OperationID: op, OperationGeneration: 1, ProjectID: r.ProjectID, NetworkID: r.NetworkID, NetworkRevision: networkRevision, PortID: r.PortID, PortRevision: revision, RealizationGeneration: generation, MACAddress: mac.MACAddress, IPAddress: ipAddr, BindingGeneration: bindingGeneration, ExpectedChassis: chassis, DesiredState: state})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.port_realization_operation_evidence(operation_id,operation_generation,operation_kind,port_id,port_revision,network_id,network_revision,subnet_id,subnet_revision,mac_allocation_id,mac_allocation_generation,ip_allocation_id,ip_allocation_generation,binding_generation,realization_generation,schema_version,canonical_plan,plan_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,NULLIF($13,0),$14,$15,$16,$17)`, op, kind, r.PortID, revision, r.NetworkID, networkRevision, subnetID, sr, mac.MACAllocationID, mac.MACAllocationGeneration, ipID, ipGen, bindingGeneration, generation, ovnadapter.PortResourceIntentSchema, plan, digest); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.port_realization_operations_current(operation_id,operation_generation,port_id,port_revision,realization_generation,operation_kind,phase) VALUES($1,1,$2,$3,$4,$5,'PENDING') ON CONFLICT(port_id) DO UPDATE SET operation_id=EXCLUDED.operation_id,operation_generation=1,port_revision=EXCLUDED.port_revision,realization_generation=EXCLUDED.realization_generation,operation_kind=EXCLUDED.operation_kind,phase='PENDING',last_claim_generation=0,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state=NULL,terminal_evidence_id=NULL,updated_at=statement_timestamp()`, op, r.PortID, revision, generation, kind); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.port_realizations_current(port_id,port_revision,realization_generation,operation_id,operation_generation,realization_state) VALUES($1,$2,$3,$4,1,'PENDING') ON CONFLICT(port_id) DO UPDATE SET port_revision=EXCLUDED.port_revision,realization_generation=EXCLUDED.realization_generation,operation_id=EXCLUDED.operation_id,operation_generation=1,realization_state='PENDING',terminal_evidence_id=NULL,updated_at=statement_timestamp()`, r.PortID, revision, generation, op)
	return err
}

func GetPortResource(ctx context.Context, db TxBeginner, id string) (PortResource, error) {
	var p PortResource
	var sr, ig *int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.port_id,p.project_id,p.network_id,p.port_name,p.mac_policy,COALESCE(p.requested_mac::text,''),COALESCE(p.subnet_id,''),p.ip_allocation_mode,COALESCE(host(p.requested_ip),''),p.attachment_policy,p.attachment_state,p.datapath_profile,p.desired_state,p.port_revision,n.network_revision,s.subnet_revision,m.allocation_id,m.allocation_generation,m.assigned_mac::text,COALESCE(i.allocation_id,''),i.allocation_generation,COALESCE(host(i.assigned_address),''),p.delete_protection,r.realization_generation,r.realization_state,r.operation_id,p.created_at,p.updated_at FROM kim.network_ports_current p JOIN kim.networks_current n USING(network_id) LEFT JOIN kim.network_subnets_current s USING(subnet_id) JOIN kim.port_mac_allocations_current m USING(port_id) LEFT JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_type='PORT' AND i.owner_resource_id=p.port_id AND i.allocation_state IN ('ALLOCATED','RELEASE_PENDING') JOIN kim.port_realizations_current r USING(port_id) WHERE p.port_id=$1 AND p.authority_source='PORT_RESOURCE'`, id).Scan(&p.PortID, &p.ProjectID, &p.NetworkID, &p.Name, &p.MACPolicy, &p.RequestedMAC, &p.SubnetID, &p.IPAllocationMode, &p.RequestedIP, &p.AttachmentPolicy, &p.AttachmentState, &p.DatapathProfile, &p.Lifecycle, &p.Revision, &p.NetworkRevision, &sr, &p.MACAllocationID, &p.MACAllocationGeneration, &p.MACAddress, &p.IPAllocationID, &ig, &p.IPAddress, &p.DeleteProtection, &p.RealizationGeneration, &p.RealizationState, &p.OperationID, &p.CreatedAt, &p.UpdatedAt)
	})
	if sr != nil {
		p.SubnetRevision = uint64(*sr)
	}
	if ig != nil {
		p.IPAllocationGeneration = uint64(*ig)
	}
	return p, err
}

// UpdatePortResource authorizes metadata-policy replacement while identity
// policy is deliberately immutable in Phase 2. Re-addressing is a future
// explicit allocation transition, never an implicit Update side effect.
func UpdatePortResource(ctx context.Context, db TxBeginner, request PortResourceRequest, expected uint64) (PortResource, error) {
	r, err := canonicalPortResource(request)
	if err != nil || expected == 0 {
		return PortResource{}, errors.New("complete Port replacement revision is required")
	}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var current PortResourceRequest
		var revision, networkRevision, subnetRevision, realizationGeneration int64
		var mac PortResource
		var ipID, ipAddress string
		var ipGeneration *int64
		err := tx.QueryRow(ctx, `SELECT p.project_id,p.network_id,p.port_name,p.mac_policy,COALESCE(p.requested_mac::text,''),COALESCE(p.subnet_id,''),p.ip_allocation_mode,COALESCE(host(p.requested_ip),''),p.attachment_policy,p.datapath_profile,p.delete_protection,p.port_revision,n.network_revision,COALESCE(s.subnet_revision,0),x.realization_generation,m.allocation_id,m.allocation_generation,m.assigned_mac::text,COALESCE(i.allocation_id,''),i.allocation_generation,COALESCE(host(i.assigned_address),'') FROM kim.network_ports_current p JOIN kim.networks_current n USING(network_id) LEFT JOIN kim.network_subnets_current s USING(subnet_id) JOIN kim.port_realizations_current x USING(port_id) JOIN kim.port_mac_allocations_current m USING(port_id) LEFT JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_id=p.port_id AND i.allocation_state='ALLOCATED' WHERE p.port_id=$1 AND p.authority_source='PORT_RESOURCE' AND p.desired_state='ACTIVE' AND p.attachment_state='UNATTACHED' AND x.realization_state='VERIFIED' FOR UPDATE OF p,x`, r.PortID).Scan(&current.ProjectID, &current.NetworkID, &current.Name, &current.MACPolicy, &current.RequestedMAC, &current.SubnetID, &current.IPAllocationMode, &current.RequestedIP, &current.AttachmentPolicy, &current.DatapathProfile, &current.DeleteProtection, &revision, &networkRevision, &subnetRevision, &realizationGeneration, &mac.MACAllocationID, &mac.MACAllocationGeneration, &mac.MACAddress, &ipID, &ipGeneration, &ipAddress)
		if err != nil || uint64(revision) != expected || current.ProjectID != r.ProjectID || current.NetworkID != r.NetworkID || current.MACPolicy != r.MACPolicy || current.RequestedMAC != r.RequestedMAC || current.SubnetID != r.SubnetID || current.IPAllocationMode != r.IPAllocationMode || current.RequestedIP != r.RequestedIP || current.AttachmentPolicy != r.AttachmentPolicy || current.DatapathProfile != r.DatapathProfile {
			return ErrPlacementConflict
		}
		next := expected + 1
		digest := portResourceDigest(r, next, "ACTIVE")
		var subnetID any
		var sr any
		if r.SubnetID != "" {
			subnetID = r.SubnetID
			sr = subnetRevision
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_resource_revision_evidence(port_id,port_revision,project_id,network_id,network_revision,subnet_id,subnet_revision,port_name,mac_policy,requested_mac,ip_allocation_mode,requested_ip,attachment_policy,datapath_profile,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::macaddr,$11,NULLIF($12,'')::inet,$13,$14,$15,'ACTIVE',$16,$17)`, r.PortID, next, r.ProjectID, r.NetworkID, networkRevision, subnetID, sr, r.Name, r.MACPolicy, r.RequestedMAC, r.IPAllocationMode, r.RequestedIP, r.AttachmentPolicy, r.DatapathProfile, r.DeleteProtection, expected, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET port_revision=$2,port_name=$3,delete_protection=$4,desired_digest=$5,updated_at=statement_timestamp() WHERE port_id=$1 AND port_revision=$6`, r.PortID, next, r.Name, r.DeleteProtection, digest, expected); err != nil {
			return err
		}
		var ip *SubnetIPAllocation
		if ipID != "" {
			ip = &SubnetIPAllocation{AllocationID: ipID, AllocationGeneration: uint64(*ipGeneration), AssignedAddress: ipAddress}
		}
		return insertPortRealizationOperationTx(ctx, tx, r, next, uint64(networkRevision), uint64(subnetRevision), mac, ip, uint64(realizationGeneration+1), "REALIZE", "PRESENT", 0, "")
	})
	if err != nil {
		return PortResource{}, err
	}
	return GetPortResource(ctx, db, r.PortID)
}

func RequestPortAttachment(ctx context.Context, db TxBeginner, r PortAttachmentRequest) (PortResource, error) {
	if r.PortID == "" || r.AttachmentIntentID == "" || r.WorkloadID == "" || r.ExpectedPortRevision == 0 {
		return PortResource{}, errors.New("complete Port attachment intent is required")
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state string
		var rev int64
		if err := tx.QueryRow(ctx, `SELECT attachment_state,port_revision FROM kim.network_ports_current WHERE port_id=$1 AND authority_source='PORT_RESOURCE' FOR UPDATE`, r.PortID).Scan(&state, &rev); err != nil || uint64(rev) != r.ExpectedPortRevision || state != "UNATTACHED" {
			return ErrPlacementConflict
		}
		digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s", r.AttachmentIntentID, r.PortID, rev, r.WorkloadID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_attachment_intent_evidence(attachment_intent_id,port_id,port_revision,attachment_generation,workload_id,intent_state,intent_digest) VALUES($1,$2,$3,1,$4,'REQUESTED',$5)`, r.AttachmentIntentID, r.PortID, rev, r.WorkloadID, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_attachment_intents_current(port_id,port_revision,attachment_intent_id,attachment_generation,workload_id,intent_state) VALUES($1,$2,$3,1,$4,'REQUESTED')`, r.PortID, rev, r.AttachmentIntentID, r.WorkloadID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET workload_id=$2,attachment_state='ATTACHMENT_REQUESTED',updated_at=statement_timestamp() WHERE port_id=$1`, r.PortID, r.WorkloadID)
		return err
	})
	if err != nil {
		return PortResource{}, err
	}
	return GetPortResource(ctx, db, r.PortID)
}

func RetirePortResource(ctx context.Context, db TxBeginner, id string, expected uint64) (PortResource, error) {
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var p PortResource
		var requestedMAC, requestedIP, subnetID string
		var deleteProtection bool
		var generation int64
		var retiredIPGeneration *int64
		if err := tx.QueryRow(ctx, `SELECT p.project_id,p.network_id,p.port_name,p.mac_policy,COALESCE(p.requested_mac::text,''),COALESCE(p.subnet_id,''),p.ip_allocation_mode,COALESCE(host(p.requested_ip),''),p.attachment_policy,p.datapath_profile,p.delete_protection,p.port_revision,r.realization_generation,m.allocation_id,m.allocation_generation,m.assigned_mac::text,COALESCE(i.allocation_id,''),i.allocation_generation,COALESCE(host(i.assigned_address),'') FROM kim.network_ports_current p JOIN kim.port_realizations_current r USING(port_id) JOIN kim.port_mac_allocations_current m USING(port_id) LEFT JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_id=p.port_id AND i.allocation_state='ALLOCATED' WHERE p.port_id=$1 AND p.authority_source='PORT_RESOURCE' AND p.attachment_state='UNATTACHED' AND p.desired_state='ACTIVE' AND NOT EXISTS(SELECT 1 FROM kim.port_bindings_current b WHERE b.port_id=p.port_id AND b.binding_state<>'RELEASED') FOR UPDATE OF p,r,m`, id).Scan(&p.ProjectID, &p.NetworkID, &p.Name, &p.MACPolicy, &requestedMAC, &subnetID, &p.IPAllocationMode, &requestedIP, &p.AttachmentPolicy, &p.DatapathProfile, &deleteProtection, &p.Revision, &generation, &p.MACAllocationID, &p.MACAllocationGeneration, &p.MACAddress, &p.IPAllocationID, &retiredIPGeneration, &p.IPAddress); err != nil || p.Revision != expected || deleteProtection {
			return ErrPlacementConflict
		}
		p.PortID = id
		p.RequestedMAC = requestedMAC
		p.SubnetID = subnetID
		p.RequestedIP = requestedIP
		if retiredIPGeneration != nil {
			p.IPAllocationGeneration = uint64(*retiredIPGeneration)
		}
		var nr int64
		var sr *int64
		if err := tx.QueryRow(ctx, `SELECT n.network_revision,s.subnet_revision FROM kim.networks_current n LEFT JOIN kim.network_subnets_current s ON s.subnet_id=NULLIF($2,'') WHERE n.network_id=$1`, p.NetworkID, subnetID).Scan(&nr, &sr); err != nil {
			return err
		}
		next := expected + 1
		r := PortResourceRequest{PortID: id, ProjectID: p.ProjectID, NetworkID: p.NetworkID, Name: p.Name, MACPolicy: p.MACPolicy, RequestedMAC: requestedMAC, SubnetID: subnetID, IPAllocationMode: p.IPAllocationMode, RequestedIP: requestedIP, AttachmentPolicy: p.AttachmentPolicy, DatapathProfile: p.DatapathProfile, DeleteProtection: false}
		digest := portResourceDigest(r, next, "RETIRE_PENDING")
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_resource_revision_evidence(port_id,port_revision,project_id,network_id,network_revision,subnet_id,subnet_revision,port_name,mac_policy,requested_mac,ip_allocation_mode,requested_ip,attachment_policy,datapath_profile,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,NULLIF($10,'')::macaddr,$11,NULLIF($12,'')::inet,$13,$14,false,'RETIRE_PENDING',$15,$16)`, id, next, p.ProjectID, p.NetworkID, nr, subnetID, sr, p.Name, p.MACPolicy, requestedMAC, p.IPAllocationMode, requestedIP, p.AttachmentPolicy, p.DatapathProfile, expected, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET port_revision=$2,desired_state='RELEASE_PENDING',attachment_state='RETIRE_PENDING',desired_digest=$3,updated_at=statement_timestamp() WHERE port_id=$1`, id, next, digest); err != nil {
			return err
		}
		mac := PortResource{MACAllocationID: p.MACAllocationID, MACAllocationGeneration: p.MACAllocationGeneration, MACAddress: p.MACAddress}
		var ip *SubnetIPAllocation
		if p.IPAllocationID != "" {
			ip = &SubnetIPAllocation{AllocationID: p.IPAllocationID, AllocationGeneration: p.IPAllocationGeneration, AssignedAddress: p.IPAddress}
		}
		return insertPortRealizationOperationTx(ctx, tx, r, next, uint64(nr), func() uint64 {
			if sr != nil {
				return uint64(*sr)
			}
			return 0
		}(), mac, ip, uint64(generation+1), "RETIRE", "ABSENT", 0, "")
	})
	if err != nil {
		return PortResource{}, err
	}
	return GetPortResource(ctx, db, id)
}

func ClaimPortRealization(ctx context.Context, db TxBeginner, operationID, owner string, lease time.Duration) (PortRealizationClaim, error) {
	if owner == "" || lease <= 0 || lease > MaxOVNRuntimeClaimLifetime {
		return PortRealizationClaim{}, errors.New("bounded Port realization claim is required")
	}
	var c PortRealizationClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var phase string
		var gen int64
		if err := tx.QueryRow(ctx, `SELECT c.operation_id,c.operation_generation,c.operation_kind,c.phase,e.canonical_plan,e.plan_digest FROM kim.port_realization_operations_current c JOIN kim.port_realization_operation_evidence e USING(operation_id,operation_generation) WHERE ($1='' OR c.operation_id=$1) AND (c.phase IN ('PENDING','DISPATCH_UNKNOWN') OR(c.phase='CLAIMED' AND c.claim_expires_at<=statement_timestamp())) ORDER BY c.updated_at FOR UPDATE OF c SKIP LOCKED LIMIT 1`, operationID).Scan(&c.OperationID, &c.OperationGeneration, &c.OperationKind, &phase, &c.CanonicalPlan, &c.PlanDigest); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoPortRealizationWork
		} else if err != nil {
			return err
		}
		c.ClaimMode = "APPLY_ALLOWED"
		if phase != "PENDING" {
			c.ClaimMode = "READ_BACK_FIRST"
		}
		if err := tx.QueryRow(ctx, `UPDATE kim.port_realization_operations_current SET phase='CLAIMED',claim_owner=$2,claim_generation=last_claim_generation+1,last_claim_generation=last_claim_generation+1,claim_expires_at=statement_timestamp()+($3*interval '1 microsecond'),updated_at=statement_timestamp() WHERE operation_id=$1 RETURNING claim_generation,claim_expires_at`, c.OperationID, owner, lease.Microseconds()).Scan(&gen, &c.LeaseExpiresAt); err != nil {
			return err
		}
		c.Owner = owner
		c.ClaimGeneration = uint64(gen)
		_, err := tx.Exec(ctx, `INSERT INTO kim.port_realization_attempt_evidence(operation_id,operation_generation,claim_generation,claim_owner,claim_mode,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6)`, c.OperationID, c.OperationGeneration, gen, owner, c.ClaimMode, c.LeaseExpiresAt)
		return err
	})
	return c, err
}
func lockPortRealizationClaim(ctx context.Context, tx pgx.Tx, c PortRealizationClaim) error {
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return err
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.port_realization_operations_current WHERE operation_id=$1 AND operation_generation=$2 AND phase='CLAIMED' AND claim_owner=$3 AND claim_generation=$4 AND claim_expires_at>statement_timestamp())`, c.OperationID, c.OperationGeneration, c.Owner, c.ClaimGeneration).Scan(&ok); err != nil || !ok {
		return ErrPlacementConflict
	}
	return nil
}
func MarkPortRealizationDispatchUnknown(ctx context.Context, db TxBeginner, c PortRealizationClaim) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockPortRealizationClaim(ctx, tx, c); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.port_realization_operations_current SET phase='DISPATCH_UNKNOWN',response_state='UNKNOWN',claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID)
		return err
	})
}
func RecordPortRealizationReadBackStarted(ctx context.Context, db TxBeginner, c PortRealizationClaim) error {
	return recordPortEvent(ctx, db, c, "READ_BACK_STARTED")
}
func AuthorizePortRealizationApply(ctx context.Context, db TxBeginner, c PortRealizationClaim) error {
	return recordPortEvent(ctx, db, c, "APPLY_AUTHORIZED")
}
func recordPortEvent(ctx context.Context, db TxBeginner, c PortRealizationClaim, event string) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockPortRealizationClaim(ctx, tx, c); err != nil {
			return err
		}
		if c.ClaimMode == "READ_BACK_FIRST" && event == "APPLY_AUTHORIZED" {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.port_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='READ_BACK_STARTED')`, c.OperationID, c.OperationGeneration, c.ClaimGeneration).Scan(&exists); err != nil || !exists {
				return ErrPlacementConflict
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.port_realization_attempt_event_evidence(operation_id,operation_generation,claim_generation,event_type) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, c.OperationID, c.OperationGeneration, c.ClaimGeneration, event)
		return err
	})
}

func AcceptPortRealizationObservation(ctx context.Context, db TxBeginner, c PortRealizationClaim, o PortRealizationObservation) (string, error) {
	var terminal string
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockPortRealizationClaim(ctx, tx, c); err != nil {
			return err
		}
		var portID, kind string
		var pr, rg int64
		if err := tx.QueryRow(ctx, `SELECT port_id,port_revision,realization_generation,operation_kind FROM kim.port_realization_operation_evidence WHERE operation_id=$1 AND operation_generation=$2`, c.OperationID, c.OperationGeneration).Scan(&portID, &pr, &rg, &kind); err != nil {
			return err
		}
		desired := "PRESENT"
		if kind == "RETIRE" {
			desired = "ABSENT"
		}
		state := o.Observation.State(desired)
		if state == "UNKNOWN" {
			if _, err := tx.Exec(ctx, `UPDATE kim.port_realization_operations_current SET phase='DISPATCH_UNKNOWN',response_state=$2,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID, o.ApplyResponseState); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE kim.port_realizations_current SET realization_state='UNKNOWN',updated_at=statement_timestamp() WHERE port_id=$1 AND realization_generation=$2`, portID, rg)
			return err
		}
		if o.ObservationID == "" || o.OperationID != c.OperationID || o.OperationGeneration != c.OperationGeneration || o.ObservationGeneration != c.ClaimGeneration || len(o.ObservationDigest) != 64 || len(o.AdapterArtifactDigest) != 64 {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_realization_observation_evidence(observation_id,operation_id,operation_generation,port_id,port_revision,realization_generation,observation_generation,apply_response_state,logical_port_name,backend_uuid,object_present,ownership_marker_matches,plan_digest_matches,network_matches,mac_matches,ip_matches,binding_matches,observation_digest,adapter_artifact_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15,$16,$17,$18,$19)`, o.ObservationID, o.OperationID, o.OperationGeneration, portID, pr, rg, o.ObservationGeneration, o.ApplyResponseState, o.LogicalPortName, o.BackendUUID, o.Observation.ObjectPresent, o.Observation.OwnershipMarkerMatches, o.Observation.PlanDigestMatches, o.Observation.NetworkMatches, o.Observation.MACMatches, o.Observation.IPMatches, o.Observation.BindingMatches, o.ObservationDigest, o.AdapterArtifactDigest); err != nil {
			return err
		}
		terminal = fmt.Sprintf("port-terminal:%s:%d", c.OperationID, c.ClaimGeneration)
		td := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%d/%s", terminal, portID, pr, rg, state))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.port_realization_terminal_evidence(terminal_evidence_id,operation_id,operation_generation,observation_id,port_id,port_revision,realization_generation,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, terminal, c.OperationID, c.OperationGeneration, o.ObservationID, portID, pr, rg, state, td); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.port_realization_operations_current SET phase='SUCCEEDED',response_state=$2,terminal_evidence_id=$3,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID, o.ApplyResponseState, terminal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.port_realizations_current SET realization_state=$2,terminal_evidence_id=$3,updated_at=statement_timestamp() WHERE port_id=$1 AND realization_generation=$4`, portID, state, terminal, rg); err != nil {
			return err
		}
		if state == "ABSENT" {
			return completePortRetirementTx(ctx, tx, portID, uint64(pr), terminal)
		}
		return nil
	})
	return terminal, err
}

func completePortRetirementTx(ctx context.Context, tx pgx.Tx, portID string, revision uint64, terminal string) error {
	var macID string
	var macGen int64
	var ipID *string
	var ipGen *int64
	if err := tx.QueryRow(ctx, `SELECT m.allocation_id,m.allocation_generation,i.allocation_id,i.allocation_generation FROM kim.port_mac_allocations_current m LEFT JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_id=m.port_id AND i.allocation_state IN ('ALLOCATED','RELEASE_PENDING') WHERE m.port_id=$1 AND m.allocation_state IN ('ALLOCATED','RELEASE_PENDING') FOR UPDATE OF m`, portID).Scan(&macID, &macGen, &ipID, &ipGen); err != nil {
		return err
	}
	releaseID := "port-identity-release:" + portID + fmt.Sprintf(":%d", revision)
	digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s", releaseID, portID, revision, terminal))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.port_identity_release_evidence(release_evidence_id,port_id,port_revision,mac_allocation_id,mac_allocation_generation,ip_allocation_id,ip_allocation_generation,backend_absence_terminal_evidence_id,release_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, releaseID, portID, revision, macID, macGen, ipID, ipGen, terminal, digest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.port_mac_allocations_current SET allocation_state='RELEASED',updated_at=statement_timestamp() WHERE allocation_id=$1 AND allocation_generation=$2 AND port_id=$3`, macID, macGen, portID); err != nil {
		return err
	}
	if ipID != nil {
		if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ip_allocations_current SET allocation_state='RELEASED',updated_at=statement_timestamp() WHERE allocation_id=$1 AND allocation_generation=$2 AND owner_resource_id=$3`, *ipID, *ipGen, portID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET desired_state='RELEASED',attachment_state='DELETED',updated_at=statement_timestamp() WHERE port_id=$1 AND port_revision=$2 AND attachment_state='RETIRE_PENDING'`, portID, revision)
	return err
}
