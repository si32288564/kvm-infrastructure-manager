package postgres

import (
	"testing"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestEvaluateHostGroupSelectorExpression(t *testing.T) {
	snapshot := agentinventory.Snapshot{
		HostIdentity: "host-a",
		Capabilities: []agentinventory.Capability{
			{Name: "kim.host.cpu-topology.v1", State: agentinventory.AvailabilityAvailable},
			{Name: "kim.host.sriov-observation.v1", State: agentinventory.AvailabilityUnavailable},
			{Name: "kim.host.iommu-observation.v1", State: agentinventory.AvailabilityUnsupported},
		},
		Fragments: []agentinventory.Fragment{{Compute: &agentinventory.Compute{Architecture: "x86_64"}}},
	}
	tests := []struct {
		name       string
		expression HostGroupSelectorExpression
		state      string
	}{
		{name: "match", expression: selectorExpression(
			HostGroupSelectorPredicate{Field: "HOST_ID", Operator: "EQUALS", Value: "host-a"},
			HostGroupSelectorPredicate{Field: "COMPUTE_ARCHITECTURE", Operator: "EQUALS", Value: "x86_64"},
		), state: "MATCHED"},
		{name: "not matched", expression: selectorExpression(
			HostGroupSelectorPredicate{Field: "HOST_ID", Operator: "EQUALS", Value: "host-b"},
		), state: "NOT_MATCHED"},
		{name: "unknown", expression: selectorExpression(
			HostGroupSelectorPredicate{Field: "CAPABILITY_STATE", Key: "kim.host.hugepages.v1", Operator: "EQUALS", Value: "AVAILABLE"},
		), state: "UNKNOWN"},
		{name: "unsupported", expression: selectorExpression(
			HostGroupSelectorPredicate{Field: "CAPABILITY_STATE", Key: "kim.host.iommu-observation.v1", Operator: "EQUALS", Value: "AVAILABLE"},
		), state: "UNSUPPORTED"},
		{name: "known unavailable", expression: selectorExpression(
			HostGroupSelectorPredicate{Field: "CAPABILITY_STATE", Key: "kim.host.sriov-observation.v1", Operator: "EQUALS", Value: "UNAVAILABLE"},
		), state: "MATCHED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, _ := evaluateHostGroupSelectorExpression(test.expression, snapshot)
			if state != test.state {
				t.Fatalf("state = %s, want %s", state, test.state)
			}
		})
	}
}

func TestNormalizeHostGroupSelectorRevisionRejectsOpenLanguage(t *testing.T) {
	base := HostGroupSelectorRevision{
		SelectorID: "selector-a", HostGroupID: "group-a", Generation: 1,
		BasedOnHostGroupGeneration: 1, SchemaVersion: HostGroupSelectorSchemaV1,
		EvaluatorArtifactDigest: digestHostGroupFields("evaluator"), LifecycleState: "ACTIVE",
		Expression: selectorExpression(HostGroupSelectorPredicate{
			Field: "HOST_ID", Operator: "EQUALS", Value: "host-a",
		}),
	}
	if _, _, _, _, err := normalizeHostGroupSelectorRevision(base); err != nil {
		t.Fatalf("valid selector: %v", err)
	}
	for _, predicate := range []HostGroupSelectorPredicate{
		{Field: "SQL", Operator: "EQUALS", Value: "SELECT true"},
		{Field: "HOST_ID", Operator: "REGEX", Value: ".*"},
		{Field: "CAPABILITY_STATE", Key: "kim.host.unapproved.v1", Operator: "EQUALS", Value: "AVAILABLE"},
		{Field: "COMPUTE_ARCHITECTURE", Operator: "EQUALS", Value: "caller-defined"},
	} {
		revision := base
		revision.Expression = selectorExpression(predicate)
		if _, _, _, _, err := normalizeHostGroupSelectorRevision(revision); err == nil {
			t.Fatalf("open selector predicate accepted: %#v", predicate)
		}
	}
}

func TestEvaluateArchitectureUnavailableFailsClosed(t *testing.T) {
	snapshot := agentinventory.Snapshot{
		HostIdentity: "host-a",
		Capabilities: []agentinventory.Capability{{
			Name: "kim.host.cpu-topology.v1", State: agentinventory.AvailabilityUnavailable,
		}},
		Fragments: []agentinventory.Fragment{{Compute: &agentinventory.Compute{}}},
	}
	state, reason := evaluateHostGroupSelectorExpression(selectorExpression(
		HostGroupSelectorPredicate{Field: "COMPUTE_ARCHITECTURE", Operator: "EQUALS", Value: "x86_64"},
	), snapshot)
	if state != "UNKNOWN" || reason != "architecture_unavailable" {
		t.Fatalf("unavailable architecture = %s/%s", state, reason)
	}
}

func selectorExpression(predicates ...HostGroupSelectorPredicate) HostGroupSelectorExpression {
	return HostGroupSelectorExpression{MatchAll: predicates, UnknownPolicy: "FAIL_CLOSED"}
}
