//go:build libvirt && cgo

package ovsnetwork

import (
	"context"
	"encoding/xml"
	"errors"
	"os/exec"

	libvirt "libvirt.org/go/libvirt"
)

type client struct {
	connection *libvirt.Connect
	bridges    map[string]string
}

func New(uri string, bridges map[string]string) (*Backend, func() error, error) {
	if uri == "" || len(bridges) == 0 {
		return nil, nil, errors.New("complete OVS Network configuration is required")
	}
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, nil, errors.New("connect to libvirt failed")
	}
	return &Backend{Client: &client{connection: connection, bridges: bridges}}, func() error { _, err := connection.Close(); return err }, nil
}
func (c *client) Bridge(ctx context.Context, segment string) (string, bool, error) {
	bridge, ok := c.bridges[segment]
	if !ok || bridge == "" {
		return "", false, errors.New("Segment has no configured OVS bridge")
	}
	if err := exec.CommandContext(ctx, "ovs-vsctl", "br-exists", bridge).Run(); err != nil {
		return bridge, false, nil
	}
	return bridge, true, nil
}
func (c *client) NIC(ctx context.Context, domainUUID, mac string) (NICObservation, error) {
	if err := ctx.Err(); err != nil {
		return NICObservation{}, err
	}
	domain, err := c.connection.LookupDomainByUUIDString(domainUUID)
	if err != nil {
		return NICObservation{}, errors.New("lookup libvirt Domain failed")
	}
	defer domain.Free()
	description, err := domain.GetXMLDesc(libvirt.DOMAIN_XML_INACTIVE)
	if err != nil {
		return NICObservation{}, errors.New("read libvirt Domain XML failed")
	}
	var current domainDescription
	if err := xml.Unmarshal([]byte(description), &current); err != nil {
		return NICObservation{}, errors.New("parse libvirt Domain XML failed")
	}
	var found *domainInterface
	for index := range current.Devices.Interfaces {
		nic := &current.Devices.Interfaces[index]
		if nic.MAC.Address != mac {
			continue
		}
		if found != nil {
			return NICObservation{}, errors.New("duplicate libvirt NIC MAC")
		}
		found = nic
	}
	if found == nil {
		return NICObservation{MAC: mac}, nil
	}
	matches := found.Type == "bridge" && found.VirtualPort.Type == "openvswitch" && found.Model.Type == "virtio"
	return NICObservation{Present: true, IdentityMatches: matches, Bridge: found.Source.Bridge, MAC: found.MAC.Address, Model: found.Model.Type}, nil
}
func (c *client) AttachNIC(ctx context.Context, domainUUID string, nic NICObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	domain, err := c.connection.LookupDomainByUUIDString(domainUUID)
	if err != nil {
		return errors.New("lookup libvirt Domain failed")
	}
	defer domain.Free()
	payload, err := xml.Marshal(domainInterface{Type: "bridge", MAC: macElement{Address: nic.MAC}, Source: sourceElement{Bridge: nic.Bridge}, VirtualPort: virtualPortElement{Type: "openvswitch"}, Model: modelElement{Type: "virtio"}})
	if err != nil {
		return errors.New("encode typed OVS NIC XML failed")
	}
	if err := domain.AttachDeviceFlags(string(payload), libvirt.DOMAIN_DEVICE_MODIFY_CONFIG); err != nil {
		return errors.New("attach typed OVS NIC failed")
	}
	return nil
}

type domainDescription struct {
	Devices struct {
		Interfaces []domainInterface `xml:"interface"`
	} `xml:"devices"`
}
type domainInterface struct {
	XMLName     xml.Name           `xml:"interface"`
	Type        string             `xml:"type,attr"`
	MAC         macElement         `xml:"mac"`
	Source      sourceElement      `xml:"source"`
	VirtualPort virtualPortElement `xml:"virtualport"`
	Model       modelElement       `xml:"model"`
}
type macElement struct {
	Address string `xml:"address,attr"`
}
type sourceElement struct {
	Bridge string `xml:"bridge,attr"`
}
type virtualPortElement struct {
	Type string `xml:"type,attr"`
}
type modelElement struct {
	Type string `xml:"type,attr"`
}
