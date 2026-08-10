package localimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type fakeResolver struct {
	volume locallvm.LogicalVolume
	path   string
}

func (resolver fakeResolver) Resolve(_ context.Context, _, _, _ string) (locallvm.LogicalVolume, string, error) {
	return resolver.volume, resolver.path, nil
}

func TestBackendMaterializesAndReadsBackVerifiedImage(t *testing.T) {
	root := t.TempDir()
	content := []byte("verified KIM root image\n")
	digest := sha256.Sum256(content)
	checksum := hex.EncodeToString(digest[:])
	if err := os.Mkdir(filepath.Join(root, "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sha256", checksum), content, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "lv")
	if err := os.WriteFile(target, make([]byte, len(content)+4096), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := Backend{CacheRoot: root, Volumes: fakeResolver{
		volume: locallvm.LogicalVolume{VGUUID: "vg-1", LVUUID: "lv-1", SizeBytes: uint64(len(content) + 4096)},
		path:   target,
	}}
	payload, _ := json.Marshal(map[string]any{
		"domain_uuid": "55555555-5555-4555-8555-555555555555", "materialization_generation": 1,
		"image_id": "image-1", "image_revision": 1, "image_checksum": checksum,
		"image_size_bytes": len(content), "volume_id": "volume-1", "vg_uuid": "vg-1",
		"lv_uuid": "lv-1", "backend_resource_key": locallvm.ResourceKey("volume-1"), "desired_state": StateRealized,
	})
	lease := contract.CommandLease{CommandType: CommandType, CommandSchemaVersion: SchemaVersion,
		TargetResourceID: "vm:55555555-5555-4555-8555-555555555555", CommandPayload: payload,
		AttemptIndex: 1}
	result, err := backend.Execute(context.Background(), lease)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("execute = %#v, %v", result, err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual[:len(content)]) != string(content) {
		t.Fatalf("target content mismatch: %v", err)
	}
	observation, err := backend.Observe(context.Background(), contract.VerificationRequest{
		CommandType: CommandType, CommandSchemaVersion: SchemaVersion,
		TargetResourceID: lease.TargetResourceID, CommandPayload: payload, AttemptIndex: 2,
	})
	if err != nil || observation.State != "MATCHED" || observation.Evidence["observed_content_digest"] != checksum {
		t.Fatalf("observation = %#v, %v", observation, err)
	}
}

func TestBackendFailsClosed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "lv")
	if err := os.WriteFile(target, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := Backend{CacheRoot: root, Volumes: fakeResolver{
		volume: locallvm.LogicalVolume{VGUUID: "vg", LVUUID: "lv", SizeBytes: 64, DeviceOpen: true}, path: target,
	}}
	payload := []byte(`{"domain_uuid":"55555555-5555-4555-8555-555555555555","materialization_generation":1,"image_id":"i","image_revision":1,"image_checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","image_size_bytes":1,"volume_id":"v","vg_uuid":"vg","lv_uuid":"lv","backend_resource_key":"wrong","desired_state":"REALIZED","source_uri":"file:///tmp/x"}`)
	_, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "vm:55555555-5555-4555-8555-555555555555", CommandPayload: payload})
	if err == nil {
		t.Fatal("untyped/path-bearing payload was accepted")
	}
}
