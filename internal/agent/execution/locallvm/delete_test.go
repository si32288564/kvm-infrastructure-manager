package locallvm

import (
	"context"
	"testing"
)

type fakeDeleteClient struct {
	lv          LogicalVolume
	found       bool
	removeCount int
}

func (*fakeDeleteClient) VerifyVolumeGroup(context.Context, string, string) error { return nil }
func (c *fakeDeleteClient) LogicalVolume(context.Context, string, string) (LogicalVolume, bool, error) {
	return c.lv, c.found, nil
}
func (c *fakeDeleteClient) RemoveLogicalVolume(context.Context, string, string) error {
	c.removeCount++
	c.found = false
	return nil
}

func deletePayload(volume string) map[string]any {
	return map[string]any{"backend_id": "backend-1", "backend_generation": 1, "vg_uuid": "vg-uuid", "expected_lv_uuid": "old-lv-uuid", "backend_resource_key": ResourceKey(volume), "binding_id": "binding-1", "binding_generation": 1, "cleanup_operation_id": "cleanup-1", "cleanup_generation": 1, "desired_state": "ABSENT"}
}

func TestDeleteBackendRemovesOnlyExactClosedLVIdentity(t *testing.T) {
	client := &fakeDeleteClient{found: true, lv: LogicalVolume{VGUUID: "vg-uuid", LVUUID: "old-lv-uuid", Name: ResourceKey("vol-1")}}
	backend := DeleteBackend{Client: client, VolumeGroups: map[string]string{"vg-uuid": "kim_test_vg"}}
	result, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", deletePayload("vol-1")))
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || client.removeCount != 1 {
		t.Fatalf("result=%+v removes=%d err=%v", result, client.removeCount, err)
	}
	if _, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", deletePayload("vol-1"))); err != nil || client.removeCount != 1 {
		t.Fatalf("absence replay mutated: removes=%d err=%v", client.removeCount, err)
	}
}

func TestDeleteBackendFencesHolderAndForeignReplacement(t *testing.T) {
	client := &fakeDeleteClient{found: true, lv: LogicalVolume{VGUUID: "vg-uuid", LVUUID: "old-lv-uuid", Name: ResourceKey("vol-1"), DeviceOpen: true}}
	backend := DeleteBackend{Client: client, VolumeGroups: map[string]string{"vg-uuid": "kim_test_vg"}}
	if _, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", deletePayload("vol-1"))); err == nil || client.removeCount != 0 {
		t.Fatalf("holder not fenced: removes=%d err=%v", client.removeCount, err)
	}
	client.lv = LogicalVolume{VGUUID: "vg-uuid", LVUUID: "foreign-new-uuid", Name: ResourceKey("vol-1")}
	result, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", deletePayload("vol-1")))
	if err != nil || result.Observation.State != "MATCHED" || client.removeCount != 0 || result.Observation.Evidence["foreign_replacement_present"] != true {
		t.Fatalf("foreign replacement result=%+v removes=%d err=%v", result, client.removeCount, err)
	}
}

func TestDeleteReadBackAndSchemaCannotMutate(t *testing.T) {
	client := &fakeDeleteClient{found: true, lv: LogicalVolume{VGUUID: "vg-uuid", LVUUID: "old-lv-uuid", Name: ResourceKey("vol-1")}}
	backend := DeleteBackend{Client: client, VolumeGroups: map[string]string{"vg-uuid": "kim_test_vg"}, ReadBackOnly: true}
	result, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", deletePayload("vol-1")))
	if err != nil || result.Observation.State != "NOT_APPLIED" || client.removeCount != 0 {
		t.Fatalf("read-back mutated: result=%+v removes=%d err=%v", result, client.removeCount, err)
	}
	bad := deletePayload("vol-1")
	bad["path"] = "/dev/vg/lv"
	if _, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", bad)); err == nil {
		t.Fatal("arbitrary path accepted")
	}
	bad = deletePayload("vol-1")
	bad["backend_resource_key"] = "caller-lv-name"
	if _, err := backend.Execute(context.Background(), lease(t, "volume:vol-1", bad)); err == nil {
		t.Fatal("caller LV name accepted")
	}
}
