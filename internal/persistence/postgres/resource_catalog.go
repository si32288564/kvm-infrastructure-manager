package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	ErrImageIntegrity       = errors.New("image integrity validation failed")
	ErrCatalogConflict      = errors.New("catalog revision conflict")
	ErrFlavorShapeInvalid   = errors.New("flavor shape is invalid")
	ErrCatalogAuthorityMode = errors.New("database authority does not allow catalog mutation")
)

type ImageRevision struct {
	ImageID, OwnerProjectID, Format, SourceURI, Visibility string
	Revision, SizeBytes                                    uint64
	DeclaredChecksum, ObservedChecksum                     string
	SignatureState, SignatureDigest                        string
	Metadata                                               map[string]string
}

type ImageRegistrationDecision struct {
	State, Reason, MetadataDigest string
}

type FlavorRevision struct {
	FlavorID, OwnerProjectID, Name, NUMAPolicy, CPUAllocation string
	Revision, VCPUs, MemoryMiB, RootDiskGiB                   uint64
	NUMANodes                                                 *uint32
	HugePageSizeKiB                                           *uint64
	CPUPinning                                                bool
	ExtraSpecs                                                map[string]string
}

// PlacementShape is the immutable Flavor input handed to dry evaluation.
// It deliberately contains no transport or backend implementation detail.
type PlacementShape struct {
	FlavorID, ShapeDigest, NUMAPolicy, CPUAllocation string
	FlavorRevision, VCPUs, MemoryMiB, RootDiskGiB    uint64
	NUMANodes                                        *uint32
	HugePageSizeKiB                                  *uint64
	CPUPinning                                       bool
	ExtraSpecs                                       map[string]string
}

func RegisterImageRevision(ctx context.Context, db TxBeginner, image ImageRevision) (ImageRegistrationDecision, error) {
	metadataJSON, metadataDigest, err := canonicalStringMap(image.Metadata)
	if err != nil {
		return ImageRegistrationDecision{}, err
	}
	if err := validateImageRevision(image); err != nil {
		return ImageRegistrationDecision{}, err
	}
	revisionDigest, err := imageRevisionDigest(image, metadataJSON)
	if err != nil {
		return ImageRegistrationDecision{}, err
	}
	state, reason := "VERIFIED", "checksum_and_signature_policy_satisfied"
	if image.DeclaredChecksum != image.ObservedChecksum {
		state, reason = "REJECTED", "checksum_mismatch"
	} else if image.SignatureState == "FAILED" {
		state, reason = "REJECTED", "signature_verification_failed"
	}
	decision := ImageRegistrationDecision{State: state, Reason: reason, MetadataDigest: metadataDigest}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "image/"+image.ImageID); err != nil {
			return err
		}
		commandTag, err := tx.Exec(ctx, `
			INSERT INTO kim.image_revision_evidence (
				image_id, image_revision, owner_project_id, image_format, size_bytes,
				checksum_algorithm, declared_checksum, observed_checksum,
				signature_state, signature_digest, source_uri, visibility,
				validation_state, validation_reason, metadata, metadata_digest, revision_digest
			) VALUES ($1,$2,$3,$4,$5,'SHA256',$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT DO NOTHING
		`, image.ImageID, image.Revision, image.OwnerProjectID, image.Format, image.SizeBytes,
			image.DeclaredChecksum, image.ObservedChecksum, image.SignatureState, image.SignatureDigest,
			image.SourceURI, image.Visibility, state, reason, metadataJSON, metadataDigest, revisionDigest)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() == 0 {
			var existingDigest, existingState string
			if err := tx.QueryRow(ctx, `SELECT revision_digest, validation_state FROM kim.image_revision_evidence WHERE image_id=$1 AND image_revision=$2`, image.ImageID, image.Revision).Scan(&existingDigest, &existingState); err != nil {
				return err
			}
			if existingDigest != revisionDigest || existingState != state {
				return ErrCatalogConflict
			}
		}
		if state != "VERIFIED" {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.images_current (image_id, image_revision, owner_project_id, lifecycle_state, authority_generation)
			VALUES ($1,$2,$3,'ACTIVE',1)
			ON CONFLICT (image_id) DO UPDATE SET
				image_revision=EXCLUDED.image_revision,
				owner_project_id=EXCLUDED.owner_project_id,
				lifecycle_state='ACTIVE',
				authority_generation=kim.images_current.authority_generation+1,
				updated_at=statement_timestamp()
			WHERE kim.images_current.image_revision < EXCLUDED.image_revision
		`, image.ImageID, image.Revision, image.OwnerProjectID)
		return err
	})
	if err != nil {
		return ImageRegistrationDecision{}, fmt.Errorf("register image revision: %w", err)
	}
	if state == "REJECTED" {
		return decision, ErrImageIntegrity
	}
	return decision, nil
}

func RegisterFlavorRevision(ctx context.Context, db TxBeginner, flavor FlavorRevision) (PlacementShape, error) {
	shape, shapeJSON, err := normalizeFlavor(flavor)
	if err != nil {
		return PlacementShape{}, err
	}
	revisionDigest, err := flavorRevisionDigest(flavor, shape.ShapeDigest)
	if err != nil {
		return PlacementShape{}, err
	}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "flavor/"+flavor.FlavorID); err != nil {
			return err
		}
		commandTag, err := tx.Exec(ctx, `
			INSERT INTO kim.flavor_revision_evidence (
				flavor_id, flavor_revision, owner_project_id, name, vcpus, memory_mib,
				root_disk_gib, numa_policy, numa_nodes, hugepage_size_kib,
				cpu_allocation, cpu_pinning, extra_specs, shape_digest, revision_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT DO NOTHING
		`, flavor.FlavorID, flavor.Revision, flavor.OwnerProjectID, flavor.Name, flavor.VCPUs,
			flavor.MemoryMiB, flavor.RootDiskGiB, flavor.NUMAPolicy, flavor.NUMANodes,
			flavor.HugePageSizeKiB, flavor.CPUAllocation, flavor.CPUPinning, shapeJSON, shape.ShapeDigest, revisionDigest)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() == 0 {
			var existingDigest string
			if err := tx.QueryRow(ctx, `SELECT revision_digest FROM kim.flavor_revision_evidence WHERE flavor_id=$1 AND flavor_revision=$2`, flavor.FlavorID, flavor.Revision).Scan(&existingDigest); err != nil {
				return err
			}
			if existingDigest != revisionDigest {
				return ErrCatalogConflict
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.flavors_current (flavor_id, flavor_revision, owner_project_id, lifecycle_state, authority_generation)
			VALUES ($1,$2,$3,'ACTIVE',1)
			ON CONFLICT (flavor_id) DO UPDATE SET
				flavor_revision=EXCLUDED.flavor_revision,
				owner_project_id=EXCLUDED.owner_project_id,
				lifecycle_state='ACTIVE',
				authority_generation=kim.flavors_current.authority_generation+1,
				updated_at=statement_timestamp()
			WHERE kim.flavors_current.flavor_revision < EXCLUDED.flavor_revision
		`, flavor.FlavorID, flavor.Revision, flavor.OwnerProjectID)
		return err
	})
	if err != nil {
		return PlacementShape{}, fmt.Errorf("register flavor revision: %w", err)
	}
	return shape, nil
}

func LoadCurrentPlacementShape(ctx context.Context, db QueryRower, flavorID string) (PlacementShape, error) {
	var shape PlacementShape
	var extraJSON []byte
	err := db.QueryRow(ctx, `
		SELECT f.flavor_id, f.flavor_revision, f.vcpus, f.memory_mib, f.root_disk_gib,
		       f.numa_policy, f.numa_nodes, f.hugepage_size_kib, f.cpu_allocation,
		       f.cpu_pinning, f.extra_specs, f.shape_digest
		FROM kim.flavors_current c
		JOIN kim.flavor_revision_evidence f
		  ON f.flavor_id=c.flavor_id AND f.flavor_revision=c.flavor_revision
		WHERE c.flavor_id=$1 AND c.lifecycle_state='ACTIVE'
	`, flavorID).Scan(&shape.FlavorID, &shape.FlavorRevision, &shape.VCPUs, &shape.MemoryMiB,
		&shape.RootDiskGiB, &shape.NUMAPolicy, &shape.NUMANodes, &shape.HugePageSizeKiB,
		&shape.CPUAllocation, &shape.CPUPinning, &extraJSON, &shape.ShapeDigest)
	if err != nil {
		return PlacementShape{}, err
	}
	if err := json.Unmarshal(extraJSON, &shape.ExtraSpecs); err != nil {
		return PlacementShape{}, fmt.Errorf("decode placement extra specs: %w", err)
	}
	return shape, nil
}

func requireActiveDatabaseAuthority(ctx context.Context, tx pgx.Tx) error {
	var mode string
	if err := tx.QueryRow(ctx, `SELECT mode FROM kim.database_authority WHERE singleton=true FOR SHARE`).Scan(&mode); err != nil {
		return fmt.Errorf("read database authority: %w", err)
	}
	if mode != "ACTIVE" {
		return ErrCatalogAuthorityMode
	}
	return nil
}

func validateImageRevision(image ImageRevision) error {
	if image.ImageID == "" || image.OwnerProjectID == "" || image.Revision == 0 || image.SizeBytes == 0 || image.SourceURI == "" {
		return fmt.Errorf("%w: required image field missing", ErrImageIntegrity)
	}
	if image.Format != "QCOW2" && image.Format != "RAW" {
		return fmt.Errorf("%w: unsupported image format", ErrImageIntegrity)
	}
	if image.Visibility != "PRIVATE" && image.Visibility != "SHARED" && image.Visibility != "PUBLIC" {
		return fmt.Errorf("%w: invalid visibility", ErrImageIntegrity)
	}
	if !isSHA256(image.DeclaredChecksum) || !isSHA256(image.ObservedChecksum) {
		return fmt.Errorf("%w: invalid SHA-256 checksum", ErrImageIntegrity)
	}
	if image.SignatureState != "VERIFIED" && image.SignatureState != "UNVERIFIED" && image.SignatureState != "FAILED" {
		return fmt.Errorf("%w: invalid signature state", ErrImageIntegrity)
	}
	if image.SignatureDigest != "" && !isSHA256(image.SignatureDigest) {
		return fmt.Errorf("%w: invalid signature digest", ErrImageIntegrity)
	}
	return nil
}

func imageRevisionDigest(image ImageRevision, metadataJSON []byte) (string, error) {
	canonical := struct {
		ImageID, OwnerProjectID, Format, SourceURI, Visibility string
		Revision, SizeBytes                                    uint64
		DeclaredChecksum, ObservedChecksum                     string
		SignatureState, SignatureDigest                        string
		Metadata                                               json.RawMessage
	}{image.ImageID, image.OwnerProjectID, image.Format, image.SourceURI, image.Visibility,
		image.Revision, image.SizeBytes, image.DeclaredChecksum, image.ObservedChecksum,
		image.SignatureState, image.SignatureDigest, metadataJSON}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeFlavor(flavor FlavorRevision) (PlacementShape, []byte, error) {
	if flavor.FlavorID == "" || flavor.OwnerProjectID == "" || flavor.Name == "" || flavor.Revision == 0 || flavor.VCPUs == 0 || flavor.MemoryMiB == 0 || flavor.RootDiskGiB == 0 {
		return PlacementShape{}, nil, ErrFlavorShapeInvalid
	}
	if flavor.NUMAPolicy != "NONE" && flavor.NUMAPolicy != "REQUIRED" || flavor.NUMAPolicy == "REQUIRED" && flavor.NUMANodes == nil || flavor.NUMAPolicy == "NONE" && flavor.NUMANodes != nil {
		return PlacementShape{}, nil, ErrFlavorShapeInvalid
	}
	if flavor.CPUAllocation != "SHARED" && flavor.CPUAllocation != "DEDICATED" || flavor.CPUPinning && flavor.CPUAllocation != "DEDICATED" {
		return PlacementShape{}, nil, ErrFlavorShapeInvalid
	}
	extraJSON, _, err := canonicalStringMap(flavor.ExtraSpecs)
	if err != nil {
		return PlacementShape{}, nil, err
	}
	canonical := struct {
		FlavorID, NUMAPolicy, CPUAllocation           string
		FlavorRevision, VCPUs, MemoryMiB, RootDiskGiB uint64
		NUMANodes                                     *uint32
		HugePageSizeKiB                               *uint64
		CPUPinning                                    bool
		ExtraSpecs                                    json.RawMessage
	}{flavor.FlavorID, flavor.NUMAPolicy, flavor.CPUAllocation, flavor.Revision, flavor.VCPUs, flavor.MemoryMiB, flavor.RootDiskGiB, flavor.NUMANodes, flavor.HugePageSizeKiB, flavor.CPUPinning, extraJSON}
	body, err := json.Marshal(canonical)
	if err != nil {
		return PlacementShape{}, nil, err
	}
	digest := sha256.Sum256(body)
	return PlacementShape{FlavorID: flavor.FlavorID, FlavorRevision: flavor.Revision, ShapeDigest: hex.EncodeToString(digest[:]), VCPUs: flavor.VCPUs, MemoryMiB: flavor.MemoryMiB, RootDiskGiB: flavor.RootDiskGiB, NUMAPolicy: flavor.NUMAPolicy, NUMANodes: flavor.NUMANodes, HugePageSizeKiB: flavor.HugePageSizeKiB, CPUAllocation: flavor.CPUAllocation, CPUPinning: flavor.CPUPinning, ExtraSpecs: cloneStringMap(flavor.ExtraSpecs)}, extraJSON, nil
}

func flavorRevisionDigest(flavor FlavorRevision, shapeDigest string) (string, error) {
	canonical := struct {
		FlavorID, OwnerProjectID, Name, ShapeDigest string
		Revision                                    uint64
	}{flavor.FlavorID, flavor.OwnerProjectID, flavor.Name, shapeDigest, flavor.Revision}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func isSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
