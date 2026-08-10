package localimage_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/localimage"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

func TestRealLocalLVMImageMaterialization(t *testing.T) {
	cacheRoot, vgUUID, vgName := os.Getenv("KIM_IMAGE_CACHE_ROOT"), os.Getenv("KIM_IMAGE_VG_UUID"), os.Getenv("KIM_IMAGE_VG_NAME")
	lvUUID, checksum := os.Getenv("KIM_IMAGE_LV_UUID"), os.Getenv("KIM_IMAGE_SHA256")
	if cacheRoot == "" || vgUUID == "" || vgName == "" || lvUUID == "" || checksum == "" {
		t.Skip("real Local LVM Image qualification environment is not configured")
	}
	client, err := locallvm.NewCLIClient()
	if err != nil {
		t.Fatal(err)
	}
	const (
		vmID      = "77777777-7777-4777-8777-777777777777"
		volumeID  = "qualification-image-volume-20260810"
		imageID   = "qualification-image-20260810"
		imageSize = uint64(8 * 1024 * 1024)
	)
	backend := localimage.Backend{CacheRoot: cacheRoot, Volumes: libvirtvolume.LocalLVMResolver{Client: client, VolumeGroups: map[string]string{vgUUID: vgName}}}
	payload, _ := json.Marshal(map[string]any{
		"domain_uuid": vmID, "materialization_generation": 1,
		"image_id": imageID, "image_revision": 1, "image_checksum": checksum,
		"image_size_bytes": imageSize, "volume_id": volumeID, "vg_uuid": vgUUID,
		"lv_uuid": lvUUID, "backend_resource_key": locallvm.ResourceKey(volumeID),
		"desired_state": localimage.StateRealized,
	})
	result, err := backend.Execute(context.Background(), contract.CommandLease{
		CommandID: "qualification-image-command", AttemptIndex: 1,
		CommandType: localimage.CommandType, CommandSchemaVersion: localimage.SchemaVersion,
		TargetResourceID: "vm:" + vmID, CommandPayload: payload,
	})
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("real Local LVM Image materialization = %#v, %v", result, err)
	}
	if result.Observation.Evidence["observed_content_digest"] != checksum || result.Observation.Evidence["observed_lv_uuid"] != lvUUID || result.Observation.Evidence["holder_open"] != false {
		t.Fatalf("real Local LVM Image evidence = %#v", result.Observation.Evidence)
	}
	encoded, _ := json.Marshal(result.Observation.Evidence)
	digest := sha256.Sum256(encoded)
	if result.Observation.Digest != hex.EncodeToString(digest[:]) {
		t.Fatal("Image observation digest is not canonical evidence digest")
	}
}
