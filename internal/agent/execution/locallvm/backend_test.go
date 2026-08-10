package locallvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type fakeClient struct {
	vgUUID      string
	lv          LogicalVolume
	found       bool
	createCount int
	err         error
}

func (client *fakeClient) VerifyVolumeGroup(context.Context, string, string) error { return client.err }
func (client *fakeClient) LogicalVolume(context.Context, string, string) (LogicalVolume, bool, error) {
	return client.lv, client.found, client.err
}
func (client *fakeClient) CreateLogicalVolume(_ context.Context, _ string, name string, size uint64) error {
	if client.err != nil {
		return client.err
	}
	client.createCount++
	client.found = true
	client.lv = LogicalVolume{VGUUID: client.vgUUID, LVUUID: "lv-uuid", Name: name, SizeBytes: size * MiB}
	return nil
}

func TestExecuteCreatesKIMOwnedVolumeAndReadsBack(t *testing.T) {
	client := &fakeClient{vgUUID: "vg-uuid"}
	backend := Backend{Client: client, VolumeGroups: map[string]string{"vg-uuid": "kim_test_vg"}}
	result, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "PRESENT"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || client.createCount != 1 {
		t.Fatalf("unexpected result: %#v creates=%d", result, client.createCount)
	}
	if got := result.Observation.Evidence["backend_resource_key"]; got != ResourceKey("vol-1") {
		t.Fatalf("unexpected resource key %v", got)
	}
	if result.Observation.Evidence["observed_lv_uuid"] != "lv-uuid" {
		t.Fatal("read-back LV UUID was not returned")
	}
	if _, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "PRESENT"})); err != nil || client.createCount != 1 {
		t.Fatalf("idempotent replay created another LV: err=%v creates=%d", err, client.createCount)
	}
}

func TestObserveSeparatesAbsentAndConflicting(t *testing.T) {
	client := &fakeClient{vgUUID: "vg-uuid"}
	backend := Backend{Client: client, VolumeGroups: map[string]string{"vg-uuid": "kim_test_vg"}}
	verification := verification(t, "volume:vol-2", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "PRESENT"})
	observation, err := backend.Observe(context.Background(), verification)
	if err != nil || observation.State != "NOT_APPLIED" {
		t.Fatalf("unexpected absent observation: %#v err=%v", observation, err)
	}
	client.found = true
	client.lv = LogicalVolume{VGUUID: "vg-uuid", LVUUID: "lv-uuid", Name: ResourceKey("vol-2"), SizeBytes: 32 * MiB}
	observation, err = backend.Observe(context.Background(), verification)
	if err != nil || observation.State != "CONFLICTING" {
		t.Fatalf("unexpected conflict observation: %#v err=%v", observation, err)
	}
}

func TestBackendRejectsOpenEndedInputAndUnconfiguredVG(t *testing.T) {
	backend := Backend{Client: &fakeClient{}, VolumeGroups: map[string]string{"vg-uuid": "kim_test_vg"}}
	tests := []struct {
		target  string
		payload map[string]any
	}{
		{"volume:vol-1", map[string]any{"vg_uuid": "other", "size_mib": 64, "desired_state": "PRESENT"}},
		{"volume:vol-1", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "ABSENT"}},
		{"volume:vol-1", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "PRESENT", "path": "/dev/vg/lv"}},
		{"volume:/bad", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "PRESENT"}},
	}
	for _, test := range tests {
		if _, err := backend.Execute(context.Background(), lease(t, test.target, test.payload)); err == nil {
			t.Fatalf("accepted invalid request target=%q payload=%v", test.target, test.payload)
		}
	}
	backend.Client = &fakeClient{err: errors.New("permission denied")}
	if _, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", map[string]any{"vg_uuid": "vg-uuid", "size_mib": 64, "desired_state": "PRESENT"})); err == nil {
		t.Fatal("backend error was converted to absence")
	}
}

func lease(t *testing.T, target string, value map[string]any) contract.CommandLease {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return contract.CommandLease{TargetResourceID: target, CommandPayload: payload, CommandPayloadDigest: hex.EncodeToString(digest[:]), AttemptIndex: 1}
}

func verification(t *testing.T, target string, value map[string]any) contract.VerificationRequest {
	t.Helper()
	l := lease(t, target, value)
	return contract.VerificationRequest{TargetResourceID: target, CommandPayload: l.CommandPayload, CommandPayloadDigest: l.CommandPayloadDigest, AttemptIndex: 1}
}
