package targetexecutor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdPackageProfileValidation(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "profile.json")
	digest := strings.Repeat("a", 64)
	binaryDigest := strings.Repeat("b", 64)
	raw := fmt.Sprintf(`{
  "schema_version":"kim.upgrade.systemd-package-profile/v1",
  "component_type":"CONTROL_WORKER",
  "component_id":"kim-upgrade-fixture-component",
  "package_name":"kim-upgrade-fixture-component",
  "service_name":"kim-upgrade-fixture-component.service",
  "binary_path":"/usr/lib/kim-upgrade-fixture/component",
  "health_path":"/run/kim-upgrade-fixture/health.json",
  "health_schema":"kim.upgrade.fixture-health/v1",
  "artifacts":{"%s":{"package_path":"/var/lib/kim-upgrade-fixture/v2.deb","package_version":"2.0.0","binary_digest":"%s"}}
}`, digest, binaryDigest)
	if err := os.WriteFile(profilePath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	backend, err := NewSystemdPackageBackend(profilePath)
	if err != nil || backend.profile.PackageName != "kim-upgrade-fixture-component" {
		t.Fatalf("backend=%+v err=%v", backend, err)
	}
	matching := Target{ComponentType: "CONTROL_WORKER", ComponentID: "kim-upgrade-fixture-component", TargetArtifactDigest: digest}
	if _, err := backend.artifactFor(matching); err != nil {
		t.Fatalf("matching Target rejected: %v", err)
	}
	conflicting := matching
	conflicting.ComponentID = "foreign-component"
	if _, err := backend.artifactFor(conflicting); err == nil {
		t.Fatal("foreign Target component was accepted")
	}
	if err := os.Chmod(profilePath, 0o622); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSystemdPackageBackend(profilePath); err == nil {
		t.Fatal("writable backend profile was accepted")
	}
}
