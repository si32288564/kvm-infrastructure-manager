//go:build libvirt && cgo

package libvirtvm

import (
	"context"
	"encoding/xml"
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	libvirt "libvirt.org/go/libvirt"
)

const metadataNamespace = "urn:kvm-infrastructure-manager:vm-materialization:v1"

type libvirtClient struct{ connection *libvirt.Connect }

func New(uri string, resolver libvirtvolume.VolumeResolver) (*Backend, func() error, error) {
	if uri == "" || resolver == nil {
		return nil, nil, errors.New("complete libvirt VM configuration is required")
	}
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	return &Backend{Domains: &libvirtClient{connection: connection}, Volumes: resolver}, func() error { _, err := connection.Close(); return err }, nil
}

func NewCleanup(uri string) (*CleanupBackend, func() error, error) {
	if uri == "" {
		return nil, nil, errors.New("libvirt URI is required")
	}
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	return &CleanupBackend{Client: &libvirtClient{connection: connection}}, func() error { _, err := connection.Close(); return err }, nil
}

func (client *libvirtClient) DomainCleanupState(ctx context.Context, uuid string) (CleanupObservation, error) {
	if err := ctx.Err(); err != nil {
		return CleanupObservation{}, err
	}
	domain, err := client.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		var libvirtError libvirt.Error
		if errors.As(err, &libvirtError) && libvirtError.Code == libvirt.ERR_NO_DOMAIN {
			return CleanupObservation{UUID: uuid}, nil
		}
		return CleanupObservation{}, errors.New("lookup libvirt Domain for cleanup failed")
	}
	defer domain.Free()
	state, _, err := domain.GetState()
	if err != nil {
		return CleanupObservation{}, errors.New("read libvirt Domain cleanup state failed")
	}
	description, err := domain.GetXMLDesc(libvirt.DOMAIN_XML_INACTIVE)
	if err != nil {
		return CleanupObservation{}, errors.New("read inactive libvirt Domain cleanup XML failed")
	}
	var parsed domainXML
	if err := xml.Unmarshal([]byte(description), &parsed); err != nil {
		return CleanupObservation{}, errors.New("parse libvirt Domain cleanup XML failed")
	}
	running := state == libvirt.DOMAIN_RUNNING || state == libvirt.DOMAIN_BLOCKED
	return CleanupObservation{Present: true, Running: running, UUID: parsed.UUID, PlanDigest: parsed.Metadata.Materialization.PlanDigest, MaterializationGeneration: parsed.Metadata.Materialization.Generation}, nil
}

func (client *libvirtClient) UndefineDomain(ctx context.Context, uuid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	domain, err := client.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		return errors.New("lookup libvirt Domain for undefine failed")
	}
	defer domain.Free()
	state, _, err := domain.GetState()
	if err != nil || state != libvirt.DOMAIN_SHUTOFF {
		return errors.New("refuse to undefine non-SHUTOFF libvirt Domain")
	}
	if err := domain.Undefine(); err != nil {
		return errors.New("undefine typed libvirt Domain failed")
	}
	return nil
}

func (client *libvirtClient) Domain(ctx context.Context, uuid string) (DomainObservation, error) {
	if err := ctx.Err(); err != nil {
		return DomainObservation{}, err
	}
	domain, err := client.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		var libvirtError libvirt.Error
		if errors.As(err, &libvirtError) && libvirtError.Code == libvirt.ERR_NO_DOMAIN {
			return DomainObservation{}, nil
		}
		return DomainObservation{}, errors.New("lookup libvirt Domain failed")
	}
	defer domain.Free()
	description, err := domain.GetXMLDesc(libvirt.DOMAIN_XML_INACTIVE)
	if err != nil {
		return DomainObservation{}, errors.New("read inactive libvirt Domain XML failed")
	}
	var parsed domainXML
	if err := xml.Unmarshal([]byte(description), &parsed); err != nil {
		return DomainObservation{}, errors.New("parse libvirt Domain XML failed")
	}
	if parsed.UUID != uuid || len(parsed.Devices.Disks) != 1 {
		return DomainObservation{Present: true, UUID: parsed.UUID, Name: parsed.Name}, nil
	}
	disk := parsed.Devices.Disks[0]
	return DomainObservation{Present: true, UUID: parsed.UUID, Name: parsed.Name,
		PlanDigest:                parsed.Metadata.Materialization.PlanDigest,
		MaterializationGeneration: parsed.Metadata.Materialization.Generation,
		VCPUs:                     parsed.VCPU, MemoryMiB: parsed.Memory.Value / 1024,
		RootSource: disk.Source.Device, RootTarget: disk.Target.Device, RootSerial: disk.Serial}, nil
}

func (client *libvirtClient) Define(ctx context.Context, spec DomainSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !spec.Present || spec.UUID == "" || spec.Name == "" || spec.PlanDigest == "" || spec.MaterializationGeneration == 0 || spec.VCPUs == 0 || spec.MemoryMiB == 0 || spec.RootSource == "" || spec.RootTarget != "vda" || spec.RootSerial == "" {
		return errors.New("invalid typed libvirt Domain specification")
	}
	desired := domainXML{Type: "kvm", Name: spec.Name, UUID: spec.UUID,
		Memory: domainMemory{Unit: "KiB", Value: spec.MemoryMiB * 1024}, VCPU: spec.VCPUs,
		OS:       domainOS{Type: domainOSType{Value: "hvm"}},
		Features: domainFeatures{ACPI: &struct{}{}},
		Metadata: domainMetadata{Materialization: domainMaterialization{XMLNS: metadataNamespace, PlanDigest: spec.PlanDigest, Generation: spec.MaterializationGeneration}},
		Devices: domainDevices{Disks: []domainDisk{{Type: "block", Device: "disk",
			Driver: domainDiskDriver{Name: "qemu", Type: "raw"}, Source: domainDiskSource{Device: spec.RootSource},
			Target: domainDiskTarget{Device: spec.RootTarget, Bus: "virtio"}, Serial: spec.RootSerial}},
			Serial:  domainSerial{Type: "pty", Target: domainSerialTarget{Port: 0}},
			Console: domainConsole{Type: "pty", Target: domainConsoleTarget{Type: "serial", Port: 0}}},
		OnPoweroff: "destroy", OnReboot: "restart", OnCrash: "destroy"}
	payload, err := xml.Marshal(desired)
	if err != nil {
		return errors.New("encode typed libvirt Domain XML failed")
	}
	domain, err := client.connection.DomainDefineXML(string(payload))
	if err != nil {
		return errors.New("define typed libvirt Domain failed")
	}
	return domain.Free()
}

type domainXML struct {
	XMLName    xml.Name       `xml:"domain"`
	Type       string         `xml:"type,attr,omitempty"`
	Name       string         `xml:"name"`
	UUID       string         `xml:"uuid"`
	Memory     domainMemory   `xml:"memory"`
	VCPU       uint64         `xml:"vcpu"`
	OS         domainOS       `xml:"os"`
	Features   domainFeatures `xml:"features"`
	Metadata   domainMetadata `xml:"metadata"`
	Devices    domainDevices  `xml:"devices"`
	OnPoweroff string         `xml:"on_poweroff,omitempty"`
	OnReboot   string         `xml:"on_reboot,omitempty"`
	OnCrash    string         `xml:"on_crash,omitempty"`
}
type domainFeatures struct {
	ACPI *struct{} `xml:"acpi"`
}
type domainMemory struct {
	Unit  string `xml:"unit,attr"`
	Value uint64 `xml:",chardata"`
}
type domainOS struct {
	Type domainOSType `xml:"type"`
}
type domainOSType struct {
	Value string `xml:",chardata"`
}
type domainMetadata struct {
	Materialization domainMaterialization `xml:"materialization"`
}
type domainMaterialization struct {
	XMLNS      string `xml:"xmlns,attr,omitempty"`
	PlanDigest string `xml:"plan-digest,attr"`
	Generation uint64 `xml:"generation,attr"`
}
type domainDevices struct {
	Disks   []domainDisk  `xml:"disk"`
	Serial  domainSerial  `xml:"serial"`
	Console domainConsole `xml:"console"`
}
type domainSerial struct {
	Type   string             `xml:"type,attr"`
	Target domainSerialTarget `xml:"target"`
}
type domainSerialTarget struct {
	Port uint `xml:"port,attr"`
}
type domainConsole struct {
	Type   string              `xml:"type,attr"`
	Target domainConsoleTarget `xml:"target"`
}
type domainConsoleTarget struct {
	Type string `xml:"type,attr"`
	Port uint   `xml:"port,attr"`
}
type domainDisk struct {
	Type   string           `xml:"type,attr"`
	Device string           `xml:"device,attr"`
	Driver domainDiskDriver `xml:"driver"`
	Source domainDiskSource `xml:"source"`
	Target domainDiskTarget `xml:"target"`
	Serial string           `xml:"serial"`
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
