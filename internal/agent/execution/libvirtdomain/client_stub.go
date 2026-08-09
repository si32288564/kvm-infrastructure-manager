//go:build !libvirt || !cgo

package libvirtdomain

import "errors"

func New(string) (*Backend, func() error, error) {
	return nil, nil, errors.New("Host Agent was built without standard libvirt support")
}
