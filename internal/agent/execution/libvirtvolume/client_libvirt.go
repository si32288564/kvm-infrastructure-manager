//go:build libvirt && cgo

package libvirtvolume

import (
	"context"
	"encoding/xml"
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	libvirt "libvirt.org/go/libvirt"
)

type libvirtClient struct{ connection *libvirt.Connect }

func New(uri string, lvmClient locallvm.Client, volumeGroups map[string]string) (*Backend, func() error, error) {
	if uri == "" || lvmClient == nil || len(volumeGroups) == 0 {
		return nil, nil, errors.New("complete libvirt Volume configuration is required")
	}
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	resolver := LocalLVMResolver{Client: lvmClient, VolumeGroups: volumeGroups}
	return &Backend{Domains: &libvirtClient{connection: connection}, Volumes: resolver}, func() error { _, err := connection.Close(); return err }, nil
}

func (client *libvirtClient) Disk(ctx context.Context, domainUUID, target string) (DiskObservation, error) {
	domain, err := client.lookup(ctx, domainUUID)
	if err != nil {
		return DiskObservation{}, err
	}
	defer domain.Free()
	description, err := domain.GetXMLDesc(0)
	if err != nil {
		return DiskObservation{}, errors.New("read libvirt Domain XML failed")
	}
	var current domainDescription
	if err := xml.Unmarshal([]byte(description), &current); err != nil {
		return DiskObservation{}, errors.New("parse libvirt Domain XML failed")
	}
	var observed *domainDisk
	for index := range current.Devices.Disks {
		disk := &current.Devices.Disks[index]
		if disk.Device != "disk" || disk.Target.Device != target {
			continue
		}
		if observed != nil {
			return DiskObservation{}, errors.New("duplicate libvirt disk target")
		}
		observed = disk
	}
	if observed == nil {
		return DiskObservation{Target: target}, nil
	}
	return DiskObservation{Present: true, SourcePath: observed.Source.Device, Target: observed.Target.Device, Serial: observed.Serial, ReadOnly: observed.ReadOnly != nil}, nil
}

func (client *libvirtClient) AttachDisk(ctx context.Context, domainUUID string, disk DiskObservation) error {
	domain, err := client.lookup(ctx, domainUUID)
	if err != nil {
		return err
	}
	defer domain.Free()
	payload, err := diskXML(disk)
	if err != nil {
		return err
	}
	flags, err := deviceModifyFlags(domain)
	if err != nil {
		return err
	}
	if err := domain.AttachDeviceFlags(payload, flags); err != nil {
		return errors.New("attach typed libvirt disk failed")
	}
	return nil
}

func (client *libvirtClient) DetachDisk(ctx context.Context, domainUUID string, disk DiskObservation) error {
	domain, err := client.lookup(ctx, domainUUID)
	if err != nil {
		return err
	}
	defer domain.Free()
	payload, err := diskXML(disk)
	if err != nil {
		return err
	}
	flags, err := deviceModifyFlags(domain)
	if err != nil {
		return err
	}
	if err := domain.DetachDeviceFlags(payload, flags); err != nil {
		return errors.New("detach typed libvirt disk failed")
	}
	return nil
}

func deviceModifyFlags(domain *libvirt.Domain) (libvirt.DomainDeviceModifyFlags, error) {
	active, err := domain.IsActive()
	if err != nil {
		return 0, errors.New("read libvirt Domain activity failed")
	}
	if active {
		return libvirt.DOMAIN_DEVICE_MODIFY_LIVE | libvirt.DOMAIN_DEVICE_MODIFY_CONFIG, nil
	}
	return libvirt.DOMAIN_DEVICE_MODIFY_CONFIG, nil
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

type domainDescription struct {
	Devices struct {
		Disks []domainDisk `xml:"disk"`
	} `xml:"devices"`
}

type domainDisk struct {
	XMLName  xml.Name         `xml:"disk"`
	Type     string           `xml:"type,attr"`
	Device   string           `xml:"device,attr"`
	Driver   domainDiskDriver `xml:"driver"`
	Source   domainDiskSource `xml:"source"`
	Target   domainDiskTarget `xml:"target"`
	Serial   string           `xml:"serial"`
	ReadOnly *struct{}        `xml:"readonly"`
}

type domainDiskDriver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}
type domainDiskSource struct {
	Device string `xml:"dev,attr"`
}
type domainDiskTarget struct {
	Device string `xml:"dev,attr"`
	Bus    string `xml:"bus,attr"`
}

func diskXML(observed DiskObservation) (string, error) {
	if !observed.Present || observed.SourcePath == "" || observed.Target == "" || observed.Serial == "" || observed.ReadOnly {
		return "", errors.New("invalid typed libvirt disk identity")
	}
	payload, err := xml.Marshal(domainDisk{Type: "block", Device: "disk", Driver: domainDiskDriver{Name: "qemu", Type: "raw"}, Source: domainDiskSource{Device: observed.SourcePath}, Target: domainDiskTarget{Device: observed.Target, Bus: "virtio"}, Serial: observed.Serial})
	if err != nil {
		return "", errors.New("encode typed libvirt disk XML failed")
	}
	return string(payload), nil
}
