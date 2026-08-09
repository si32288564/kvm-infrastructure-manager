//go:build libvirt && cgo

package libvirtdomain

import (
	"context"
	"errors"

	libvirt "libvirt.org/go/libvirt"
)

type libvirtClient struct{ connection *libvirt.Connect }

func New(uri string) (*Backend, func() error, error) {
	if uri == "" {
		return nil, nil, errors.New("libvirt URI is required")
	}
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	client := &libvirtClient{connection: connection}
	return &Backend{Client: client}, func() error { _, err := connection.Close(); return err }, nil
}

func (client *libvirtClient) DomainState(ctx context.Context, uuid string) (string, error) {
	domain, err := client.lookup(ctx, uuid)
	if err != nil {
		return "", err
	}
	defer domain.Free()
	state, _, err := domain.GetState()
	if err != nil {
		return "", errors.New("read libvirt Domain state failed")
	}
	switch state {
	case libvirt.DOMAIN_RUNNING, libvirt.DOMAIN_BLOCKED:
		return StateRunning, nil
	case libvirt.DOMAIN_SHUTOFF:
		return StateShutoff, nil
	default:
		return "UNKNOWN", nil
	}
}

func (client *libvirtClient) StartDomain(ctx context.Context, uuid string) error {
	domain, err := client.lookup(ctx, uuid)
	if err != nil {
		return err
	}
	defer domain.Free()
	if err := domain.Create(); err != nil {
		return errors.New("start libvirt Domain failed")
	}
	return nil
}

func (client *libvirtClient) ShutdownDomain(ctx context.Context, uuid string) error {
	domain, err := client.lookup(ctx, uuid)
	if err != nil {
		return err
	}
	defer domain.Free()
	if err := domain.Shutdown(); err != nil {
		return errors.New("shutdown libvirt Domain failed")
	}
	return nil
}

func (client *libvirtClient) lookup(ctx context.Context, uuid string) (*libvirt.Domain, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	domain, err := client.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		return nil, errors.New("lookup libvirt Domain failed")
	}
	return domain, nil
}
