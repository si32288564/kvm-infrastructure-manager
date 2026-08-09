//go:build libvirt && cgo

package libvirtdomain

import (
	"encoding/json"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	libvirt "libvirt.org/go/libvirt"
)

func TestStandardLibvirtTestDriverPowerStateRoundTrip(t *testing.T) {
	connection, err := libvirt.NewConnect("test:///default")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	domain, err := connection.LookupDomainByName("test")
	if err != nil {
		t.Fatal(err)
	}
	uuid, err := domain.GetUUIDString()
	if err != nil {
		domain.Free()
		t.Fatal(err)
	}
	if err := domain.Destroy(); err != nil {
		domain.Free()
		t.Fatal(err)
	}
	domain.Free()
	backend := Backend{Client: &libvirtClient{connection: connection}}
	payload, _ := json.Marshal(map[string]string{"desired_state": StateRunning})
	lease := contract.CommandLease{TargetResourceID: "vm:" + uuid, CommandPayload: payload, AttemptIndex: 1}
	result, err := backend.Execute(t.Context(), lease)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("standard libvirt round-trip = %#v, %v", result, err)
	}
}
