package postgres

import (
	"context"
	"errors"
	"net/netip"

	"github.com/jackc/pgx/v5"
)

type NetworkFoundation struct {
	NetworkID, ProjectID, NetworkState                    string
	NetworkGeneration                                     uint64
	MTU                                                   uint32
	SubnetID, SubnetState                                 string
	SubnetGeneration                                      uint64
	CIDR, AllocationStart, AllocationEnd                  string
	ExcludedAddresses                                     []string
	SegmentClaimID, SegmentType, ScopeID, SegmentState    string
	SegmentID, SegmentGeneration, ProviderMappingRevision uint64
}

type HostNetworkMapping struct {
	HostID, SegmentClaimID, State string
	OVNChassisName                string
	Generation                    uint64
	MaximumMTU                    uint32
	SupportedBindingTypes         []string
}

// UpsertNetworkFoundation establishes the PostgreSQL Network/Subnet/Segment
// authority used by Placement. It does not contact an IPAM, switch, OVS, or
// OVN backend.
func UpsertNetworkFoundation(ctx context.Context, db TxBeginner, foundation NetworkFoundation) error {
	excluded, err := validateNetworkFoundation(foundation)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		for _, key := range []string{"network/" + foundation.NetworkID, "network-segment/" + foundation.SegmentType + "/" + foundation.ScopeID} {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
				return err
			}
		}
		networkTag, err := tx.Exec(ctx, `
			INSERT INTO kim.networks_current (
				network_id, project_id, network_generation, lifecycle_state, mtu,
				network_revision,network_name,network_profile,segment_policy,delete_protection,
				desired_digest,authority_source,created_at
			) VALUES ($1,$2,$3,$4,$5,$3,$1,'LEGACY_FOUNDATION','LEGACY_EXPLICIT',false,
				 encode(sha256(convert_to($1||':'||($3::bigint)::text||':'||($5::integer)::text,'UTF8')),'hex'),'LEGACY_FOUNDATION',statement_timestamp())
			ON CONFLICT (network_id) DO UPDATE SET
				project_id=EXCLUDED.project_id,
				network_generation=EXCLUDED.network_generation,
				lifecycle_state=EXCLUDED.lifecycle_state,
				mtu=EXCLUDED.mtu,
				updated_at=statement_timestamp()
			WHERE kim.networks_current.authority_source='LEGACY_FOUNDATION'
			  AND kim.networks_current.network_generation < EXCLUDED.network_generation
		`, foundation.NetworkID, foundation.ProjectID, foundation.NetworkGeneration, foundation.NetworkState, foundation.MTU)
		if err != nil {
			return err
		}
		if networkTag.RowsAffected() != 1 {
			var compatible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.networks_current
			 WHERE network_id=$1 AND project_id=$2 AND network_generation=$3 AND lifecycle_state=$4 AND mtu=$5)`,
				foundation.NetworkID, foundation.ProjectID, foundation.NetworkGeneration, foundation.NetworkState, foundation.MTU).Scan(&compatible); err != nil || !compatible {
				return ErrPlacementConflict
			}
		}
		subnetTag, err := tx.Exec(ctx, `
			INSERT INTO kim.network_subnets_current (
				subnet_id, network_id, subnet_generation, lifecycle_state, cidr,
				allocation_start, allocation_end, excluded_addresses,
				project_id,subnet_revision,subnet_name,ip_family,gateway_policy,allocation_policy,
				dhcp_enabled,dns_servers,delete_protection,desired_digest,authority_source,created_at
			) VALUES ($1,$2,$3,$4,$5::cidr,$6::inet,$7::inet,$8::inet[],
				$9,$3,$1,'IPV4','NONE','RANGE',false,'{}'::inet[],false,
				encode(sha256(convert_to($1||':'||($3::bigint)::text||':'||$5,'UTF8')),'hex'),'LEGACY_FOUNDATION',statement_timestamp())
			ON CONFLICT (subnet_id) DO UPDATE SET
				network_id=EXCLUDED.network_id,
				subnet_generation=EXCLUDED.subnet_generation,
				lifecycle_state=EXCLUDED.lifecycle_state,
				cidr=EXCLUDED.cidr,
				allocation_start=EXCLUDED.allocation_start,
				allocation_end=EXCLUDED.allocation_end,
				excluded_addresses=EXCLUDED.excluded_addresses,
				updated_at=statement_timestamp()
			WHERE kim.network_subnets_current.authority_source='LEGACY_FOUNDATION'
			  AND kim.network_subnets_current.subnet_generation < EXCLUDED.subnet_generation
		`, foundation.SubnetID, foundation.NetworkID, foundation.SubnetGeneration,
			foundation.SubnetState, foundation.CIDR, foundation.AllocationStart,
			foundation.AllocationEnd, excluded, foundation.ProjectID)
		if err != nil {
			return err
		}
		if subnetTag.RowsAffected() != 1 {
			var compatible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_subnets_current WHERE subnet_id=$1 AND network_id=$2 AND subnet_generation=$3 AND lifecycle_state=$4 AND cidr=$5::cidr AND allocation_start=$6::inet AND allocation_end=$7::inet)`, foundation.SubnetID, foundation.NetworkID, foundation.SubnetGeneration, foundation.SubnetState, foundation.CIDR, foundation.AllocationStart, foundation.AllocationEnd).Scan(&compatible); err != nil || !compatible {
				return ErrPlacementConflict
			}
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.network_segment_claims_current (
				segment_claim_id, network_id, segment_generation, segment_type,
				scope_id, segment_id, provider_mapping_revision, claim_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (segment_claim_id) DO UPDATE SET
				network_id=EXCLUDED.network_id,
				segment_generation=EXCLUDED.segment_generation,
				segment_type=EXCLUDED.segment_type,
				scope_id=EXCLUDED.scope_id,
				segment_id=EXCLUDED.segment_id,
				provider_mapping_revision=EXCLUDED.provider_mapping_revision,
				claim_state=EXCLUDED.claim_state,
				updated_at=statement_timestamp()
			WHERE kim.network_segment_claims_current.segment_generation < EXCLUDED.segment_generation
		`, foundation.SegmentClaimID, foundation.NetworkID, foundation.SegmentGeneration,
			foundation.SegmentType, foundation.ScopeID, foundation.SegmentID,
			foundation.ProviderMappingRevision, foundation.SegmentState)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			var compatible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_segment_claims_current WHERE segment_claim_id=$1 AND network_id=$2 AND segment_generation=$3 AND segment_type=$4 AND scope_id=$5 AND segment_id=$6 AND provider_mapping_revision=$7 AND claim_state=$8)`, foundation.SegmentClaimID, foundation.NetworkID, foundation.SegmentGeneration, foundation.SegmentType, foundation.ScopeID, foundation.SegmentID, foundation.ProviderMappingRevision, foundation.SegmentState).Scan(&compatible); err != nil || !compatible {
				return ErrPlacementConflict
			}
		}
		return nil
	})
}

func UpsertHostNetworkMapping(ctx context.Context, db TxBeginner, mapping HostNetworkMapping) error {
	if mapping.HostID == "" || mapping.SegmentClaimID == "" || mapping.Generation == 0 || mapping.MaximumMTU < 576 || len(mapping.SupportedBindingTypes) == 0 || (mapping.State != "CURRENT" && mapping.State != "STALE" && mapping.State != "UNKNOWN" && mapping.State != "BLOCKED") {
		return errors.New("complete Host Network mapping is required")
	}
	for _, bindingType := range mapping.SupportedBindingTypes {
		if bindingType != "OVS" && bindingType != "SRIOV_DIRECT" {
			return errors.New("unsupported Host Network binding type")
		}
		if bindingType == "OVS" && mapping.OVNChassisName == "" {
			return errors.New("OVS Host Network mapping requires an OVN Chassis name")
		}
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, mapping.HostID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_network_mappings_current (
				host_id, segment_claim_id, mapping_generation, mapping_state, maximum_mtu,
				supported_binding_types, ovn_chassis_name
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (host_id, segment_claim_id) DO UPDATE SET
				mapping_generation=EXCLUDED.mapping_generation,
				mapping_state=EXCLUDED.mapping_state,
				maximum_mtu=EXCLUDED.maximum_mtu,
				supported_binding_types=EXCLUDED.supported_binding_types,
				ovn_chassis_name=EXCLUDED.ovn_chassis_name,
				updated_at=statement_timestamp()
			WHERE kim.host_network_mappings_current.mapping_generation < EXCLUDED.mapping_generation
		`, mapping.HostID, mapping.SegmentClaimID, mapping.Generation, mapping.State,
			mapping.MaximumMTU, mapping.SupportedBindingTypes, mapping.OVNChassisName)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrPlacementConflict
		}
		return nil
	})
}

func validateNetworkFoundation(foundation NetworkFoundation) ([]netip.Addr, error) {
	if foundation.NetworkID == "" || foundation.ProjectID == "" || foundation.NetworkGeneration == 0 || foundation.MTU < 576 || foundation.SubnetID == "" || foundation.SubnetGeneration == 0 || foundation.SegmentClaimID == "" || foundation.SegmentGeneration == 0 || foundation.ScopeID == "" || foundation.ProviderMappingRevision == 0 {
		return nil, errors.New("complete Network foundation authority is required")
	}
	if foundation.NetworkState != "ACTIVE" && foundation.NetworkState != "DRAINING" && foundation.NetworkState != "DISABLED" {
		return nil, errors.New("invalid Network lifecycle state")
	}
	if foundation.SubnetState != "ACTIVE" && foundation.SubnetState != "DRAINING" && foundation.SubnetState != "DISABLED" {
		return nil, errors.New("invalid Subnet lifecycle state")
	}
	if foundation.SegmentState != "ACTIVE" && foundation.SegmentState != "RELEASE_PENDING" && foundation.SegmentState != "QUARANTINED" && foundation.SegmentState != "RELEASED" {
		return nil, errors.New("invalid Segment Claim state")
	}
	if (foundation.SegmentType != "VLAN" && foundation.SegmentType != "VNI") || foundation.SegmentID == 0 {
		return nil, errors.New("invalid Network segment")
	}
	prefix, err := netip.ParsePrefix(foundation.CIDR)
	if err != nil {
		return nil, errors.New("invalid Subnet CIDR")
	}
	start, err := netip.ParseAddr(foundation.AllocationStart)
	if err != nil || !prefix.Contains(start) {
		return nil, errors.New("invalid allocation start")
	}
	end, err := netip.ParseAddr(foundation.AllocationEnd)
	if err != nil || !prefix.Contains(end) || start.Compare(end) > 0 {
		return nil, errors.New("invalid allocation end")
	}
	excluded := make([]netip.Addr, 0, len(foundation.ExcludedAddresses))
	for _, raw := range foundation.ExcludedAddresses {
		address, err := netip.ParseAddr(raw)
		if err != nil || !prefix.Contains(address) {
			return nil, errors.New("invalid excluded address")
		}
		excluded = append(excluded, address)
	}
	return excluded, nil
}
