//go:build libvirt && cgo

package libvirtvm_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	libvirt "libvirt.org/go/libvirt"
)

func TestDefineDomainFromTypedAdmissionPlanOnKVM(t *testing.T) {
	uri, vgUUID, vgName := os.Getenv("KIM_LIBVIRT_SYSTEM_URI"), os.Getenv("KIM_LOCAL_LVM_VG_UUID"), os.Getenv("KIM_LOCAL_LVM_VG_NAME")
	lvUUID, domainUUID := os.Getenv("KIM_LOCAL_LVM_LV_UUID"), os.Getenv("KIM_KVM_DOMAIN_UUID")
	if uri == "" || vgUUID == "" || vgName == "" || lvUUID == "" || domainUUID == "" {
		t.Skip("complete KVM VM materialization qualification environment is not set")
	}
	lvmClient, err := locallvm.NewCLIClient()
	if err != nil {
		t.Fatal(err)
	}
	resolver := libvirtvolume.LocalLVMResolver{Client: lvmClient, VolumeGroups: map[string]string{vgUUID: vgName}}
	backend, closeBackend, err := libvirtvm.New(uri, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if existing, lookupErr := connection.LookupDomainByUUIDString(domainUUID); lookupErr == nil {
		existing.Free()
		t.Fatal("qualification Domain UUID already exists")
	}
	defer func() {
		if domain, lookupErr := connection.LookupDomainByUUIDString(domainUUID); lookupErr == nil {
			_ = domain.Undefine()
			_ = domain.Free()
		}
	}()
	volumeID := "qualification-vm-root-20260810"
	payload, err := json.Marshal(map[string]any{
		"domain_uuid": domainUUID, "materialization_generation": 1,
		"vcpus": 2, "memory_mib": 512, "desired_state": "DEFINED",
		"image_id": "qualification-image", "image_revision": 1,
		"image_materialization_state": "PENDING", "network_realization_state": "PENDING",
		"root_volume": map[string]any{"volume_id": volumeID, "vg_uuid": vgUUID, "lv_uuid": lvUUID, "backend_resource_key": locallvm.ResourceKey(volumeID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	lease := contract.CommandLease{TargetResourceID: "vm:" + domainUUID, CommandPayload: payload, CommandPayloadDigest: hex.EncodeToString(digest[:]), AttemptIndex: 1}
	result, err := backend.Execute(t.Context(), lease)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("typed Domain define = %#v, %v", result, err)
	}
	result, err = backend.Execute(t.Context(), lease)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("idempotent Domain define = %#v, %v", result, err)
	}
	domain, err := connection.LookupDomainByUUIDString(domainUUID)
	if err != nil {
		t.Fatal(err)
	}
	defer domain.Free()
	active, err := domain.IsActive()
	if err != nil || active {
		t.Fatalf("defined Domain active=%v err=%v", active, err)
	}
}
