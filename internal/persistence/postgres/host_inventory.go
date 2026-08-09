package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

var ErrHostInventoryEvidenceConflict = errors.New("Host inventory evidence conflict")

// AcceptHostInventory commits the durable Message Receipt, immutable normalized
// snapshot, and rebuildable capability projection in one transaction.
func AcceptHostInventory(ctx context.Context, db TxBeginner, envelope session.Envelope, maxMessageBytes int) (receipt session.Receipt, returnedErr error) {
	if err := envelope.Validate(maxMessageBytes); err != nil {
		return session.Receipt{}, err
	}
	if envelope.Stream != session.StreamInventory || envelope.SchemaVersion != agentinventory.SnapshotSchemaV3 {
		return session.Receipt{}, errors.New("normalized Host inventory envelope is required")
	}
	snapshot, err := agentinventory.DecodeSnapshot(envelope.Payload)
	if err != nil {
		return session.Receipt{}, err
	}
	canonicalPayload, err := snapshot.MarshalCanonical()
	if err != nil {
		return session.Receipt{}, err
	}
	if !bytes.Equal(canonicalPayload, envelope.Payload) {
		return session.Receipt{}, errors.New("Host inventory payload is not canonical")
	}
	if snapshot.HostIdentity != envelope.HostIdentity || snapshot.ObservationGeneration != envelope.ResourceGeneration || snapshot.ObservationGeneration != envelope.Sequence {
		return session.Receipt{}, errors.New("Host inventory envelope identity/generation mismatch")
	}
	capabilityPayload, err := json.Marshal(snapshot.Capabilities)
	if err != nil {
		return session.Receipt{}, err
	}
	capabilityDigestBytes := sha256.Sum256(capabilityPayload)
	capabilityDigest := hex.EncodeToString(capabilityDigestBytes[:])

	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var err error
		receipt, err = acceptAgentMessageTx(ctx, tx, envelope)
		if err != nil || receipt.Disposition != "ACCEPTED" {
			return err
		}
		return applyHostInventoryTx(ctx, tx, envelope, snapshot, capabilityPayload, capabilityDigest)
	})
	return receipt, err
}

func applyHostInventoryTx(ctx context.Context, tx pgx.Tx, envelope session.Envelope, snapshot agentinventory.Snapshot, capabilityPayload []byte, capabilityDigest string) error {
	if err := lockHostAuthorityTx(ctx, tx, snapshot.HostIdentity); err != nil {
		return fmt.Errorf("lock Host capability authority: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.host_inventory_snapshots (
			host_id, observation_generation, message_id, schema_version,
			collection_status, payload_digest, capability_digest, snapshot_payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT DO NOTHING
	`, snapshot.HostIdentity, snapshot.ObservationGeneration, envelope.MessageID, snapshot.SchemaVersion,
		snapshot.CollectionStatus, envelope.PayloadDigest, capabilityDigest, envelope.Payload)
	if err != nil {
		return fmt.Errorf("commit Host inventory snapshot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var messageID, payloadDigest, acceptedCapabilityDigest string
		if err := tx.QueryRow(ctx, `
			SELECT message_id, payload_digest, capability_digest
			FROM kim.host_inventory_snapshots
			WHERE host_id = $1 AND observation_generation = $2
		`, snapshot.HostIdentity, snapshot.ObservationGeneration).Scan(&messageID, &payloadDigest, &acceptedCapabilityDigest); err != nil {
			return ErrHostInventoryEvidenceConflict
		}
		if messageID != envelope.MessageID || payloadDigest != envelope.PayloadDigest || acceptedCapabilityDigest != capabilityDigest {
			return ErrHostInventoryEvidenceConflict
		}
	}
	state := "CURRENT"
	if snapshot.CollectionStatus != "COMPLETE" {
		state = "DEGRADED"
	}
	tag, err = tx.Exec(ctx, `
		INSERT INTO kim.host_capability_projections (
			host_id, observation_generation, source_message_id, schema_version,
			projection_state, snapshot_digest, capability_digest, capability_payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (host_id) DO UPDATE SET
			observation_generation = EXCLUDED.observation_generation,
			source_message_id = EXCLUDED.source_message_id,
			schema_version = EXCLUDED.schema_version,
			projection_state = EXCLUDED.projection_state,
			snapshot_digest = EXCLUDED.snapshot_digest,
			capability_digest = EXCLUDED.capability_digest,
			capability_payload = EXCLUDED.capability_payload,
			updated_at = statement_timestamp()
		WHERE host_capability_projections.observation_generation < EXCLUDED.observation_generation
	`, snapshot.HostIdentity, snapshot.ObservationGeneration, envelope.MessageID, snapshot.SchemaVersion,
		state, envelope.PayloadDigest, capabilityDigest, capabilityPayload)
	if err != nil {
		return fmt.Errorf("update Host capability projection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var currentGeneration int64
		var currentDigest string
		if err := tx.QueryRow(ctx, `
			SELECT observation_generation, snapshot_digest
			FROM kim.host_capability_projections WHERE host_id = $1
		`, snapshot.HostIdentity).Scan(&currentGeneration, &currentDigest); err != nil {
			return fmt.Errorf("read Host capability projection: %w", err)
		}
		if uint64(currentGeneration) == snapshot.ObservationGeneration && currentDigest != envelope.PayloadDigest {
			return ErrHostInventoryEvidenceConflict
		}
	} else if err := fenceHostOperationAuthorityTx(ctx, tx, snapshot.HostIdentity, "capability_generation_changed"); err != nil {
		return err
	}
	if err := applyPCIDeviceProjectionsTx(ctx, tx, envelope, snapshot); err != nil {
		return err
	}
	return refreshHostSessionAuthorizationTx(ctx, tx, snapshot.HostIdentity)
}

func applyPCIDeviceProjectionsTx(ctx context.Context, tx pgx.Tx, envelope session.Envelope, snapshot agentinventory.Snapshot) error {
	for _, fragment := range snapshot.Fragments {
		if fragment.PCI == nil {
			continue
		}
		observationState := capabilityState(fragment.Capabilities, "kim.host.pci-observation.v1")
		for _, device := range fragment.PCI.Devices {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, snapshot.HostIdentity+"/"+device.Address); err != nil {
				return fmt.Errorf("lock PCI device projection %s: %w", device.Address, err)
			}
			payload, err := json.Marshal(device)
			if err != nil {
				return err
			}
			digestBytes := sha256.Sum256(payload)
			observationDigest := hex.EncodeToString(digestBytes[:])
			var vfIndex any
			if device.VFIndex != nil {
				vfIndex = *device.VFIndex
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO kim.host_pci_device_projections (
					host_id, device_address, observation_generation, source_message_id,
					observation_digest, observation_state, vendor_id, device_id,
					subsystem_vendor_id, subsystem_device_id, driver, device_revision,
					firmware_revision, numa_node_id, iommu_group, sriov_total_vfs,
					sriov_enabled_vfs, pf_address, vf_index, relationship_state,
					relationship_reason
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),
				          NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),$14,NULLIF($15,''),
				          $16,$17,NULLIF($18,''),$19,$20,NULLIF($21,''))
				ON CONFLICT (host_id, device_address) DO UPDATE SET
					observation_generation=EXCLUDED.observation_generation,
					source_message_id=EXCLUDED.source_message_id,
					observation_digest=EXCLUDED.observation_digest,
					observation_state=EXCLUDED.observation_state,
					vendor_id=EXCLUDED.vendor_id, device_id=EXCLUDED.device_id,
					subsystem_vendor_id=EXCLUDED.subsystem_vendor_id,
					subsystem_device_id=EXCLUDED.subsystem_device_id,
					driver=EXCLUDED.driver, device_revision=EXCLUDED.device_revision,
					firmware_revision=EXCLUDED.firmware_revision,
					numa_node_id=EXCLUDED.numa_node_id, iommu_group=EXCLUDED.iommu_group,
					sriov_total_vfs=EXCLUDED.sriov_total_vfs,
					sriov_enabled_vfs=EXCLUDED.sriov_enabled_vfs,
					pf_address=EXCLUDED.pf_address, vf_index=EXCLUDED.vf_index,
					relationship_state=EXCLUDED.relationship_state,
					relationship_reason=EXCLUDED.relationship_reason,
					updated_at=statement_timestamp()
				WHERE host_pci_device_projections.observation_generation < EXCLUDED.observation_generation
			`, snapshot.HostIdentity, device.Address, snapshot.ObservationGeneration, envelope.MessageID,
				observationDigest, observationState, device.VendorID, device.DeviceID,
				device.SubsystemVendorID, device.SubsystemDeviceID, device.Driver,
				device.DeviceRevision, device.FirmwareRevision, device.NUMANodeID,
				device.IOMMUGroup, device.SRIOVTotalVFs, device.SRIOVEnabledVFs,
				device.PFAddress, vfIndex, device.RelationshipState, device.RelationshipReason)
			if err != nil {
				return fmt.Errorf("update PCI device projection %s: %w", device.Address, err)
			}
		}
	}
	return nil
}

func capabilityState(capabilities []agentinventory.Capability, name string) agentinventory.Availability {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.State
		}
	}
	return agentinventory.AvailabilityUnknown
}
