package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTerraformVMApplyNoopPowerImportDestroy(t *testing.T) {
	terraformCLI := os.Getenv("KIM_TERRAFORM_CLI")
	if terraformCLI == "" {
		t.Skip("KIM_TERRAFORM_CLI is required")
	}
	if out, err := exec.Command(terraformCLI, "version").CombinedOutput(); err != nil || !bytes.Contains(out, []byte("Terraform v1.14.9")) {
		t.Fatalf("Terraform 1.14.9 required: %s/%v", out, err)
	}
	const id = "88000000-0000-4000-8000-000000000001"
	var mu sync.Mutex
	revision := int64(1)
	power := "RUNNING"
	deleted := false
	resourceBody := func() map[string]any {
		return map[string]any{"id": id, "projectId": "10000000-0000-4000-8000-000000000001", "name": "acceptance", "flavorId": "20000000-0000-4000-8000-000000000001", "flavorRevision": 1, "imageId": "30000000-0000-4000-8000-000000000001", "imageRevision": 1, "availabilityPolicyId": "40000000-0000-4000-8000-000000000001", "availabilityPolicyRevision": 1, "placementScopeId": "scope", "placementScopeGeneration": 1, "rootVolume": map[string]any{"id": "50000000-0000-4000-8000-000000000001", "revision": 1}, "ports": []any{}, "dataVolumes": []any{}, "desiredPowerState": power, "deleteProtection": false, "revision": revision, "runtimeIntentGeneration": revision, "lifecycleState": "ACTIVE", "convergenceState": "CONVERGED", "operationId": "", "createdAt": "2026-08-15T00:00:00Z", "updatedAt": "2026-08-15T00:00:00Z"}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/vms":
			w.Header().Set("ETag", `"1"`)
			body := resourceBody()
			body["operationId"] = "create-operation"
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/operations/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/v1/operations/"), "type": "VM", "targetResourceType": "VM", "targetResourceId": id, "targetRevision": revision, "acceptedAt": "2026-08-15T00:00:00Z", "phase": "SUCCEEDED", "terminalState": "VERIFIED", "retryable": false, "cancellable": false, "completedAt": "2026-08-15T00:00:01Z"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/vms/"+id:
			if deleted {
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "RESOURCE_NOT_FOUND"})
				return
			}
			w.Header().Set("ETag", fmt.Sprintf(`"%d"`, revision))
			_ = json.NewEncoder(w).Encode(resourceBody())
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/vms/"+id:
			var patch map[string]any
			_ = json.NewDecoder(r.Body).Decode(&patch)
			revision++
			if v, ok := patch["desiredPowerState"].(string); ok {
				power = v
			}
			body := resourceBody()
			if _, ok := patch["desiredPowerState"]; ok {
				body["operationId"] = "power-operation"
				body["convergenceState"] = "PENDING"
				powerState := power
				body["desiredPowerState"] = powerState
			}
			w.Header().Set("ETag", fmt.Sprintf(`"%d"`, revision))
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/vms/"+id:
			deleted = true
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "delete-operation", "type": "DELETE", "targetResourceType": "VM", "targetResourceId": id, "targetRevision": revision + 1, "acceptedAt": "2026-08-15T00:00:00Z", "phase": "PENDING", "retryable": false, "cancellable": false})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	work := t.TempDir()
	mirror := filepath.Join(work, "mirror", "registry.terraform.io", "kvm-infrastructure-manager", "kim", "0.1.0", "darwin_arm64")
	if err := os.MkdirAll(mirror, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(mirror, "terraform-provider-kim_v0.1.0")
	build := exec.Command("go", "build", "-o", binary, "../..")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("provider build: %s/%v", out, err)
	}
	cliConfig := filepath.Join(work, "terraform.rc")
	if err := os.WriteFile(cliConfig, []byte(fmt.Sprintf(`provider_installation {
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
	config := func(desired string) string {
		return fmt.Sprintf(`terraform {
  required_providers {
    kim = {
      source = "kvm-infrastructure-manager/kim"
      version = "0.1.0"
    }
  }
}
provider "kim" {
 endpoint = %q
 token = "token"
 client_id = "acceptance"
 operation_poll_interval_seconds = 1
}
resource "kim_vm" "test" {
 client_reference = "vm"
 project_id = "10000000-0000-4000-8000-000000000001"
 name = "acceptance"
 flavor_id = "20000000-0000-4000-8000-000000000001"
 flavor_revision = 1
 image_id = "30000000-0000-4000-8000-000000000001"
 image_revision = 1
 availability_policy_id = "40000000-0000-4000-8000-000000000001"
 availability_policy_revision = 1
 placement_scope_id = "scope"
 placement_scope_generation = 1
 root_volume = { id = "50000000-0000-4000-8000-000000000001", revision = 1 }
 ports = []
 data_volumes = []
 desired_power_state = %q
}`, server.URL+"/api/v1", desired)
	}
	path := filepath.Join(work, "main.tf")
	if err := os.WriteFile(path, []byte(config("RUNNING")), 0600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfig, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1")
	runTerraformVM(t, terraformCLI, work, env, 0, "init", "-input=false")
	runTerraformVM(t, terraformCLI, work, env, 0, "apply", "-input=false", "-auto-approve")
	if out := runTerraformVM(t, terraformCLI, work, env, 0, "plan", "-input=false", "-detailed-exitcode"); !strings.Contains(out, "No changes") {
		t.Fatalf("second plan not no-op: %s", out)
	}
	if err := os.WriteFile(path, []byte(config("SHUTOFF")), 0600); err != nil {
		t.Fatal(err)
	}
	runTerraformVM(t, terraformCLI, work, env, 0, "apply", "-input=false", "-auto-approve")
	state := runTerraformVM(t, terraformCLI, work, env, 0, "show", "-json")
	for _, field := range []string{"host_id", "admission_id", "binding_id", "lv_uuid", "materialization_generation", "recovery_generation", "evacuation_generation"} {
		if strings.Contains(strings.ToLower(state), `"`+field+`"`) {
			t.Fatalf("physical incarnation leaked: %s", field)
		}
	}
	runTerraformVM(t, terraformCLI, work, env, 0, "state", "rm", "kim_vm.test")
	runTerraformVM(t, terraformCLI, work, env, 0, "import", "kim_vm.test", "vm/"+id)
	runTerraformVM(t, terraformCLI, work, env, 0, "plan", "-input=false", "-detailed-exitcode")
	runTerraformVM(t, terraformCLI, work, env, 0, "destroy", "-input=false", "-auto-approve")
}
func runTerraformVM(t *testing.T, binary, dir string, env []string, want int, args ...string) string {
	t.Helper()
	c := exec.Command(binary, args...)
	c.Dir, c.Env = dir, env
	out, err := c.CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("terraform %v: %v", args, err)
		}
	}
	if exit != want {
		t.Fatalf("terraform %v exit=%d want=%d:\n%s", args, exit, want, out)
	}
	return string(out)
}
