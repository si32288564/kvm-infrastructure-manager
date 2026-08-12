package ovsnetwork

import (
	"context"
	"encoding/json"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"testing"
)

type fakeClient struct{ nic NICObservation }

func (*fakeClient) Bridge(context.Context, string) (string, bool, error)          { return "br-int", true, nil }
func (c *fakeClient) NIC(context.Context, string, string) (NICObservation, error) { return c.nic, nil }
func (c *fakeClient) AttachNIC(_ context.Context, _ string, n NICObservation) error {
	c.nic = n
	return nil
}
func TestClosedTypedOVSRealization(t *testing.T) {
	client := &fakeClient{}
	backend := Backend{client}
	payload, _ := json.Marshal(map[string]any{"domain_uuid": "55555555-5555-4555-8555-555555555555", "vm_generation": 1, "port_id": "port-1", "port_generation": 1, "network_id": "network-1", "network_generation": 1, "segment_claim_id": "segment-1", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:00:00:10", "mtu": 1500, "binding_type": "OVS", "desired_state": "REALIZED"})
	result, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: payload, AttemptIndex: 1})
	if err != nil || result.Outcome != "SUCCEEDED" {
		t.Fatalf("%#v %v", result, err)
	}
	var unsafe map[string]any
	_ = json.Unmarshal(payload, &unsafe)
	unsafe["bridge"] = "br-any"
	payload, _ = json.Marshal(unsafe)
	if _, err = backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: payload}); err == nil {
		t.Fatal("caller bridge accepted")
	}
}

type fakeDataplaneClient struct{ observation DataplaneObservation }

func (f fakeDataplaneClient) Dataplane(context.Context, string, string, string) (DataplaneObservation, error) {
	return f.observation, nil
}

func TestDataplaneRequiresActiveOVSIdentity(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"domain_uuid": "55555555-5555-4555-8555-555555555555", "vm_generation": 1, "port_id": "port-1", "port_generation": 1, "network_id": "network-1", "network_generation": 1, "segment_claim_id": "segment-1", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:00:00:10", "mtu": 1500, "binding_type": "OVS", "desired_state": "CONVERGED"})
	backend := DataplaneBackend{Client: fakeDataplaneClient{DataplaneObservation{DomainRunning: true, InterfacePresent: true, BridgeMatches: true, TargetDevice: "vnet7", Bridge: "br-int", LinkState: "up", InterfaceID: "port-1"}}}
	result, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: payload, AttemptIndex: 1})
	if err != nil || result.Outcome != "SUCCEEDED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	degraded := DataplaneBackend{Client: fakeDataplaneClient{DataplaneObservation{DomainRunning: true, InterfacePresent: true, BridgeMatches: true, TargetDevice: "vnet7", Bridge: "br-int", LinkState: "down", InterfaceID: "port-1"}}}
	result, err = degraded.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: payload, AttemptIndex: 2})
	if err != nil || result.Outcome != "UNKNOWN" || result.Observation.State != "DEGRADED" {
		t.Fatalf("link-down result=%#v err=%v", result, err)
	}
	var unsafe map[string]any
	_ = json.Unmarshal(payload, &unsafe)
	unsafe["bridge"] = "br-any"
	payload, _ = json.Marshal(unsafe)
	if _, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: payload}); err == nil {
		t.Fatal("caller bridge accepted by dataplane backend")
	}
}

func TestDataplaneExactIfaceIDAndSourceQuiescence(t *testing.T) {
	payload := map[string]any{"domain_uuid": "55555555-5555-4555-8555-555555555555", "vm_generation": 1, "port_id": "port-1", "port_generation": 1, "network_id": "network-1", "network_generation": 1, "segment_claim_id": "segment-1", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:00:00:10", "mtu": 1500, "binding_type": "OVS", "desired_state": "CONVERGED"}
	raw, _ := json.Marshal(payload)
	wrong := DataplaneBackend{Client: fakeDataplaneClient{DataplaneObservation{DomainRunning: true, InterfacePresent: true, BridgeMatches: true, TargetDevice: "vnet7", Bridge: "br-int", LinkState: "up", InterfaceID: "foreign-port"}}}
	result, err := wrong.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: raw, AttemptIndex: 1})
	if err != nil || result.Outcome != "UNKNOWN" {
		t.Fatalf("wrong iface-id accepted: result=%#v err=%v", result, err)
	}
	payload["desired_state"] = "QUIESCED"
	raw, _ = json.Marshal(payload)
	quiesced := DataplaneBackend{Client: fakeDataplaneClient{DataplaneObservation{}}}
	result, err = quiesced.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: raw, AttemptIndex: 2})
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("source quiescence not matched: result=%#v err=%v", result, err)
	}
}
