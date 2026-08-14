package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maximumAutomaticIPv4PoolSize = 1 << 16

type NetworkIdentityReleaseObservation struct {
	ObservationID, IdentityClaimID, PortID, EvidenceState string
	ClaimGeneration, PortGeneration, BindingGeneration    uint64
	ObservationGeneration                                 uint64
	PortAbsent, BindingAbsent, OVNNBAbsent                bool
	OVNSBAbsent, HostAbsent                               bool
	VerifierArtifactDigest                                string
	ObservedAt                                            time.Time
}

func allocateAutomaticNetworkIdentitiesTx(ctx context.Context, tx pgx.Tx, requestID string, requiredPortID string, networkID string, subnetID string) (string, string, error) {
	var cidr, startRaw, endRaw string
	var excludedRaw []string
	if err := tx.QueryRow(ctx, `
		SELECT cidr::text, host(allocation_start), host(allocation_end),
		       ARRAY(SELECT host(address) FROM unnest(excluded_addresses) address)
		FROM kim.network_subnets_current
		WHERE subnet_id=$1 AND network_id=$2 AND lifecycle_state='ACTIVE'
		FOR UPDATE
	`, subnetID, networkID).Scan(&cidr, &startRaw, &endRaw, &excludedRaw); err != nil {
		return "", "", err
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() {
		return "", "", errors.New("automatic IPAM supports bounded IPv4 Subnets only")
	}
	start, err := netip.ParseAddr(startRaw)
	if err != nil || !start.Is4() || !prefix.Contains(start) {
		return "", "", errors.New("invalid automatic IPAM allocation start")
	}
	end, err := netip.ParseAddr(endRaw)
	if err != nil || !end.Is4() || !prefix.Contains(end) {
		return "", "", errors.New("invalid automatic IPAM allocation end")
	}
	startNumber, endNumber := ipv4Number(start), ipv4Number(end)
	if endNumber < startNumber || endNumber-startNumber+1 > maximumAutomaticIPv4PoolSize {
		return "", "", errors.New("automatic IPAM pool exceeds the Developer Preview bound")
	}
	excluded := make(map[string]struct{}, len(excludedRaw))
	for _, raw := range excludedRaw {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			return "", "", err
		}
		excluded[address.String()] = struct{}{}
	}
	usedIP := map[string]struct{}{}
	rows, err := tx.Query(ctx, `
		SELECT host(ip_address)
		FROM kim.network_identity_claims
		WHERE subnet_id=$1 AND claim_type='IP'
		  AND claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED')
		UNION
		SELECT host(assigned_address)
		FROM kim.subnet_ip_allocations_current
		WHERE subnet_id=$1 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING')
	`, subnetID)
	if err != nil {
		return "", "", err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return "", "", err
		}
		usedIP[raw] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", "", err
	}
	rows.Close()
	seed := sha256.Sum256([]byte(requestID + "\x00" + requiredPortID))
	poolSize := uint64(endNumber) - uint64(startNumber) + 1
	startOffset := uint64(binary.BigEndian.Uint32(seed[:4])) % poolSize
	selectedIP := ""
	for offset := uint64(0); offset < poolSize; offset++ {
		candidate := ipv4Address(uint32(uint64(startNumber) + ((startOffset + offset) % poolSize))).String()
		if _, blocked := excluded[candidate]; blocked {
			continue
		}
		if _, claimed := usedIP[candidate]; claimed {
			continue
		}
		selectedIP = candidate
		break
	}
	if selectedIP == "" {
		return "", "", ErrPlacementIneligible
	}

	usedMAC := map[string]struct{}{}
	rows, err = tx.Query(ctx, `
		SELECT mac_address::text
		FROM kim.network_identity_claims
		WHERE network_id=$1 AND claim_type='MAC'
		  AND claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED')
	`, networkID)
	if err != nil {
		return "", "", err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return "", "", err
		}
		parsed, err := net.ParseMAC(raw)
		if err != nil {
			rows.Close()
			return "", "", err
		}
		usedMAC[parsed.String()] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", "", err
	}
	rows.Close()
	selectedMAC := ""
	for attempt := uint32(0); attempt < 256; attempt++ {
		material := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", requestID, requiredPortID, attempt)))
		candidate := net.HardwareAddr{0x02, material[0], material[1], material[2], material[3], material[4]}.String()
		if _, claimed := usedMAC[candidate]; !claimed {
			selectedMAC = candidate
			break
		}
	}
	if selectedMAC == "" {
		return "", "", ErrPlacementIneligible
	}
	return selectedIP, selectedMAC, nil
}

func ipv4Number(address netip.Addr) uint32 {
	octets := address.As4()
	return binary.BigEndian.Uint32(octets[:])
}

func ipv4Address(value uint32) netip.Addr {
	var octets [4]byte
	binary.BigEndian.PutUint32(octets[:], value)
	return netip.AddrFrom4(octets)
}

func BeginNetworkPortRelease(ctx context.Context, db TxBeginner, portID string, expectedPortGeneration uint64) error {
	if portID == "" || expectedPortGeneration == 0 {
		return errors.New("Port identity and generation are required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "network-port/"+portID); err != nil {
			return err
		}
		var generation uint64
		var state string
		if err := tx.QueryRow(ctx, `SELECT port_generation, desired_state FROM kim.network_ports_current WHERE port_id=$1 FOR UPDATE`, portID).Scan(&generation, &state); err != nil {
			return err
		}
		if generation != expectedPortGeneration {
			return ErrPlacementStale
		}
		if state == "RELEASE_PENDING" || state == "QUARANTINED" || state == "RELEASED" {
			return nil
		}
		if state != "RESERVED" && state != "BINDING" && state != "ACTIVE" {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET desired_state='RELEASE_PENDING' WHERE port_id=$1`, portID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_state='RELEASE_PENDING' WHERE port_id=$1 AND binding_state <> 'RELEASED'`, portID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_identity_claims SET claim_state='RELEASE_PENDING' WHERE port_id=$1 AND claim_state <> 'RELEASED'`, portID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.subnet_ip_allocations_current allocation SET allocation_state='RELEASE_PENDING',updated_at=statement_timestamp() FROM kim.network_identity_claims claim WHERE claim.port_id=$1 AND claim.ip_allocation_id=allocation.allocation_id AND claim.ip_allocation_generation=allocation.allocation_generation AND allocation.allocation_state='ALLOCATED'`, portID)
		return err
	})
}

func RecordNetworkIdentityReleaseObservation(ctx context.Context, db TxBeginner, observation NetworkIdentityReleaseObservation) (string, error) {
	if observation.ObservationID == "" || observation.IdentityClaimID == "" || observation.PortID == "" || observation.ClaimGeneration == 0 || observation.PortGeneration == 0 || observation.BindingGeneration == 0 || observation.ObservationGeneration == 0 || observation.ObservedAt.IsZero() || (observation.EvidenceState != "MATCHED" && observation.EvidenceState != "CONFLICTING" && observation.EvidenceState != "UNKNOWN") || len(observation.VerifierArtifactDigest) != 64 {
		return "", errors.New("complete Network identity release observation is required")
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	digestString := hex.EncodeToString(digest[:])
	state := ""
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "network-port/"+observation.PortID); err != nil {
			return err
		}
		var claimPortID, claimState string
		var claimGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT port_id, claim_generation, claim_state FROM kim.network_identity_claims WHERE identity_claim_id=$1 FOR UPDATE`, observation.IdentityClaimID).Scan(&claimPortID, &claimGeneration, &claimState); err != nil {
			return err
		}
		var portGeneration, bindingGeneration uint64
		var portState, bindingState string
		if err := tx.QueryRow(ctx, `
			SELECT port.port_generation, port.desired_state,
			       binding.binding_generation, binding.binding_state
			FROM kim.network_ports_current port
			JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id
			WHERE port.port_id=$1
			FOR UPDATE OF port, binding
		`, observation.PortID).Scan(&portGeneration, &portState, &bindingGeneration, &bindingState); err != nil {
			return err
		}
		if claimPortID != observation.PortID || claimGeneration != observation.ClaimGeneration || portGeneration != observation.PortGeneration || bindingGeneration != observation.BindingGeneration {
			return ErrPlacementStale
		}
		var existingDigest string
		err := tx.QueryRow(ctx, `
			SELECT observation_digest
			FROM kim.network_identity_release_observation_evidence
			WHERE observation_id=$1
		`, observation.ObservationID).Scan(&existingDigest)
		if err == nil {
			if existingDigest != digestString {
				return ErrPlacementConflict
			}
			state = claimState
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if claimState == "RELEASED" {
			return ErrPlacementStale
		}
		if (claimState != "RELEASE_PENDING" && claimState != "QUARANTINED") ||
			(portState != "RELEASE_PENDING" && portState != "QUARANTINED") ||
			(bindingState != "RELEASE_PENDING" && bindingState != "QUARANTINED") {
			return ErrPlacementConflict
		}
		var latestObservationGeneration uint64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(observation_generation), 0)
			FROM kim.network_identity_release_observation_evidence
			WHERE identity_claim_id=$1
		`, observation.IdentityClaimID).Scan(&latestObservationGeneration); err != nil {
			return err
		}
		if observation.ObservationGeneration <= latestObservationGeneration {
			return ErrPlacementStale
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.network_identity_release_observation_evidence (
				observation_id, identity_claim_id, port_id, claim_generation,
				port_generation, binding_generation, observation_generation,
				evidence_state, port_absent, binding_absent, ovn_nb_absent,
				ovn_sb_absent, host_absent, observation_digest,
				verifier_artifact_digest, observed_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		`, observation.ObservationID, observation.IdentityClaimID, observation.PortID,
			observation.ClaimGeneration, observation.PortGeneration, observation.BindingGeneration,
			observation.ObservationGeneration, observation.EvidenceState, observation.PortAbsent,
			observation.BindingAbsent, observation.OVNNBAbsent, observation.OVNSBAbsent,
			observation.HostAbsent, digestString, observation.VerifierArtifactDigest, observation.ObservedAt)
		if err != nil {
			var pgError *pgconn.PgError
			if errors.As(err, &pgError) && pgError.Code == "23505" {
				return ErrPlacementConflict
			}
			return err
		}
		clean := observation.EvidenceState == "MATCHED" && observation.PortAbsent && observation.BindingAbsent && observation.OVNNBAbsent && observation.OVNSBAbsent && observation.HostAbsent
		if !clean {
			state = "QUARANTINED"
		} else {
			var priorClean bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM kim.network_identity_release_observation_evidence
					WHERE identity_claim_id=$1 AND observation_generation < $2
					  AND evidence_state='MATCHED' AND port_absent AND binding_absent
					  AND ovn_nb_absent AND ovn_sb_absent AND host_absent
				)
			`, observation.IdentityClaimID, observation.ObservationGeneration).Scan(&priorClean); err != nil {
				return err
			}
			if claimState == "QUARANTINED" && priorClean {
				state = "RELEASED"
			} else {
				state = "QUARANTINED"
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_identity_claims SET claim_state=$2 WHERE identity_claim_id=$1`, observation.IdentityClaimID, state); err != nil {
			return err
		}
		if state == "RELEASED" {
			var allocationID *string
			var allocationGeneration, subnetRevision *int64
			var subnetID string
			if err := tx.QueryRow(ctx, `SELECT ip_allocation_id,ip_allocation_generation,subnet_revision,subnet_id FROM kim.network_identity_claims WHERE identity_claim_id=$1`, observation.IdentityClaimID).Scan(&allocationID, &allocationGeneration, &subnetRevision, &subnetID); err != nil {
				return err
			}
			if allocationID != nil && allocationGeneration != nil && subnetRevision != nil {
				releaseID := "subnet-ip-release:" + *allocationID
				releaseDigest := digestNetworkResource(fmt.Sprintf("%s/%d/%s", *allocationID, *allocationGeneration, observation.ObservationID))
				if _, err := tx.Exec(ctx, `INSERT INTO kim.subnet_ip_allocation_release_evidence(release_evidence_id,allocation_id,allocation_generation,subnet_id,subnet_revision,release_observation_id,release_digest) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(release_evidence_id) DO NOTHING`, releaseID, *allocationID, *allocationGeneration, subnetID, *subnetRevision, observation.ObservationID, releaseDigest); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE kim.subnet_ip_allocations_current SET allocation_state='RELEASED',updated_at=statement_timestamp() WHERE allocation_id=$1 AND allocation_generation=$2 AND allocation_state='RELEASE_PENDING'`, *allocationID, *allocationGeneration); err != nil {
					return err
				}
			}
		}
		if state == "RELEASED" {
			var outstanding bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kim.network_identity_claims WHERE port_id=$1 AND claim_state <> 'RELEASED')`, observation.PortID).Scan(&outstanding); err != nil {
				return err
			}
			if !outstanding {
				if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_state='RELEASED' WHERE port_id=$1`, observation.PortID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET desired_state='RELEASED' WHERE port_id=$1`, observation.PortID); err != nil {
					return err
				}
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET desired_state='QUARANTINED' WHERE port_id=$1 AND desired_state <> 'RELEASED'`, observation.PortID); err != nil {
				return err
			}
		}
		return nil
	})
	return state, err
}
