//go:build libvirt && cgo

package sriovnetwork

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	libvirt "libvirt.org/go/libvirt"
)

func TestLibvirtTestDriverSRIOVPrebootRealization(t *testing.T) {
	const uri = "test:///default"
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	uuid := "99999999-9999-4999-8999-999999999999"
	domain, err := connection.DomainDefineXML(fmt.Sprintf(`<domain type='test'><name>kim-sriov-contract</name><uuid>%s</uuid><memory unit='MiB'>64</memory><vcpu>1</vcpu><os><type>hvm</type></os><devices><interface type='hostdev' managed='yes'><mac address='02:00:00:00:99:99'/><source><address type='pci' domain='0x0000' bus='0x03' slot='0x00' function='0x1'/></source></interface></devices></domain>`, uuid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = domain.Undefine(); _ = domain.Free() }()
	backend, closeBackend, err := New(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	payload, _ := json.Marshal(map[string]any{"domain_uuid": uuid, "vm_generation": 1, "port_id": "qualification-port", "port_generation": 1, "network_id": "qualification-network", "network_generation": 1, "segment_claim_id": "qualification-segment", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:00:99:99", "device_address": "0000:03:00.1", "vf_claim_id": "qualification-claim", "pci_observation_generation": 1, "pci_observation_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "qualification_id": "qualification", "qualification_revision": 1, "policy_id": "qualification-policy", "policy_generation": 1, "binding_type": "SRIOV_DIRECT", "desired_state": "REALIZED"})
	observation, err := backend.Observe(context.Background(), contract.VerificationRequest{TargetResourceID: "port:qualification-port", CommandPayload: payload, AttemptIndex: 1})
	if err != nil || observation.State != "MATCHED" {
		t.Fatalf("observation=%#v err=%v", observation, err)
	}
}
