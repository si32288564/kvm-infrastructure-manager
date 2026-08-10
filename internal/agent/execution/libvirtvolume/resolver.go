package libvirtvolume

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

type LocalLVMResolver struct {
	Client       locallvm.Client
	VolumeGroups map[string]string
}

func (resolver LocalLVMResolver) Resolve(ctx context.Context, vgUUID, resourceKey, expectedLVUUID string) (locallvm.LogicalVolume, string, error) {
	vgName, ok := resolver.VolumeGroups[vgUUID]
	if !ok || resolver.Client == nil {
		return locallvm.LogicalVolume{}, "", errors.New("Local LVM VG UUID is not configured")
	}
	if err := resolver.Client.VerifyVolumeGroup(ctx, vgName, vgUUID); err != nil {
		return locallvm.LogicalVolume{}, "", err
	}
	volume, found, err := resolver.Client.LogicalVolume(ctx, vgName, resourceKey)
	if err != nil {
		return locallvm.LogicalVolume{}, "", err
	}
	if !found || volume.VGUUID != vgUUID || volume.LVUUID != expectedLVUUID || volume.Name != resourceKey {
		return locallvm.LogicalVolume{}, "", errors.New("Local LVM Binding identity mismatch")
	}
	return volume, filepath.Join("/dev", vgName, resourceKey), nil
}

func (resolver LocalLVMResolver) VolumeGroupName(vgUUID string) (string, bool) {
	name, ok := resolver.VolumeGroups[vgUUID]
	return name, ok
}
