package postgres

import "testing"

func TestRecoveryEligibilityResultFailsClosedByIndependentAuthority(t *testing.T) {
	base := RecoveryEligibilityEvaluation{
		EvaluatedEpochState:          "FENCED",
		Responsibility:               "INFRASTRUCTURE_MANAGED",
		HostFailureAction:            "RESTART_ON_OTHER_HOST",
		FencingUsability:             "USABLE",
		StorageUsability:             "USABLE",
		RecoveryBudgetPolicyID:       "budget",
		RecoveryBudgetPolicyRevision: 1,
		BudgetMaxActive:              1,
		EligibleDestinationCount:     1,
	}
	tests := []struct {
		name, want string
		mutate     func(*RecoveryEligibilityEvaluation)
	}{
		{"eligible", "ELIGIBLE", func(*RecoveryEligibilityEvaluation) {}},
		{"not-confirmed", "EPOCH_NOT_CONFIRMED", func(e *RecoveryEligibilityEvaluation) { e.EvaluatedEpochState = "SUSPECTED" }},
		{"workload-managed", "RESPONSIBILITY_BLOCKED", func(e *RecoveryEligibilityEvaluation) {
			e.Responsibility, e.HostFailureAction = "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION"
		}},
		{"manual", "NO_AUTOMATIC_ACTION", func(e *RecoveryEligibilityEvaluation) {
			e.Responsibility, e.HostFailureAction = "MANUAL", "NO_AUTOMATIC_ACTION"
		}},
		{"fencing-missing", "FENCING_PROOF_MISSING", func(e *RecoveryEligibilityEvaluation) { e.FencingUsability = "MISSING" }},
		{"fencing-stale", "FENCING_PROOF_STALE", func(e *RecoveryEligibilityEvaluation) { e.FencingUsability = "STALE" }},
		{"fencing-unknown", "FENCING_PROOF_UNKNOWN", func(e *RecoveryEligibilityEvaluation) { e.FencingUsability = "UNKNOWN" }},
		{"storage-missing", "STORAGE_PROOF_MISSING", func(e *RecoveryEligibilityEvaluation) { e.StorageUsability = "MISSING" }},
		{"storage-stale", "STORAGE_PROOF_STALE", func(e *RecoveryEligibilityEvaluation) { e.StorageUsability = "STALE" }},
		{"storage-unknown", "STORAGE_PROOF_UNKNOWN", func(e *RecoveryEligibilityEvaluation) { e.StorageUsability = "UNKNOWN" }},
		{"no-budget", "NO_RECOVERY_BUDGET_POLICY", func(e *RecoveryEligibilityEvaluation) { e.RecoveryBudgetPolicyID = "" }},
		{"stale-budget", "STALE_POLICY", func(e *RecoveryEligibilityEvaluation) { e.BudgetMaxActive = 0 }},
		{"budget-exhausted", "BUDGET_EXHAUSTED", func(e *RecoveryEligibilityEvaluation) { e.BudgetActiveCount = 1 }},
		{"no-destination", "NO_DESTINATION", func(e *RecoveryEligibilityEvaluation) { e.EligibleDestinationCount = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := base
			test.mutate(&evaluation)
			recoveryEligibilityResult(&evaluation)
			if evaluation.ResultState != test.want {
				t.Fatalf("result=%s reason=%s want=%s", evaluation.ResultState, evaluation.ReasonCode, test.want)
			}
		})
	}
}
