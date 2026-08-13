//go:build !libvirt || !cgo

package sriovnetwork

import "errors"

func New(string) (*Backend, func() error, error) {
	return nil, nil, errors.New("SR-IOV Network backend requires libvirt+cgo")
}
func NewRetirement(string) (*RetirementBackend, func() error, error) {
	return nil, nil, errors.New("VF retirement backend requires libvirt+cgo")
}
