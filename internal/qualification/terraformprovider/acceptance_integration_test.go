package terraformprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/availabilitypolicy"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/flavor"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	imageapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/image"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type bearerAuthenticator struct{ principal resource.Principal }

func (a bearerAuthenticator) Authenticate(request *http.Request) (resource.Principal, error) {
	if request.Header.Get("Authorization") != "Bearer terraform-acceptance-token" {
		return resource.Principal{}, errors.New("invalid acceptance token")
	}
	return a.principal, nil
}

func TestTerraformPhaseOneRealHTTPPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	terraformCLI := os.Getenv("KIM_TERRAFORM_CLI")
	if databaseURL == "" || terraformCLI == "" {
		t.Skip("KIM_POSTGRES_TEST_URL and KIM_TERRAFORM_CLI are required")
	}
	if out, err := exec.Command(terraformCLI, "version").CombinedOutput(); err != nil || !bytes.Contains(out, []byte("Terraform v1.14.9")) {
		t.Fatalf("Terraform CLI qualification requires v1.14.9: %s err=%v", out, err)
	}
	ctx := context.Background()
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('terraform-provider-phase1',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	principal := resource.Principal{Issuer: "terraform-acceptance", Subject: "automation-" + suffix, Type: "AUTOMATION"}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','','ADMIN','ACTIVE',1)`, "terraform-system-admin-"+suffix, principal.Issuer, principal.Subject, principal.Type); err != nil {
		t.Fatal(err)
	}
	projectStore := postgres.NorthboundProjectStore{DB: pool}
	flavorStore := postgres.NorthboundFlavorStore{DB: pool}
	availabilityStore := postgres.NorthboundAvailabilityPolicyStore{DB: pool}
	imageStore := postgres.NorthboundImageStore{DB: pool}
	server := httptest.NewServer(httpapi.Server{
		Projects:             project.Service{Store: projectStore},
		Flavors:              flavor.Service{Store: flavorStore},
		AvailabilityPolicies: availabilitypolicy.Service{Store: availabilityStore},
		Images:               imageapi.Service{Store: imageStore},
		Authenticator:        bearerAuthenticator{principal: principal},
		RequestTimeout:       10 * time.Second,
	}.Handler())
	defer server.Close()

	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	mirror := filepath.Join(work, "mirror", "registry.terraform.io", "kvm-infrastructure-manager", "kim", "0.1.0", "darwin_arm64")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	providerBinary := filepath.Join(mirror, "terraform-provider-kim_v0.1.0")
	build := exec.Command("go", "build", "-o", providerBinary, ".")
	build.Dir = filepath.Join(repositoryRoot, "terraform-provider-kim")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %s: %v", out, err)
	}
	cliConfig := filepath.Join(work, "terraform.rc")
	if err := os.WriteFile(cliConfig, []byte(fmt.Sprintf(`provider_installation {
  filesystem_mirror {
    path    = %q
    include = ["registry.terraform.io/kvm-infrastructure-manager/kim"]
  }
  direct {
    exclude = ["registry.terraform.io/kvm-infrastructure-manager/kim"]
  }
}
`, filepath.Join(work, "mirror"))), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := fmt.Sprintf(`terraform {
  required_providers {
    kim = {
      source  = "kvm-infrastructure-manager/kim"
      version = "0.1.0"
    }
  }
}

variable "project_name" {
  type    = string
  default = "terraform-project-%s"
}

provider "kim" {
  endpoint                        = %q
  token                           = "terraform-acceptance-token"
  request_timeout_seconds         = 10
  operation_poll_interval_seconds = 1
}

resource "kim_project" "phase1" {
  name = var.project_name
}

resource "kim_flavor" "phase1" {
  project_id       = kim_project.phase1.id
  name             = "small-%s"
  vcpus            = 2
  memory_mib       = 2048
  root_disk_gib    = 20
  numa_policy      = "NONE"
  cpu_allocation   = "SHARED"
  cpu_pinning      = false
}

resource "kim_availability_policy" "phase1" {
  name              = "managed-%s"
  availability_mode = "WORKLOAD_MANAGED"
  max_attempts       = 3
}

resource "kim_image" "phase1" {
  project_id       = kim_project.phase1.id
  name             = "base-%s.raw"
  architecture     = "X86_64"
  format           = "RAW"
  expected_digest  = "35d67098c3ad6220670c6128826e0701bbdde4cc1349c0c55bef482df8869c93"
  source_id        = "terraform.fixture"
  visibility       = "PRIVATE"
}
`, suffix, server.URL+"/api/v1", suffix, suffix, suffix)
	configPath := filepath.Join(work, "main.tf")
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfig, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1")
	runTerraform(t, terraformCLI, work, environment, true, "init", "-input=false")

	watchContext, cancelWatch := context.WithTimeout(ctx, 45*time.Second)
	defer cancelWatch()
	watchResult := make(chan error, 1)
	go func() { watchResult <- verifyNextImageOperation(watchContext, pool, suffix) }()
	runTerraform(t, terraformCLI, work, environment, true, "apply", "-input=false", "-auto-approve")
	if err := <-watchResult; err != nil {
		t.Fatal(err)
	}
	noOp := runTerraform(t, terraformCLI, work, environment, true, "plan", "-input=false", "-detailed-exitcode")
	if !strings.Contains(noOp, "No changes") {
		t.Fatalf("second plan was not no-op:\n%s", noOp)
	}
	firstState := terraformState(t, terraformCLI, work, environment)
	firstIDs := phaseOneIDs(t, firstState)
	secondDigest := digest([]byte("terraform-image-second-content-revision"))
	configuration = strings.Replace(configuration, "vcpus            = 2", "vcpus            = 4", 1)
	configuration = strings.Replace(configuration, "max_attempts       = 3", "max_attempts       = 4", 1)
	configuration = strings.Replace(configuration, "base-"+suffix+".raw", "base-updated-"+suffix+".raw", 1)
	configuration = strings.Replace(configuration, "35d67098c3ad6220670c6128826e0701bbdde4cc1349c0c55bef482df8869c93", secondDigest, 1)
	configuration = strings.Replace(configuration, "terraform.fixture", "terraform.fixture.revision2", 1)
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	updateWatchContext, cancelUpdateWatch := context.WithTimeout(ctx, 45*time.Second)
	defer cancelUpdateWatch()
	updateWatchResult := make(chan error, 1)
	go func() { updateWatchResult <- verifyNextImageOperation(updateWatchContext, pool, suffix) }()
	runTerraform(t, terraformCLI, work, environment, true, "apply", "-input=false", "-auto-approve")
	if err := <-updateWatchResult; err != nil {
		t.Fatal(err)
	}
	updatedState := terraformState(t, terraformCLI, work, environment)
	updatedIDs := phaseOneIDs(t, updatedState)
	for address, id := range firstIDs {
		if updatedIDs[address] != id {
			t.Fatalf("logical ID changed on revision update %s: %s -> %s", address, id, updatedIDs[address])
		}
	}

	configuration = strings.Replace(configuration, `availability_mode = "WORKLOAD_MANAGED"`, `availability_mode = "UNSUPPORTED"`, 1)
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	unsupported := runTerraformExit(t, terraformCLI, work, environment, 1, "plan", "-input=false")
	if !strings.Contains(unsupported, "WORKLOAD_MANAGED") || !strings.Contains(unsupported, "MANUAL") {
		t.Fatalf("unsupported availability mode was not closed by provider schema:\n%s", unsupported)
	}
	configuration = strings.Replace(configuration, `availability_mode = "UNSUPPORTED"`, `availability_mode = "WORKLOAD_MANAGED"`, 1)
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}

	state := terraformState(t, terraformCLI, work, environment)
	ids := phaseOneIDs(t, state)
	assertNoPhysicalState(t, state)
	remotePatch(t, server.Client(), server.URL+"/api/v1/projects/"+ids["kim_project.phase1"], `{"name":"remote-drift"}`, 1)
	drift := runTerraformExit(t, terraformCLI, work, environment, 2, "plan", "-input=false", "-detailed-exitcode")
	if !strings.Contains(drift, "remote-drift") || !strings.Contains(drift, "terraform-project-") {
		t.Fatalf("remote desired drift not reported:\n%s", drift)
	}
	runTerraform(t, terraformCLI, work, environment, true, "apply", "-input=false", "-auto-approve")
	remotePatch(t, server.Client(), server.URL+"/api/v1/flavors/"+ids["kim_flavor.phase1"], `{"vcpus":5}`, 2)
	configuration = strings.Replace(configuration, "vcpus            = 4", "vcpus            = 6", 1)
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := runTerraformExit(t, terraformCLI, work, environment, 1, "apply", "-refresh=false", "-input=false", "-auto-approve")
	if !strings.Contains(stale, "STALE_REVISION") {
		t.Fatalf("stale If-Match did not fail closed:\n%s", stale)
	}
	runTerraform(t, terraformCLI, work, environment, true, "apply", "-input=false", "-auto-approve")

	for _, address := range []string{"kim_image.phase1", "kim_flavor.phase1", "kim_availability_policy.phase1", "kim_project.phase1"} {
		runTerraform(t, terraformCLI, work, environment, true, "state", "rm", address)
	}
	for _, item := range []struct{ address, prefix string }{{"kim_project.phase1", "project"}, {"kim_flavor.phase1", "flavor"}, {"kim_availability_policy.phase1", "availability-policy"}, {"kim_image.phase1", "image"}} {
		runTerraform(t, terraformCLI, work, environment, true, "import", "-input=false", item.address, item.prefix+"/"+ids[item.address])
	}
	importPlan := runTerraform(t, terraformCLI, work, environment, true, "plan", "-input=false", "-detailed-exitcode")
	if !strings.Contains(importPlan, "No changes") {
		t.Fatalf("import plan was not no-op:\n%s", importPlan)
	}
	runTerraform(t, terraformCLI, work, environment, true, "destroy", "-input=false", "-auto-approve")
}

func verifyNextImageOperation(ctx context.Context, pool *pgxpool.Pool, suffix string) error {
	var operationID, imageID, expectedDigest string
	var revision uint64
	for {
		err := pool.QueryRow(ctx, `SELECT o.operation_id,o.image_id,o.image_revision,o.expected_digest FROM kim.image_ingestion_operation_evidence o JOIN kim.image_ingestion_operations_current c USING(operation_id) WHERE c.phase='PENDING' AND o.principal_subject=$1 ORDER BY o.accepted_at DESC LIMIT 1`, "automation-"+suffix).Scan(&operationID, &imageID, &revision, &expectedDigest)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	incarnation := fmt.Sprintf("%s-%d", suffix, revision)
	hostID := "terraform-image-host-" + incarnation
	if err := postgres.RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		return err
	}
	jobID, commandID := "terraform-image-job-"+incarnation, "terraform-image-command-"+incarnation
	commandVerification := "terraform-image-command-verification-" + incarnation
	evidenceJSON, _ := json.Marshal(map[string]any{"observed_digest": expectedDigest, "observed_size_bytes": 64, "read_back_state": "COMPLETE"})
	verifierDigest := digest([]byte("terraform-independent-verifier"))
	_, err := pool.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'IMAGE_INGESTION_OPERATION',$2,$3,'VERIFYING');INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($4,$1,$5,'IMAGE_ARTIFACT_INGEST','kim.command.image-artifact-ingest/v1',$6,'{}',$7);INSERT INTO kim.execution_commands_current(command_id,command_state,current_attempt_index) VALUES($4,'UNKNOWN',1);INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($4,1,1,$5,1,1,$8,statement_timestamp()-interval '2 minutes',statement_timestamp()-interval '1 minute');INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($4,1,1,1,1);INSERT INTO kim.image_ingestion_command_evidence(operation_id,job_id,command_id,host_id,host_authority_generation,command_payload_digest) VALUES($2,$1,$4,$5,1,$7);INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($9,$4,1,1,$10,'MATCHED',$11,$12::jsonb)`, pgx.QueryExecModeSimpleProtocol, jobID, operationID, revision, commandID, hostID, "image:"+imageID+":"+fmt.Sprint(revision), digest([]byte("payload")), digest([]byte("token")), commandVerification, digest([]byte("observation")), verifierDigest, string(evidenceJSON))
	if err != nil {
		return err
	}
	observationID := "terraform-image-observation-" + incarnation
	if err := postgres.RecordImageIngestionObservation(ctx, pool, postgres.ImageIngestionObservation{OperationID: operationID, ObservationID: observationID, VerificationID: commandVerification}); err != nil {
		return err
	}
	terminal, err := postgres.FinalizeImageIngestion(ctx, pool, operationID, observationID, "terraform-image-verification-"+incarnation, "terraform-image-terminal-"+incarnation)
	if err != nil {
		return err
	}
	if terminal.Phase != "SUCCEEDED" {
		return fmt.Errorf("image operation phase=%s", terminal.Phase)
	}
	return nil
}

func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func runTerraform(t *testing.T, binary, directory string, environment []string, success bool, arguments ...string) string {
	t.Helper()
	return runTerraformExit(t, binary, directory, environment, map[bool]int{true: 0, false: 1}[success], arguments...)
}

func runTerraformExit(t *testing.T, binary, directory string, environment []string, expected int, arguments ...string) string {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir, command.Env = directory, environment
	out, err := command.CombinedOutput()
	exit := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("terraform %v: %s: %v", arguments, out, err)
		}
		exit = exitError.ExitCode()
	}
	if exit != expected {
		t.Fatalf("terraform %v exit=%d want=%d:\n%s", arguments, exit, expected, out)
	}
	return string(out)
}

func terraformState(t *testing.T, binary, directory string, environment []string) map[string]any {
	t.Helper()
	raw := runTerraform(t, binary, directory, environment, true, "show", "-json")
	var state map[string]any
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func phaseOneIDs(t *testing.T, state map[string]any) map[string]string {
	t.Helper()
	ids := map[string]string{}
	resources := state["values"].(map[string]any)["root_module"].(map[string]any)["resources"].([]any)
	for _, raw := range resources {
		item := raw.(map[string]any)
		values := item["values"].(map[string]any)
		if id, ok := values["id"].(string); ok {
			ids[item["address"].(string)] = id
		}
	}
	if len(ids) != 4 {
		t.Fatalf("resource IDs=%v", ids)
	}
	return ids
}

func assertNoPhysicalState(t *testing.T, state map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(state)
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"host_id", "pcpu", "pmd", "rxq", "vhost_user", "pci_bdf", "lv_uuid", "agent_identity", "recovery_generation", "evacuate_generation", "ingestion_attempt"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("physical/internal state leaked: %s", forbidden)
		}
	}
}

func remotePatch(t *testing.T, client *http.Client, endpoint, body string, revision int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPatch, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer terraform-acceptance-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", fmt.Sprintf(`"%d"`, revision))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remote patch status=%d", response.StatusCode)
	}
}
