//go:build !libvirt || !cgo

package ovsnetwork

import "errors"

func New(string, map[string]string) (*Backend, func() error, error) {
	return nil, nil, errors.New("Host Agent was built without standard libvirt support")
}
