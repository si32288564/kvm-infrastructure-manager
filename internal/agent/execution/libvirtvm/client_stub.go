//go:build !libvirt || !cgo

package libvirtvm

import (
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
)

func New(string, libvirtvolume.VolumeResolver) (*Backend, func() error, error) {
	return nil, nil, errors.New("Host Agent was built without standard libvirt support")
}

func NewCleanup(string) (*CleanupBackend, func() error, error) {
	return nil, nil, errors.New("Host Agent was built without standard libvirt support")
}
