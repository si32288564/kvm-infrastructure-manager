package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNormalizeFlavorPreservesPlacementRequirements(t *testing.T) {
	numaNodes := uint32(2)
	hugePageSize := uint64(1048576)
	shape, _, err := normalizeFlavor(FlavorRevision{
		FlavorID: "nfv.large", OwnerProjectID: "project-a", Name: "NFV Large", Revision: 7,
		VCPUs: 8, MemoryMiB: 16384, RootDiskGiB: 40,
		NUMAPolicy: "REQUIRED", NUMANodes: &numaNodes, HugePageSizeKiB: &hugePageSize,
		CPUAllocation: "DEDICATED", CPUPinning: true,
		ExtraSpecs: map[string]string{"trait": "sriov", "cpu_policy": "strict"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if shape.FlavorRevision != 7 || shape.VCPUs != 8 || shape.MemoryMiB != 16384 || shape.RootDiskGiB != 40 || shape.NUMANodes == nil || *shape.NUMANodes != 2 || shape.HugePageSizeKiB == nil || *shape.HugePageSizeKiB != 1048576 || !shape.CPUPinning || shape.CPUAllocation != "DEDICATED" || shape.ExtraSpecs["trait"] != "sriov" {
		t.Fatalf("placement shape lost a Flavor requirement: %#v", shape)
	}
	if len(shape.ShapeDigest) != 64 {
		t.Fatalf("shape digest length = %d", len(shape.ShapeDigest))
	}

	reordered, _, err := normalizeFlavor(FlavorRevision{
		FlavorID: "nfv.large", OwnerProjectID: "project-a", Name: "NFV Large", Revision: 7,
		VCPUs: 8, MemoryMiB: 16384, RootDiskGiB: 40,
		NUMAPolicy: "REQUIRED", NUMANodes: &numaNodes, HugePageSizeKiB: &hugePageSize,
		CPUAllocation: "DEDICATED", CPUPinning: true,
		ExtraSpecs: map[string]string{"cpu_policy": "strict", "trait": "sriov"},
	})
	if err != nil || reordered.ShapeDigest != shape.ShapeDigest {
		t.Fatalf("canonical Flavor digest mismatch: %s/%s, %v", shape.ShapeDigest, reordered.ShapeDigest, err)
	}
}

func TestCatalogPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('catalog-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	imageID := "image-" + suffix
	checksum := digestBytes([]byte("verified image bytes"))
	image := ImageRevision{
		ImageID: imageID, Revision: 1, OwnerProjectID: "project-a", Format: "QCOW2",
		SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum,
		SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")),
		SourceURI: "https://images.invalid/nfv.qcow2", Visibility: "PRIVATE",
		Metadata: map[string]string{"architecture": "x86_64", "machine_type": "pc-q35"},
	}
	decision, err := RegisterImageRevision(ctx, pool, image)
	if err != nil || decision.State != "VERIFIED" {
		t.Fatalf("verified image decision/error = %#v/%v", decision, err)
	}
	var currentRevision uint64
	if err := pool.QueryRow(ctx, `SELECT image_revision FROM kim.images_current WHERE image_id=$1 AND lifecycle_state='ACTIVE'`, imageID).Scan(&currentRevision); err != nil || currentRevision != 1 {
		t.Fatalf("current image revision/error = %d/%v", currentRevision, err)
	}

	bad := image
	bad.Revision = 2
	bad.ObservedChecksum = digestBytes([]byte("corrupt bytes"))
	decision, err = RegisterImageRevision(ctx, pool, bad)
	if !errors.Is(err, ErrImageIntegrity) || decision.State != "REJECTED" {
		t.Fatalf("checksum mismatch decision/error = %#v/%v", decision, err)
	}
	if err := pool.QueryRow(ctx, `SELECT image_revision FROM kim.images_current WHERE image_id=$1`, imageID).Scan(&currentRevision); err != nil || currentRevision != 1 {
		t.Fatalf("rejected revision changed current authority = %d/%v", currentRevision, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.image_revision_evidence SET validation_reason='rewritten' WHERE image_id=$1 AND image_revision=1`, imageID); err == nil {
		t.Fatal("immutable image evidence accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.images_current SET image_revision=2 WHERE image_id=$1`, imageID); err == nil {
		t.Fatal("rejected Image revision became current through direct database mutation")
	}

	numaNodes := uint32(2)
	hugePageSize := uint64(1048576)
	flavorID := "flavor-" + suffix
	flavor := FlavorRevision{
		FlavorID: flavorID, Revision: 1, OwnerProjectID: "project-a", Name: "nfv.large",
		VCPUs: 8, MemoryMiB: 16384, RootDiskGiB: 40,
		NUMAPolicy: "REQUIRED", NUMANodes: &numaNodes, HugePageSizeKiB: &hugePageSize,
		CPUAllocation: "DEDICATED", CPUPinning: true,
		ExtraSpecs: map[string]string{"trait": "sriov", "emulator_threads": "isolated"},
	}
	written, err := RegisterFlavorRevision(ctx, pool, flavor)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCurrentPlacementShape(ctx, pool, flavorID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ShapeDigest != written.ShapeDigest || loaded.VCPUs != flavor.VCPUs || loaded.MemoryMiB != flavor.MemoryMiB || loaded.RootDiskGiB != flavor.RootDiskGiB || loaded.NUMANodes == nil || *loaded.NUMANodes != numaNodes || loaded.HugePageSizeKiB == nil || *loaded.HugePageSizeKiB != hugePageSize || loaded.CPUAllocation != flavor.CPUAllocation || loaded.CPUPinning != flavor.CPUPinning || loaded.ExtraSpecs["trait"] != "sriov" {
		t.Fatalf("loaded Placement shape lost fields: written=%#v loaded=%#v", written, loaded)
	}

	conflict := flavor
	conflict.VCPUs = 16
	if _, err := RegisterFlavorRevision(ctx, pool, conflict); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("same Flavor revision with different shape was not rejected: %v", err)
	}
	ownerConflict := flavor
	ownerConflict.OwnerProjectID = "project-b"
	if _, err := RegisterFlavorRevision(ctx, pool, ownerConflict); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("same Flavor revision with different owner was not rejected: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE kim.database_authority SET mode='RECOVERY_READ_ONLY' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	flavor.Revision = 2
	if _, err := RegisterFlavorRevision(ctx, pool, flavor); !errors.Is(err, ErrCatalogAuthorityMode) {
		t.Fatalf("catalog mutation in recovery-read-only mode was not blocked: %v", err)
	}
}
