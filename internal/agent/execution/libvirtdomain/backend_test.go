package libvirtdomain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type fakeClient struct {
	state         string
	starts        int
	shutdowns     int
	holdAfterCall bool
}

func (client *fakeClient) DomainState(context.Context, string) (string, error) {
	return client.state, nil
}
func (client *fakeClient) StartDomain(context.Context, string) error {
	client.starts++
	if !client.holdAfterCall {
		client.state = StateRunning
	}
	return nil
}
func (client *fakeClient) ShutdownDomain(context.Context, string) error {
	client.shutdowns++
	if !client.holdAfterCall {
		client.state = StateShutoff
	}
	return nil
}

func TestBackendExecutesClosedTypedPowerStateAndReadsBack(t *testing.T) {
	client := &fakeClient{state: StateShutoff}
	backend := Backend{Client: client}
	payload, _ := json.Marshal(map[string]string{"desired_state": StateRunning})
	result, err := backend.Execute(t.Context(), contract.CommandLease{TargetResourceID: "vm:11111111-2222-3333-4444-555555555555", CommandPayload: payload, AttemptIndex: 3})
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" || client.starts != 1 {
		t.Fatalf("typed libvirt execution = %#v, %v, starts=%d", result, err, client.starts)
	}
	verification := contract.VerificationRequest{TargetResourceID: "vm:11111111-2222-3333-4444-555555555555", CommandPayload: payload, AttemptIndex: 3}
	observation, err := backend.Observe(t.Context(), verification)
	if err != nil || observation.State != "MATCHED" {
		t.Fatalf("typed libvirt observation = %#v, %v", observation, err)
	}
}

func TestBackendDoesNotPromoteUnconvergedMutation(t *testing.T) {
	client := &fakeClient{state: StateShutoff, holdAfterCall: true}
	backend := Backend{Client: client}
	payload := []byte(`{"desired_state":"RUNNING"}`)
	result, err := backend.Execute(t.Context(), contract.CommandLease{TargetResourceID: "vm:11111111-2222-3333-4444-555555555555", CommandPayload: payload, AttemptIndex: 1})
	if err != nil || result.Outcome != "UNKNOWN" || result.Observation.State != "CONFLICTING" {
		t.Fatalf("unconverged libvirt execution = %#v, %v", result, err)
	}
}

func TestBackendRejectsRawLibvirtSurface(t *testing.T) {
	backend := Backend{Client: &fakeClient{state: StateShutoff}}
	for _, test := range []struct {
		target  string
		payload string
	}{
		{"vm:11111111-2222-3333-4444-555555555555", `{"desired_state":"RUNNING","xml":"<domain/>"}`},
		{"/var/run/libvirt/libvirt-sock", `{"desired_state":"RUNNING"}`},
		{"vm:11111111-2222-3333-4444-555555555555", `{"desired_state":"PAUSED"}`},
	} {
		if _, err := backend.Execute(t.Context(), contract.CommandLease{TargetResourceID: test.target, CommandPayload: []byte(test.payload), AttemptIndex: 1}); err == nil {
			t.Fatalf("unsafe libvirt request accepted: %s %s", test.target, test.payload)
		}
	}
}
