package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

type OVNGeneveTunnelObservation struct {
	ObservationID, SourcePortID, DestinationPortID, SegmentClaimID string
	SourceChassisObservationID, DestinationChassisObservationID    string
	ObservationDigest, SourceTunnelInterfaceDigest                 string
	DestinationTunnelInterfaceDigest, VerifierArtifactDigest       string
	SourceMappingGeneration, DestinationMappingGeneration          uint64
	ObservationGeneration, PacketsSent, PacketsReceived            uint64
	ProbeProtocol                                                  string
	SourceTunnelPresent, DestinationTunnelPresent                  bool
}

func AcceptOVNGeneveTunnelObservation(ctx context.Context, db TxBeginner, observed OVNGeneveTunnelObservation) error {
	protocol := strings.ToUpper(observed.ProbeProtocol)
	if observed.ObservationID == "" || observed.SourcePortID == "" || observed.DestinationPortID == "" ||
		observed.SourcePortID == observed.DestinationPortID || observed.SegmentClaimID == "" ||
		observed.SourceChassisObservationID == "" || observed.DestinationChassisObservationID == "" ||
		observed.SourceMappingGeneration == 0 || observed.DestinationMappingGeneration == 0 ||
		observed.ObservationGeneration == 0 || observed.PacketsReceived > observed.PacketsSent ||
		(protocol != "ICMP" && protocol != "UDP") ||
		!allSHA256(observed.ObservationDigest, observed.SourceTunnelInterfaceDigest,
			observed.DestinationTunnelInterfaceDigest, observed.VerifierArtifactDigest) {
		return ErrPlacementConflict
	}
	typed := ovnadapter.TunnelObservation{
		SourceChassisMatches: true, DestinationChassisMatches: true,
		SourceTunnelPresent: observed.SourceTunnelPresent, DestinationTunnelPresent: observed.DestinationTunnelPresent,
		PacketsSent: observed.PacketsSent, PacketsReceived: observed.PacketsReceived,
	}
	state := typed.State()
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var current bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1
			FROM kim.network_ovn_control_plane_state_current source
			JOIN kim.network_ovn_control_plane_state_current destination ON destination.port_id=$2
			JOIN kim.port_bindings_current source_binding ON source_binding.port_id=source.port_id
			JOIN kim.port_bindings_current destination_binding ON destination_binding.port_id=destination.port_id
			JOIN kim.host_network_mappings_current source_mapping
			 ON source_mapping.host_id=source_binding.host_id AND source_mapping.segment_claim_id=$3
			JOIN kim.host_network_mappings_current destination_mapping
			 ON destination_mapping.host_id=destination_binding.host_id AND destination_mapping.segment_claim_id=$3
			JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=$3
			JOIN kim.ovn_chassis_encap_observation_evidence source_chassis
			 ON source_chassis.observation_id=source.chassis_encap_observation_id
			JOIN kim.ovn_chassis_encap_observation_evidence destination_chassis
			 ON destination_chassis.observation_id=destination.chassis_encap_observation_id
			WHERE source.port_id=$1 AND source.control_plane_status='CONTROL_PLANE_CONVERGED'
			 AND destination.control_plane_status='CONTROL_PLANE_CONVERGED'
			 AND source_binding.segment_claim_id=$3 AND destination_binding.segment_claim_id=$3
			 AND source_binding.host_id<>destination_binding.host_id
			 AND source_mapping.mapping_generation=$4 AND destination_mapping.mapping_generation=$5
			 AND source_mapping.mapping_state='CURRENT' AND destination_mapping.mapping_state='CURRENT'
			 AND segment.claim_state='ACTIVE'
			 AND source_chassis.observation_id=$6 AND source_chassis.chassis_encap_state='MATCHED'
			 AND destination_chassis.observation_id=$7 AND destination_chassis.chassis_encap_state='MATCHED'
		)`, observed.SourcePortID, observed.DestinationPortID, observed.SegmentClaimID,
			observed.SourceMappingGeneration, observed.DestinationMappingGeneration,
			observed.SourceChassisObservationID, observed.DestinationChassisObservationID).Scan(&current); err != nil || !current {
			return ErrPlacementConflict
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kim.ovn_geneve_tunnel_observation_evidence(
			observation_id,source_port_id,destination_port_id,source_chassis_observation_id,
			destination_chassis_observation_id,segment_claim_id,source_mapping_generation,
			destination_mapping_generation,observation_generation,observation_digest,
			source_tunnel_interface_digest,destination_tunnel_interface_digest,probe_protocol,
			packets_sent,packets_received,tunnel_state,verifier_artifact_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT(observation_id) DO NOTHING`, observed.ObservationID, observed.SourcePortID,
			observed.DestinationPortID, observed.SourceChassisObservationID,
			observed.DestinationChassisObservationID, observed.SegmentClaimID,
			observed.SourceMappingGeneration, observed.DestinationMappingGeneration,
			observed.ObservationGeneration, observed.ObservationDigest,
			observed.SourceTunnelInterfaceDigest, observed.DestinationTunnelInterfaceDigest,
			protocol, observed.PacketsSent, observed.PacketsReceived, state, observed.VerifierArtifactDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_geneve_tunnel_observation_evidence
			 WHERE observation_id=$1 AND source_port_id=$2 AND destination_port_id=$3
			 AND source_chassis_observation_id=$4 AND destination_chassis_observation_id=$5
			 AND segment_claim_id=$6 AND source_mapping_generation=$7 AND destination_mapping_generation=$8
			 AND observation_generation=$9 AND observation_digest=$10
			 AND source_tunnel_interface_digest=$11 AND destination_tunnel_interface_digest=$12
			 AND probe_protocol=$13 AND packets_sent=$14 AND packets_received=$15
			 AND tunnel_state=$16 AND verifier_artifact_digest=$17)`, observed.ObservationID,
				observed.SourcePortID, observed.DestinationPortID, observed.SourceChassisObservationID,
				observed.DestinationChassisObservationID, observed.SegmentClaimID,
				observed.SourceMappingGeneration, observed.DestinationMappingGeneration,
				observed.ObservationGeneration, observed.ObservationDigest,
				observed.SourceTunnelInterfaceDigest, observed.DestinationTunnelInterfaceDigest,
				protocol, observed.PacketsSent, observed.PacketsReceived, state,
				observed.VerifierArtifactDigest).Scan(&identical); err != nil || !identical {
				return ErrPlacementConflict
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.network_ovn_tunnel_state_current(
			source_port_id,destination_port_id,segment_claim_id,observation_generation,evidence_id,tunnel_state
		) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(source_port_id,destination_port_id,segment_claim_id) DO UPDATE SET
		 observation_generation=EXCLUDED.observation_generation,evidence_id=EXCLUDED.evidence_id,
		 tunnel_state=EXCLUDED.tunnel_state,updated_at=statement_timestamp()
		WHERE kim.network_ovn_tunnel_state_current.observation_generation<EXCLUDED.observation_generation`,
			observed.SourcePortID, observed.DestinationPortID, observed.SegmentClaimID,
			observed.ObservationGeneration, observed.ObservationID, state)
		return err
	})
}
