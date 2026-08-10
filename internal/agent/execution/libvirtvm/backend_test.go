package libvirtvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type fakeDomains struct {
	current DomainObservation
	defines int
}

func (client *fakeDomains) Domain(context.Context, string) (DomainObservation, error) {
	return client.current, nil
}
func (client *fakeDomains) Define(_ context.Context, spec DomainSpec) error {
	client.defines++
	client.current = spec
	return nil
}

type fakeVolumes struct{}

func (fakeVolumes) Resolve(_ context.Context, vgUUID, key, lvUUID string) (locallvm.LogicalVolume, string, error) {
	return locallvm.LogicalVolume{VGUUID: vgUUID, LVUUID: lvUUID, Name: key}, "/dev/kimvg/" + key, nil
}

func TestDefineUsesClosedPlanAndReadBack(t *testing.T) {
	domains := &fakeDomains{}
	backend := Backend{Domains: domains, Volumes: fakeVolumes{}}
	lease := vmLease(t, vmPayload(t))
	result, err := backend.Execute(t.Context(), lease)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || domains.defines != 1 {
		t.Fatalf("define result=%#v err=%v count=%d", result, err, domains.defines)
	}
	if _, err := backend.Execute(t.Context(), lease); err != nil || domains.defines != 1 {
		t.Fatalf("idempotent define err=%v count=%d", err, domains.defines)
	}
}

func TestDefineRefusesForeignDomainAndOpenEndedInput(t *testing.T) {
	domains := &fakeDomains{current: DomainObservation{Present: true, UUID: "11111111-1111-4111-8111-111111111111", Name: "foreign"}}
	backend := Backend{Domains: domains, Volumes: fakeVolumes{}}
	result, err := backend.Execute(t.Context(), vmLease(t, vmPayload(t)))
	if err != nil || result.Outcome != "UNKNOWN" || result.Observation.State != "CONFLICTING" || domains.defines != 0 {
		t.Fatalf("foreign Domain result=%#v err=%v defines=%d", result, err, domains.defines)
	}
	unsafe := vmPayload(t)
	unsafe["xml"] = "<domain/>"
	if _, err := backend.Execute(t.Context(), vmLease(t, unsafe)); err == nil {
		t.Fatal("raw XML was accepted")
	}
	unsafe = vmPayload(t)
	unsafe["image_materialization_state"] = "VERIFIED"
	if _, err := backend.Execute(t.Context(), vmLease(t, unsafe)); err == nil {
		t.Fatal("unimplemented Image realization was promoted")
	}
	unsafe = vmPayload(t)
	unsafe["root_volume"].(map[string]any)["backend_resource_key"] = "caller-selected-lv"
	if _, err := backend.Execute(t.Context(), vmLease(t, unsafe)); err == nil {
		t.Fatal("caller-selected root LV key was accepted")
	}
}

func vmPayload(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"domain_uuid":                "11111111-1111-4111-8111-111111111111",
		"materialization_generation": 1, "vcpus": 2, "memory_mib": 1024,
		"desired_state": "DEFINED", "image_id": "image-1", "image_revision": 1,
		"image_materialization_state": "PENDING", "network_realization_state": "PENDING",
		"root_volume": map[string]any{"volume_id": "volume-1", "vg_uuid": "vg-uuid", "lv_uuid": "lv-uuid", "backend_resource_key": locallvm.ResourceKey("volume-1")},
	}
}

func vmLease(t *testing.T, value map[string]any) contract.CommandLease {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return contract.CommandLease{TargetResourceID: "vm:11111111-1111-4111-8111-111111111111", CommandPayload: payload, CommandPayloadDigest: hex.EncodeToString(digest[:]), AttemptIndex: 1}
}
