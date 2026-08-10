package sriovnetwork

import (
	"context"
	"encoding/json"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"testing"
)

type fakeClient struct{ current Observation }

func (f *fakeClient) HostDevice(context.Context, string, string, string) (Observation, error) {
	return f.current, nil
}
func (f *fakeClient) AttachHostDevice(_ context.Context, _ string, o Observation) error {
	f.current = o
	return nil
}
func TestTypedSRIOVRealization(t *testing.T) {
	f := &fakeClient{}
	b := Backend{Client: f}
	p := map[string]any{"domain_uuid": "44444444-4444-4444-8444-444444444444", "vm_generation": 1, "port_id": "port-1", "port_generation": 1, "network_id": "network-1", "network_generation": 1, "segment_claim_id": "segment-1", "segment_generation": 1, "host_mapping_generation": 1, "binding_generation": 1, "mac_address": "02:00:00:00:00:01", "device_address": "0000:03:00.1", "vf_claim_id": "claim-1", "pci_observation_generation": 1, "pci_observation_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "qualification_id": "qualification-1", "qualification_revision": 1, "policy_id": "policy-1", "policy_generation": 1, "binding_type": "SRIOV_DIRECT", "desired_state": "REALIZED"}
	raw, _ := json.Marshal(p)
	r, err := b.Execute(context.Background(), contract.CommandLease{TargetResourceID: "port:port-1", CommandPayload: raw, AttemptIndex: 1})
	if err != nil || r.Outcome != "SUCCEEDED" {
		t.Fatalf("result=%#v err=%v", r, err)
	}
}
