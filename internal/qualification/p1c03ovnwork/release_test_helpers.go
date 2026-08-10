package p1c03ovnwork

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

const testReleaseID = "kim-p1c03-current"

func publishCurrentTestWorkerRelease(t *testing.T, ctx context.Context, pool *pgxpool.Pool, artifactDigest string) {
	t.Helper()
	if err := postgres.PublishReleaseManifest(ctx, pool, postgres.ReleaseManifest{
		ReleaseID: testReleaseID, ManifestRevision: 1, ProductVersion: "p1-c03", Channel: "DEVELOPER_PREVIEW",
		ManifestDigest: digest("p1c03-release-manifest"), CertificationState: "VALIDATED",
		OVNWorkerArtifactDigest: artifactDigest, OVNWorkerWorkSchemas: []string{postgres.OVNRuntimeWorkSchemaV1},
		OVNWorkerComponentContractDigest: digest("p1c03-ovn-worker-contract"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := postgres.SetReleaseAuthority(ctx, pool, testReleaseID, 1, postgres.OVNRuntimeWorkSchemaV1, "ACTIVE"); err != nil {
		t.Fatal(err)
	}
}

func testWorkerReleaseArguments() []string {
	return []string{
		"-release-id", testReleaseID,
		"-release-manifest-revision", "1",
		"-work-schema-versions", postgres.OVNRuntimeWorkSchemaV1,
		"-compatibility-evaluator-artifact-digest", digest("p1c03-compatibility-evaluator"),
	}
}
