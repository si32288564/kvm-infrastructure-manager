package postgres

import (
	"bytes"
	"context"
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
	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/phase2"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

type phase2BearerAuthenticator struct{ principal resource.Principal }

func (a phase2BearerAuthenticator) Authenticate(r *http.Request) (resource.Principal, error) {
	if r.Header.Get("Authorization") != "Bearer phase2-token" {
		return resource.Principal{}, errors.New("invalid token")
	}
	return a.principal, nil
}

func TestTerraformPhase2RealHTTPPostgreSQL(t *testing.T) {
	url, terraformCLI := os.Getenv("KIM_POSTGRES_TEST_URL"), os.Getenv("KIM_TERRAFORM_CLI")
	if url == "" || terraformCLI == "" {
		t.Skip("KIM_POSTGRES_TEST_URL and KIM_TERRAFORM_CLI are required")
	}
	if out, err := exec.Command(terraformCLI, "version").CombinedOutput(); err != nil || !bytes.Contains(out, []byte("Terraform v1.14.9")) {
		t.Fatalf("Terraform 1.14.9 required: %s/%v", out, err)
	}
	ctx := context.Background()
	db, err := OpenWithMaxConnections(ctx, url, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('terraform-phase2',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%012x", uint64(time.Now().UnixNano())&0xffffffffffff)
	projectID := "30000000-0000-4000-8000-" + suffix
	pd := digestBytes([]byte("terraform-phase2-project-" + suffix))
	if _, err = db.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'terraform','phase2','project')`, projectID, "terraform-phase2-"+suffix, pd); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "terraform-phase2-"+suffix, pd); err != nil {
		t.Fatal(err)
	}
	principal := resource.Principal{Issuer: "terraform-phase2", Subject: "automation-" + suffix, Type: "AUTOMATION"}
	if _, err = db.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','','ADMIN','ACTIVE',1)`, "terraform-phase2-admin-"+suffix, principal.Issuer, principal.Subject, principal.Type); err != nil {
		t.Fatal(err)
	}
	host := "terraform-phase2-host-" + suffix
	fingerprint := digestBytes([]byte(host + "-cert"))
	prepareSessionIdentityFixture(t, ctx, db, host, 1, fingerprint)
	if _, err = AdmitAgentSession(ctx, db, AgentSessionAdmission{SessionAttemptID: host + "-attempt", HostID: host, ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, db, host)
	if err = UpdateHostReadinessGate(ctx, db, HostReadinessGate{HostID: host, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	authority, err := ArmHostOperationAuthority(ctx, db, HostAuthorityArmRequest{HostID: host, PolicyID: "terraform-phase2", PolicyGeneration: 1, ActorID: "integration", ReasonCode: "terraform_phase2"})
	if err != nil {
		t.Fatal(err)
	}
	class, backend, vg := "terraform-phase2-storage-"+suffix, "terraform-phase2-backend-"+suffix, "terraform-phase2-vg-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: backend, HostID: host, VGUUID: vg, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: class, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "terraform-phase2-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	store := NorthboundPhase2Store{DB: db}
	server := httptest.NewServer(httpapi.Server{Projects: project.Service{Store: NorthboundProjectStore{DB: db}}, Phase2: phase2.Service{Store: store}, Authenticator: phase2BearerAuthenticator{principal}}.Handler())
	defer server.Close()
	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	mirror := filepath.Join(work, "mirror", "registry.terraform.io", "kvm-infrastructure-manager", "kim", "0.1.0", "darwin_arm64")
	if err = os.MkdirAll(mirror, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(mirror, "terraform-provider-kim_v0.1.0")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = filepath.Join(repositoryRoot, "terraform-provider-kim")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("provider build: %s/%v", out, err)
	}
	cliConfig := filepath.Join(work, "terraform.rc")
	if err = os.WriteFile(cliConfig, []byte(fmt.Sprintf(`provider_installation {
  filesystem_mirror {
    path = %q
    include = ["registry.terraform.io/kvm-infrastructure-manager/kim"]
  }
  direct {
    exclude = ["registry.terraform.io/kvm-infrastructure-manager/kim"]
  }
}`, filepath.Join(work, "mirror"))), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`terraform {
  required_providers {
    kim = {
      source = "kvm-infrastructure-manager/kim"
      version = "0.1.0"
    }
  }
}
provider "kim" {
  endpoint = %q
  token = "phase2-token"
  client_id = "phase2-%s"
  request_timeout_seconds = 30
  operation_poll_interval_seconds = 1
}
resource "kim_network" "p2" {
  client_reference = "network"
  project_id = %q
  name = "overlay"
  profile = "STANDARD_OVERLAY"
  mtu = 1450
  segment_policy = "AUTO"
}
resource "kim_subnet" "p2" {
  client_reference = "subnet"
  project_id = %q
  network_id = kim_network.p2.id
  name = "v4"
  ip_family = "IPV4"
  cidr = "10.92.0.0/24"
  gateway_policy = "AUTO"
  gateway_address = "10.92.0.1"
  allocation_policy = "RANGE"
  allocation_start = "10.92.0.10"
  allocation_end = "10.92.0.200"
  dhcp_enabled = true
  dns_servers = ["10.92.0.53"]
}
resource "kim_port" "p2" {
  client_reference = "port"
  project_id = %q
  network_id = kim_network.p2.id
  subnet_id = kim_subnet.p2.id
  name = "unattached"
  mac_policy = "AUTO"
  ip_allocation_mode = "AUTO"
  attachment_policy = "ON_DEMAND"
  datapath_profile = "STANDARD"
}
resource "kim_volume" "p2" {
  client_reference = "volume"
  project_id = %q
  name = "blank-root"
  size_bytes = 16777216
  storage_class_id = %q
  storage_class_revision = 1
  bootable = true
  source_type = "BLANK"
}
`, server.URL+"/api/v1", suffix, projectID, projectID, projectID, projectID, class)
	configPath := filepath.Join(work, "main.tf")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfig, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1")
	runPhase2Terraform(t, terraformCLI, work, env, 0, "init", "-input=false")
	lvmClient := &volumeResourceLVMClient{vgUUID: vg}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		qualifyTerraformPhase2Operations(t, ctx, db, projectID, 4, authority.AuthorityGeneration, lvmClient, vg)
	}()
	runPhase2Terraform(t, terraformCLI, work, env, 0, "apply", "-input=false", "-auto-approve")
	<-workerDone
	plan := runPhase2Terraform(t, terraformCLI, work, env, 0, "plan", "-input=false", "-detailed-exitcode")
	if !strings.Contains(plan, "No changes") {
		t.Fatalf("Phase 2 second plan not no-op:\n%s", plan)
	}
	state := runPhase2Terraform(t, terraformCLI, work, env, 0, "show", "-json")
	for _, forbidden := range []string{"host_id", "backend_id", "vg_uuid", "lv_uuid", "binding_id", "allocation_id", "segment_id", "mac_address", "ip_address"} {
		if strings.Contains(strings.ToLower(state), `"`+forbidden+`"`) {
			t.Fatalf("physical identity leaked into Terraform state: %s", forbidden)
		}
	}
	for _, address := range []string{"kim_network.p2", "kim_subnet.p2", "kim_port.p2", "kim_volume.p2"} {
		runPhase2Terraform(t, terraformCLI, work, env, 0, "state", "rm", address)
	}
	var shown map[string]any
	if json.Unmarshal([]byte(state), &shown) != nil {
		t.Fatal("invalid state")
	}
	ids := phase2StateIDs(t, shown)
	for _, entry := range []struct{ address, prefix string }{{"kim_network.p2", "network/"}, {"kim_subnet.p2", "subnet/"}, {"kim_port.p2", "port/"}, {"kim_volume.p2", "volume/"}} {
		runPhase2Terraform(t, terraformCLI, work, env, 0, "import", entry.address, entry.prefix+ids[entry.address])
	}
	runPhase2Terraform(t, terraformCLI, work, env, 0, "plan", "-input=false", "-detailed-exitcode")
	destroyDone := make(chan struct{})
	go func() {
		defer close(destroyDone)
		qualifyTerraformPhase2Operations(t, ctx, db, projectID, 4, authority.AuthorityGeneration, lvmClient, vg)
	}()
	runPhase2Terraform(t, terraformCLI, work, env, 0, "destroy", "-input=false", "-auto-approve")
	<-destroyDone
}

func runPhase2Terraform(t *testing.T, binary, dir string, env []string, want int, args ...string) string {
	t.Helper()
	c := exec.Command(binary, args...)
	c.Dir, c.Env = dir, env
	out, err := c.CombinedOutput()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("terraform %v: %s/%v", args, out, err)
		}
		exit = ee.ExitCode()
	}
	if exit != want {
		t.Fatalf("terraform %v exit=%d want=%d:\n%s", args, exit, want, out)
	}
	return string(out)
}
func phase2StateIDs(t *testing.T, state map[string]any) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, raw := range state["values"].(map[string]any)["root_module"].(map[string]any)["resources"].([]any) {
		r := raw.(map[string]any)
		ids[r["address"].(string)] = r["values"].(map[string]any)["id"].(string)
	}
	return ids
}

func qualifyTerraformPhase2Operations(t *testing.T, ctx context.Context, db TxBeginner, project string, want int, authority int64, lvmClient *volumeResourceLVMClient, vg string) {
	t.Helper()
	seen := map[string]bool{}
	deadline := time.Now().Add(60 * time.Second)
	for len(seen) < want && time.Now().Before(deadline) {
		for _, kind := range []phase2.Kind{phase2.Network, phase2.Subnet, phase2.Port, phase2.Volume} {
			operation, err := nextPhase2Operation(ctx, db, project, kind, seen)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				t.Error(err)
				return
			}
			if operation == "" {
				continue
			}
			switch kind {
			case phase2.Network:
				qualifyTerraformNetworkOperation(t, ctx, db, operation)
			case phase2.Subnet:
				qualifyTerraformSubnetOperation(t, ctx, db, operation)
			case phase2.Port:
				qualifyTerraformPortOperation(t, ctx, db, operation)
			case phase2.Volume:
				qualifyTerraformVolumeOperation(t, ctx, db, operation, authority, lvmClient, vg)
			}
			seen[operation] = true
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(seen) != want {
		t.Errorf("Phase 2 operations completed=%d want=%d", len(seen), want)
	}
}
func nextPhase2Operation(ctx context.Context, db TxBeginner, project string, k phase2.Kind, seen map[string]bool) (string, error) {
	var q string
	switch k {
	case phase2.Network:
		q = `SELECT o.operation_id FROM kim.network_realization_operations_current o JOIN kim.networks_current r USING(network_id) WHERE r.project_id=$1 AND o.phase='PENDING' ORDER BY o.updated_at LIMIT 1`
	case phase2.Subnet:
		q = `SELECT o.operation_id FROM kim.subnet_realization_operations_current o JOIN kim.network_subnets_current r USING(subnet_id) WHERE r.project_id=$1 AND o.phase='PENDING' ORDER BY o.updated_at LIMIT 1`
	case phase2.Port:
		q = `SELECT o.operation_id FROM kim.port_realization_operations_current o JOIN kim.network_ports_current r USING(port_id) WHERE r.project_id=$1 AND o.phase='PENDING' ORDER BY o.updated_at LIMIT 1`
	case phase2.Volume:
		q = `SELECT o.operation_id FROM kim.volume_materialization_operations_current o JOIN kim.volumes_current r USING(volume_id) WHERE r.project_id=$1 AND o.phase='PENDING' ORDER BY o.updated_at LIMIT 1`
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, q, project).Scan(&id); err != nil {
		return "", err
	}
	if seen[id] {
		return "", pgx.ErrNoRows
	}
	return id, nil
}
func qualifyTerraformNetworkOperation(t *testing.T, ctx context.Context, db TxBeginner, id string) {
	c, e := ClaimNetworkRealization(ctx, db, id, "terraform", time.Minute)
	if e != nil {
		t.Error(e)
		return
	}
	_, p, e := ovnadapter.RestoreStoredNetworkPlan(c.CanonicalPlan, c.PlanDigest)
	if e != nil {
		t.Error(e)
		return
	}
	if e = AuthorizeNetworkRealizationApply(ctx, db, c); e != nil {
		t.Error(e)
		return
	}
	o := matchedNetworkObservation(c, p, "terraform-network", "RECEIVED")
	if c.OperationKind == "RETIRE" {
		o.BackendUUID = ""
		o.Observation.LogicalSwitchPresent = false
		o.Observation.OwnershipMarkerMatches = false
		o.Observation.PlanDigestMatches = false
	}
	if _, e = AcceptNetworkRealizationObservation(ctx, db, c, o); e != nil {
		t.Error(e)
	}
}
func qualifyTerraformSubnetOperation(t *testing.T, ctx context.Context, db TxBeginner, id string) {
	c, e := ClaimSubnetRealization(ctx, db, id, "terraform", time.Minute)
	if e != nil {
		t.Error(e)
		return
	}
	_, p, e := ovnadapter.RestoreStoredSubnetPlan(c.CanonicalPlan, c.PlanDigest)
	if e != nil {
		t.Error(e)
		return
	}
	if e = AuthorizeSubnetRealizationApply(ctx, db, c); e != nil {
		t.Error(e)
		return
	}
	o := matchedSubnetObservation(c, p, "terraform-dhcp", "RECEIVED")
	if c.OperationKind == "RETIRE" {
		o.BackendUUID = ""
		o.Observation.ObjectPresent = false
		o.Observation.OwnershipMarkerMatches = false
		o.Observation.PlanDigestMatches = false
	}
	if _, e = AcceptSubnetRealizationObservation(ctx, db, c, o); e != nil {
		t.Error(e)
	}
}
func qualifyTerraformPortOperation(t *testing.T, ctx context.Context, db TxBeginner, id string) {
	c, e := ClaimPortRealization(ctx, db, id, "terraform", time.Minute)
	if e != nil {
		t.Error(e)
		return
	}
	_, p, e := ovnadapter.RestoreStoredPortResourcePlan(c.CanonicalPlan, c.PlanDigest)
	if e != nil {
		t.Error(e)
		return
	}
	if e = AuthorizePortRealizationApply(ctx, db, c); e != nil {
		t.Error(e)
		return
	}
	o := matchedPortObservation(c, p, "terraform-port", "RECEIVED")
	if c.OperationKind == "RETIRE" {
		o.BackendUUID = ""
		o.Observation = ovnadapter.PortResourceObservation{LogicalPortName: p.LogicalPortName}
	}
	if _, e = AcceptPortRealizationObservation(ctx, db, c, o); e != nil {
		t.Error(e)
	}
}
func qualifyTerraformVolumeOperation(t *testing.T, ctx context.Context, db TxBeginner, id string, authority int64, c *volumeResourceLVMClient, vg string) {
	claim, e := ClaimVolumeMaterialization(ctx, db, id, "terraform", time.Minute)
	if e != nil {
		t.Error(e)
		return
	}
	mutation := agentexecution.Backend(locallvm.Backend{Client: c, VolumeGroups: map[string]string{vg: "kim_test_vg"}})
	read := agentexecution.Backend(locallvm.ReadBackBackend{Backend: locallvm.Backend{Client: c, VolumeGroups: map[string]string{vg: "kim_test_vg"}}})
	if claim.OperationKind == "RETIRE" {
		mutation = locallvm.DeleteBackend{Client: c, VolumeGroups: map[string]string{vg: "kim_test_vg"}}
		read = locallvm.DeleteBackend{Client: c, VolumeGroups: map[string]string{vg: "kim_test_vg"}, ReadBackOnly: true}
	}
	first, e := AuthorizeVolumeMaterializationCommand(ctx, db, claim, "terraform-job-"+id, "terraform-command-"+id, false)
	if e != nil {
		t.Error(e)
		return
	}
	runVolumeBackendWithLostResponse(t, ctx, db, first.CommandID, authority, CommandLeaseScopeMutation, mutation)
	if e = MarkVolumeMaterializationDispatchUnknown(ctx, db, claim); e != nil {
		t.Error(e)
		return
	}
	successor, e := ClaimVolumeMaterialization(ctx, db, id, "terraform-successor", time.Minute)
	if e != nil {
		t.Error(e)
		return
	}
	second, e := AuthorizeVolumeMaterializationCommand(ctx, db, successor, "terraform-read-job-"+id, "terraform-read-command-"+id, true)
	if e != nil {
		t.Error(e)
		return
	}
	verification := observeVolumeBackendAfterLostResponse(t, ctx, db, second.CommandID, "terraform-verification-"+id, authority, read)
	_, e = CompleteVolumeMaterialization(ctx, db, successor, CompleteVolumeMaterializationRequest{OperationID: id, OperationGeneration: successor.OperationGeneration, ClaimGeneration: successor.ClaimGeneration, ObservationID: "terraform-observation-" + id, VerificationID: verification})
	if e != nil {
		t.Error(e)
	}
}
