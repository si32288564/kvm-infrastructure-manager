//go:build !libvirt || !cgo

package ovsnetwork

import "errors"

func New(string, map[string]string) (*Backend, func() error, error) {
	return nil, nil, errors.New("Host Agent was built without standard libvirt support")
}
func NewDataplane(string, map[string]string) (*DataplaneBackend, func() error, error) {
	return nil, nil, errors.New("OVS dataplane backend requires libvirt+cgo")
}
