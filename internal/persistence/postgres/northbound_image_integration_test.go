package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	imageapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/image"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

func TestNorthboundImageArtifactAuthorityPostgreSQL(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, url, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('northbound-image',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	p := resource.Principal{Issuer: "image-issuer", Subject: "admin-" + suffix, Type: "AUTOMATION"}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','','ADMIN','ACTIVE',1)`, "image-admin-"+suffix, p.Issuer, p.Subject, p.Type); err != nil {
		t.Fatal(err)
	}
	projectStore := NorthboundProjectStore{DB: pool}
	projectService := project.Service{Store: projectStore}
	projectResource, _, err := projectService.Create(ctx, p, project.CreateRequest{Name: "image-project-" + suffix, IdempotencyKey: "project-" + suffix, RequestID: "request-project-" + suffix, CanonicalPath: "/api/v1/projects"})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("base-image\x00mutable-guest-marker\x00end-marker")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	store := NorthboundImageStore{DB: pool}
	service := imageapi.Service{Store: store}
	desired := imageapi.Desired{ProjectID: projectResource.ID, Name: "qualified.raw", Architecture: "X86_64", Format: "RAW", ExpectedDigest: digest, SourceID: "approved.fixture", Visibility: "PRIVATE"}
	created, replay, err := service.Create(ctx, p, imageapi.CreateRequest{Desired: desired, IdempotencyKey: "image-" + suffix, RequestID: "create-" + suffix, CanonicalPath: "/api/v1/images"})
	if err != nil || replay || created.VerificationState != "PENDING" || created.VerifiedDigest != nil {
		t.Fatalf("create=%+v replay=%v err=%v", created, replay, err)
	}
	replayed, replay, err := service.Create(ctx, p, imageapi.CreateRequest{Desired: desired, IdempotencyKey: "image-" + suffix, RequestID: "create-replay-" + suffix, CanonicalPath: "/api/v1/images"})
	if err != nil || !replay || replayed.ID != created.ID {
		t.Fatalf("replay=%+v/%v/%v", replayed, replay, err)
	}
	op, replay, err := service.Ingest(ctx, p, created.ID, 1, imageapi.IngestionRequest{IdempotencyKey: "ingest-" + suffix, RequestID: "ingest-request-" + suffix})
	if err != nil || replay || op.Phase != "PENDING" {
		t.Fatalf("operation=%+v replay=%v err=%v", op, replay, err)
	}
	verifier := sha256.Sum256([]byte("independent-verifier"))
	hostID := "image-ingestion-host-" + suffix
	if err := RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		t.Fatal(err)
	}
	jobID, commandID, commandVerification := "image-job-"+suffix, "image-command-"+suffix, "command-verification-"+suffix
	evidenceJSON, _ := json.Marshal(map[string]any{"observed_digest": digest, "observed_size_bytes": len(content), "read_back_state": "COMPLETE"})
	if _, err := pool.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'IMAGE_INGESTION_OPERATION',$2,1,'VERIFYING');INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($3,$1,$4,'IMAGE_ARTIFACT_INGEST','kim.command.image-artifact-ingest/v1',$5,'{}',$6);INSERT INTO kim.execution_commands_current(command_id,command_state,current_attempt_index) VALUES($3,'UNKNOWN',1);INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($3,1,1,$4,1,1,$7,statement_timestamp()-interval '2 minutes',statement_timestamp()-interval '1 minute');INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($3,1,1,1,1);INSERT INTO kim.image_ingestion_command_evidence(operation_id,job_id,command_id,host_id,host_authority_generation,command_payload_digest) VALUES($2,$1,$3,$4,1,$6);INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($8,$3,1,1,$9,'MATCHED',$10,$11::jsonb)`, pgx.QueryExecModeSimpleProtocol, jobID, op.ID, commandID, hostID, "image:"+created.ID+":1", digestBytes([]byte("payload")), digestBytes([]byte("token")), commandVerification, digestBytes([]byte("observation")), hex.EncodeToString(verifier[:]), string(evidenceJSON)); err != nil {
		t.Fatal(err)
	}
	observation := ImageIngestionObservation{OperationID: op.ID, ObservationID: "observation-" + suffix, VerificationID: commandVerification}
	if err := RecordImageIngestionObservation(ctx, pool, observation); err != nil {
		t.Fatal(err)
	}
	terminal, err := FinalizeImageIngestion(ctx, pool, op.ID, observation.ObservationID, "verification-"+suffix, "terminal-"+suffix)
	if err != nil || terminal.Phase != "SUCCEEDED" {
		t.Fatalf("terminal=%+v err=%v", terminal, err)
	}
	verified, err := service.Get(ctx, p, created.ID, "read-"+suffix)
	if err != nil || verified.VerificationState != "VERIFIED" || verified.VerifiedDigest == nil || *verified.VerifiedDigest != digest {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
	var publishedDigest, state string
	if err := pool.QueryRow(ctx, `SELECT observed_checksum,validation_state FROM kim.image_revision_evidence WHERE image_id=$1 AND image_revision=1`, created.ID).Scan(&publishedDigest, &state); err != nil || publishedDigest != digest || state != "VERIFIED" {
		t.Fatalf("publication=%s/%s err=%v", publishedDigest, state, err)
	}
	metadataName := "qualified-renamed.raw"
	metadataRevision, err := service.Patch(ctx, p, created.ID, 1, imageapi.Patch{Name: &metadataName}, "metadata-patch-"+suffix)
	if err != nil || metadataRevision.ID != created.ID || metadataRevision.Revision != 2 || metadataRevision.VerificationState != "VERIFIED" || metadataRevision.VerifiedDigest == nil || *metadataRevision.VerifiedDigest != digest {
		t.Fatalf("metadata revision=%+v err=%v", metadataRevision, err)
	}
	newContent := sha256.Sum256([]byte("new immutable artifact revision"))
	newDigest := hex.EncodeToString(newContent[:])
	newSource := "approved.fixture.revision2"
	contentRevision, err := service.Patch(ctx, p, created.ID, 2, imageapi.Patch{ExpectedDigest: &newDigest, SourceID: &newSource}, "content-patch-"+suffix)
	if err != nil || contentRevision.ID != created.ID || contentRevision.Revision != 3 || contentRevision.VerificationState != "PENDING" || contentRevision.VerifiedDigest != nil || contentRevision.VerifiedSizeBytes != nil {
		t.Fatalf("content revision=%+v err=%v", contentRevision, err)
	}
	var currentIngestion *string
	if err := pool.QueryRow(ctx, `SELECT current_ingestion_operation_id FROM kim.northbound_images_current WHERE image_id=$1`, created.ID).Scan(&currentIngestion); err != nil || currentIngestion != nil {
		t.Fatalf("content revision retained stale ingestion operation=%v err=%v", currentIngestion, err)
	}
	var publishedRevision uint64
	if err := pool.QueryRow(ctx, `SELECT image_revision FROM kim.images_current WHERE image_id=$1`, created.ID).Scan(&publishedRevision); err != nil || publishedRevision != 2 {
		t.Fatalf("unverified content revision retrofitted publication revision=%d err=%v", publishedRevision, err)
	}
	if _, err := service.Patch(ctx, p, created.ID, 2, imageapi.Patch{Name: &metadataName}, "stale-patch-"+suffix); !errors.Is(err, resource.ErrStaleRevision) {
		t.Fatalf("stale content revision patch err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.image_artifact_observation_evidence SET observed_digest=$2 WHERE observation_id=$1`, observation.ObservationID, strings64("0")); err == nil {
		t.Fatal("immutable observation updated")
	}
	server := httptest.NewServer(httpapi.Server{Projects: projectService, Images: service, Authenticator: integrationPrincipalAuthenticator{subjects: map[string]project.Principal{"admin": p}}}.Handler())
	defer server.Close()
	httpBody := fmt.Sprintf(`{"projectId":%q,"name":"http.raw","architecture":"X86_64","format":"RAW","expectedDigest":%q,"sourceId":"approved.fixture","visibility":"PRIVATE"}`, projectResource.ID, digest)
	response, err := integrationRequest(server.Client(), "POST", server.URL+"/api/v1/images", httpBody, map[string]string{"X-Test-Principal": "admin", "Idempotency-Key": "http-image-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	var httpImage imageapi.Resource
	if err := json.NewDecoder(response.Body).Decode(&httpImage); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 201 || httpImage.VerifiedDigest != nil || httpImage.VerificationState != "PENDING" {
		t.Fatalf("HTTP Image create status=%d resource=%+v", response.StatusCode, httpImage)
	}
	response, err = integrationRequest(server.Client(), "POST", server.URL+"/api/v1/images/"+httpImage.ID+"/ingestions", `{}`, map[string]string{"X-Test-Principal": "admin", "Idempotency-Key": "http-ingest-" + suffix, "If-Match": `"1"`})
	if err != nil {
		t.Fatal(err)
	}
	var httpOperation imageapi.Operation
	if err := json.NewDecoder(response.Body).Decode(&httpOperation); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 202 || httpOperation.Phase != "PENDING" || response.Header.Get("Location") == "" {
		t.Fatalf("HTTP ingestion status=%d operation=%+v", response.StatusCode, httpOperation)
	}
	response, err = integrationRequest(server.Client(), "GET", server.URL+"/api/v1/operations/"+httpOperation.ID, "", map[string]string{"X-Test-Principal": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("HTTP operation GET=%d", response.StatusCode)
	}
}

func strings64(v string) string {
	out := ""
	for len(out) < 64 {
		out += v
	}
	return out
}
