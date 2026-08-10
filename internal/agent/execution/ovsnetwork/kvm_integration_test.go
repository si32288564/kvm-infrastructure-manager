//go:build libvirt && cgo

package ovsnetwork_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/ovsnetwork"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	libvirt "libvirt.org/go/libvirt"
)

func TestDisposableLibvirtOVSPrebootAndDataplaneRealization(t *testing.T) {
	bridge := os.Getenv("KIM_OVS_BRIDGE")
	if bridge == "" {
		t.Skip("OVS qualification is not configured")
	}
	const domainUUID = "88888888-8888-4888-8888-888888888888"
	connection, err := libvirt.NewConnect("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	domain, err := connection.DomainDefineXML(`<domain type='kvm'><name>kim-ovs-qualification-20260810</name><uuid>` + domainUUID + `</uuid><memory unit='MiB'>64</memory><vcpu>1</vcpu><os><type arch='x86_64'>hvm</type></os></domain>`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = domain.Undefine(); _ = domain.Free() }()
	backend, closeBackend, err := ovsnetwork.New("qemu:///system", map[string]string{"qualification-segment": bridge})
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	payload, _ := json.Marshal(map[string]any{"domain_uuid": domainUUID, "vm_generation": 1, "port_id": "qualification-port", "port_generation": 1, "network_id": "qualification-network", "network_generation": 1, "segment_claim_id": "qualification-segment", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:99:00:10", "mtu": 1500, "binding_type": "OVS", "desired_state": "REALIZED"})
	result, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:qualification-port", CommandPayload: payload, AttemptIndex: 1})
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if err := domain.Create(); err != nil {
		t.Fatal(err)
	}
	defer domain.Destroy()
	dataplaneBackend, closeDataplane, err := ovsnetwork.NewDataplane("qemu:///system", map[string]string{"qualification-segment": bridge})
	if err != nil {
		t.Fatal(err)
	}
	defer closeDataplane()
	dataplanePayload, _ := json.Marshal(map[string]any{"domain_uuid": domainUUID, "vm_generation": 1, "port_id": "qualification-port", "port_generation": 1, "network_id": "qualification-network", "network_generation": 1, "segment_claim_id": "qualification-segment", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:99:00:10", "mtu": 1500, "binding_type": "OVS", "desired_state": "CONVERGED"})
	dataplaneResult, err := dataplaneBackend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:qualification-port", CommandPayload: dataplanePayload, AttemptIndex: 1})
	if err != nil || dataplaneResult.Outcome != "SUCCEEDED" || dataplaneResult.Observation.State != "MATCHED" {
		t.Fatalf("dataplane result=%#v err=%v", dataplaneResult, err)
	}
}
