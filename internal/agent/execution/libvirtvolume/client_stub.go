//go:build !libvirt || !cgo

package libvirtvolume

import (
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

func New(string, locallvm.Client, map[string]string) (*Backend, func() error, error) {
	return nil, nil, errors.New("Host Agent was built without standard libvirt support")
}
