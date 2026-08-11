package libvirtvolume

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type fakeDomains struct {
	disk                     DiskObservation
	attachCount, detachCount int
	volume                   *locallvm.LogicalVolume
}

func (client *fakeDomains) Disk(context.Context, string, string) (DiskObservation, error) {
	return client.disk, nil
}
func (client *fakeDomains) AttachDisk(_ context.Context, _ string, disk DiskObservation) error {
	client.attachCount++
	client.disk = disk
	client.volume.DeviceOpen = true
	return nil
}
func (client *fakeDomains) DetachDisk(context.Context, string, DiskObservation) error {
	client.detachCount++
	client.disk = DiskObservation{Target: "vdb"}
	client.volume.DeviceOpen = false
	return nil
}

type fakeVolumes struct {
	volume locallvm.LogicalVolume
	err    error
}

func (resolver *fakeVolumes) Resolve(context.Context, string, string, string) (locallvm.LogicalVolume, string, error) {
	return resolver.volume, "/dev/kimvg/" + resolver.volume.Name, resolver.err
}

func TestAttachAndDetachRequireDeviceAndHolderReadBack(t *testing.T) {
	volumeID := "volume-1"
	volumes := &fakeVolumes{volume: locallvm.LogicalVolume{VGUUID: "vg-uuid", LVUUID: "lv-uuid", Name: locallvm.ResourceKey(volumeID), SizeBytes: 64 * locallvm.MiB}}
	domains := &fakeDomains{volume: &volumes.volume}
	backend := Backend{Domains: domains, Volumes: volumes}
	attach := lease(t, map[string]any{"domain_uuid": "11111111-1111-4111-8111-111111111111", "volume_id": volumeID, "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey(volumeID), "disk_slot": 1, "desired_state": "ATTACHED", "access_mode": "SINGLE_WRITER"})
	result, err := backend.Execute(context.Background(), attach)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || domains.attachCount != 1 || !volumes.volume.DeviceOpen {
		t.Fatalf("attach result=%#v err=%v count=%d volume=%#v", result, err, domains.attachCount, volumes.volume)
	}
	if result.Observation.Evidence["target_device"] != "vdb" || result.Observation.Evidence["source_identity_matches"] != true {
		t.Fatalf("attach evidence=%#v", result.Observation.Evidence)
	}
	if _, err := backend.Execute(context.Background(), attach); err != nil || domains.attachCount != 1 {
		t.Fatalf("idempotent attach err=%v count=%d", err, domains.attachCount)
	}
	detach := attach
	detach.CommandPayload = payload(t, map[string]any{"domain_uuid": "11111111-1111-4111-8111-111111111111", "volume_id": volumeID, "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey(volumeID), "disk_slot": 1, "desired_state": "DETACHED", "access_mode": "SINGLE_WRITER"})
	result, err = backend.Execute(context.Background(), detach)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || domains.detachCount != 1 || volumes.volume.DeviceOpen {
		t.Fatalf("detach result=%#v err=%v count=%d volume=%#v", result, err, domains.detachCount, volumes.volume)
	}
}

func TestAttachDoesNotReplaceConflictingTarget(t *testing.T) {
	volumeID := "volume-2"
	volumes := &fakeVolumes{volume: locallvm.LogicalVolume{VGUUID: "vg-uuid", LVUUID: "lv-uuid", Name: locallvm.ResourceKey(volumeID)}}
	domains := &fakeDomains{volume: &volumes.volume, disk: DiskObservation{Present: true, SourcePath: "/dev/foreign/lv", Target: "vdb", Serial: "foreign"}}
	backend := Backend{Domains: domains, Volumes: volumes}
	result, err := backend.Execute(context.Background(), lease(t, map[string]any{"domain_uuid": "22222222-2222-4222-8222-222222222222", "volume_id": volumeID, "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey(volumeID), "disk_slot": 1, "desired_state": "ATTACHED", "access_mode": "SINGLE_WRITER"}))
	if err != nil || result.Outcome != "UNKNOWN" || result.Observation.State != "CONFLICTING" || domains.attachCount != 0 {
		t.Fatalf("conflicting target result=%#v err=%v attach=%d", result, err, domains.attachCount)
	}
}

func TestRootDiskIsObservationOnly(t *testing.T) {
	volumeID := "root-volume"
	volumes := &fakeVolumes{volume: locallvm.LogicalVolume{VGUUID: "vg-uuid", LVUUID: "lv-uuid", Name: locallvm.ResourceKey(volumeID), DeviceOpen: true}}
	domains := &fakeDomains{volume: &volumes.volume}
	backend := Backend{Domains: domains, Volumes: volumes}
	root := lease(t, map[string]any{"domain_uuid": "44444444-4444-4444-8444-444444444444", "volume_id": volumeID, "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey(volumeID), "disk_slot": 0, "desired_state": "ATTACHED", "access_mode": "SINGLE_WRITER"})
	result, err := backend.Execute(context.Background(), root)
	if err != nil || result.Outcome != "UNKNOWN" || result.Observation.State != "CONFLICTING" || domains.attachCount != 0 {
		t.Fatalf("missing root result=%#v err=%v attach=%d", result, err, domains.attachCount)
	}
	domains.disk = DiskObservation{Present: true, SourcePath: "/dev/kimvg/" + locallvm.ResourceKey(volumeID), Target: "vda", Serial: volumeSerial(volumeID)}
	result, err = backend.Execute(context.Background(), root)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || domains.attachCount != 0 {
		t.Fatalf("existing root result=%#v err=%v attach=%d", result, err, domains.attachCount)
	}
	detach := root
	detach.CommandPayload = payload(t, map[string]any{"domain_uuid": "44444444-4444-4444-8444-444444444444", "volume_id": volumeID, "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey(volumeID), "disk_slot": 0, "desired_state": "DETACHED", "access_mode": "SINGLE_WRITER"})
	if _, err := backend.Execute(context.Background(), detach); err == nil {
		t.Fatal("typed root detach was accepted")
	}
}

func TestBackendRejectsOpenEndedAttachmentInput(t *testing.T) {
	backend := Backend{Domains: &fakeDomains{}, Volumes: &fakeVolumes{}}
	base := map[string]any{"domain_uuid": "33333333-3333-4333-8333-333333333333", "volume_id": "volume-3", "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey("volume-3"), "disk_slot": 1, "desired_state": "ATTACHED", "access_mode": "SINGLE_WRITER"}
	cases := []map[string]any{
		with(base, "path", "/dev/vg/lv"),
		with(base, "backend_resource_key", "caller-name"), with(base, "access_mode", "SHARED_WRITER"),
		with(base, "desired_state", "DELETE"),
	}
	for _, value := range cases {
		if _, err := backend.Execute(context.Background(), lease(t, value)); err == nil {
			t.Fatalf("accepted invalid attachment payload %#v", value)
		}
	}
	backend.Volumes = &fakeVolumes{err: errors.New("permission denied")}
	if _, err := backend.Execute(context.Background(), lease(t, base)); err == nil {
		t.Fatal("holder read-back error was converted to absence")
	}
}

func lease(t *testing.T, value map[string]any) contract.CommandLease {
	t.Helper()
	payload := payload(t, value)
	digest := sha256.Sum256(payload)
	return contract.CommandLease{TargetResourceID: "attachment:attachment-1", CommandPayload: payload, CommandPayloadDigest: hex.EncodeToString(digest[:]), AttemptIndex: 1}
}

func payload(t *testing.T, value map[string]any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func with(source map[string]any, key string, value any) map[string]any {
	result := make(map[string]any, len(source)+1)
	for name, item := range source {
		result[name] = item
	}
	result[key] = value
	return result
}
