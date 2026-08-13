//go:build libvirt && cgo

package sriovnetwork

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	libvirt "libvirt.org/go/libvirt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type client struct{ connection *libvirt.Connect }

func New(uri string) (*Backend, func() error, error) {
	if uri == "" {
		return nil, nil, errors.New("libvirt URI is required")
	}
	c, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	return &Backend{Client: &client{c}}, func() error { _, err := c.Close(); return err }, nil
}

func NewRetirement(uri string) (*RetirementBackend, func() error, error) {
	if uri == "" {
		return nil, nil, errors.New("libvirt URI is required")
	}
	c, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	return &RetirementBackend{Client: &client{c}}, func() error { _, err := c.Close(); return err }, nil
}

type domainDescription struct {
	Interfaces []hostdevInterface `xml:"devices>interface"`
}
type hostdevInterface struct {
	XMLName xml.Name `xml:"interface"`
	Type    string   `xml:"type,attr"`
	Managed string   `xml:"managed,attr"`
	MAC     struct {
		Address string `xml:"address,attr"`
	} `xml:"mac"`
	Source struct {
		Address pciAddress `xml:"address"`
	} `xml:"source"`
}
type pciAddress struct {
	Type     string `xml:"type,attr,omitempty"`
	Domain   string `xml:"domain,attr"`
	Bus      string `xml:"bus,attr"`
	Slot     string `xml:"slot,attr"`
	Function string `xml:"function,attr"`
}

func (c *client) HostDevice(ctx context.Context, uuid, address, mac string) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	d, err := c.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		return Observation{}, errors.New("lookup libvirt Domain failed")
	}
	defer d.Free()
	raw, err := d.GetXMLDesc(libvirt.DOMAIN_XML_INACTIVE)
	if err != nil {
		return Observation{}, errors.New("read libvirt Domain XML failed")
	}
	var x domainDescription
	if err := xml.Unmarshal([]byte(raw), &x); err != nil {
		return Observation{}, errors.New("parse libvirt Domain XML failed")
	}
	for _, i := range x.Interfaces {
		if i.Type != "hostdev" {
			continue
		}
		observed, err := formatPCI(i.Source.Address)
		if err != nil {
			continue
		}
		if observed == address {
			return Observation{Present: true, IdentityMatches: i.MAC.Address == mac, DeviceAddress: observed, MAC: i.MAC.Address}, nil
		}
	}
	return Observation{DeviceAddress: address, MAC: mac}, nil
}
func (c *client) AttachHostDevice(ctx context.Context, uuid string, o Observation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	address, err := parsePCI(o.DeviceAddress)
	if err != nil {
		return err
	}
	d, err := c.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		return errors.New("lookup libvirt Domain failed")
	}
	defer d.Free()
	i := hostdevInterface{Type: "hostdev", Managed: "yes"}
	i.MAC.Address = o.MAC
	i.Source.Address = address
	payload, err := xml.Marshal(i)
	if err != nil {
		return err
	}
	if err := d.AttachDeviceFlags(string(payload), libvirt.DOMAIN_DEVICE_MODIFY_CONFIG); err != nil {
		return errors.New("attach typed SR-IOV hostdev failed")
	}
	return nil
}

func (c *client) RetirementState(ctx context.Context, uuid, address, iommuGroup string) (RetirementObservation, error) {
	if err := ctx.Err(); err != nil {
		return RetirementObservation{}, err
	}
	d, err := c.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		return RetirementObservation{}, errors.New("lookup libvirt Domain failed")
	}
	defer d.Free()
	state, _, err := d.GetState()
	if err != nil {
		return RetirementObservation{}, errors.New("read libvirt Domain state failed")
	}
	hostdev, err := c.HostDevice(ctx, uuid, address, "")
	if err != nil {
		return RetirementObservation{}, err
	}
	deviceRoot := filepath.Join("/sys/bus/pci/devices", address)
	driverBound := false
	if _, err := os.Lstat(filepath.Join(deviceRoot, "driver")); err == nil {
		driverBound = true
	} else if !os.IsNotExist(err) {
		return RetirementObservation{}, errors.New("read VF driver binding failed")
	}
	observedGroup := ""
	if target, err := os.Readlink(filepath.Join(deviceRoot, "iommu_group")); err == nil {
		observedGroup = filepath.Base(target)
	} else if !os.IsNotExist(err) {
		return RetirementObservation{}, errors.New("read VF IOMMU group failed")
	}
	holder, err := vfioGroupOpen(observedGroup)
	if err != nil {
		return RetirementObservation{}, err
	}
	holder = holder || driverBound
	return RetirementObservation{DomainRunning: state == libvirt.DOMAIN_RUNNING || state == libvirt.DOMAIN_BLOCKED || state == libvirt.DOMAIN_PAUSED, HostDevicePresent: hostdev.Present, DriverBound: driverBound, HolderPresent: holder, DeviceAddress: address, IOMMUGroup: observedGroup}, nil
}

func vfioGroupOpen(group string) (bool, error) {
	if group == "" {
		return false, nil
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return false, errors.New("read process table for VF holder failed")
	}
	wanted := filepath.Join("/dev/vfio", group)
	for _, process := range processes {
		if !process.IsDir() {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", process.Name(), "fd"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, errors.New("read process descriptors for VF holder failed")
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", process.Name(), "fd", fd.Name()))
			if err == nil && target == wanted {
				return true, nil
			}
		}
	}
	return false, nil
}
func (c *client) DetachHostDevice(ctx context.Context, uuid, address, iommuGroup string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parsed, err := parsePCI(address)
	if err != nil {
		return err
	}
	d, err := c.connection.LookupDomainByUUIDString(uuid)
	if err != nil {
		return errors.New("lookup libvirt Domain failed")
	}
	defer d.Free()
	i := hostdevInterface{Type: "hostdev", Managed: "yes"}
	i.Source.Address = parsed
	payload, err := xml.Marshal(i)
	if err != nil {
		return err
	}
	if err := d.DetachDeviceFlags(string(payload), libvirt.DOMAIN_DEVICE_MODIFY_CONFIG); err != nil {
		return errors.New("detach typed SR-IOV hostdev failed")
	}
	return nil
}
func parsePCI(value string) (pciAddress, error) {
	var domain, bus, slot, function uint64
	if _, err := fmt.Sscanf(value, "%04x:%02x:%02x.%1x", &domain, &bus, &slot, &function); err != nil {
		return pciAddress{}, errors.New("invalid PCI address")
	}
	return pciAddress{Type: "pci", Domain: fmt.Sprintf("0x%04x", domain), Bus: fmt.Sprintf("0x%02x", bus), Slot: fmt.Sprintf("0x%02x", slot), Function: fmt.Sprintf("0x%x", function)}, nil
}
func formatPCI(a pciAddress) (string, error) {
	values := []string{a.Domain, a.Bus, a.Slot, a.Function}
	out := make([]uint64, 4)
	for i, v := range values {
		n, err := strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 16)
		if err != nil {
			return "", err
		}
		out[i] = n
	}
	return fmt.Sprintf("%04x:%02x:%02x.%x", out[0], out[1], out[2], out[3]), nil
}
