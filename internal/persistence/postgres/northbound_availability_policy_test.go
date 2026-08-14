package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"testing"
)

type availabilityDependencyQuery struct {
	dependent bool
	err       error
}

func (q availabilityDependencyQuery) QueryRow(context.Context, string, ...any) pgx.Row {
	return availabilityDependencyRow(q)
}

type availabilityDependencyRow availabilityDependencyQuery

func (r availabilityDependencyRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*bool)) = r.dependent
	return nil
}

func TestAvailabilityPolicyDependencyConsumesDatabaseBoolean(t *testing.T) {
	db := availabilityDependencyQuery{dependent: true}
	dependent, err := availabilityPolicyDependenciesExist(context.Background(), db, "policy")
	if err != nil || !dependent {
		t.Fatalf("dependency=%v err=%v", dependent, err)
	}
	db.err = pgx.ErrNoRows
	if _, err := availabilityPolicyDependenciesExist(context.Background(), db, "policy"); err == nil {
		t.Fatal("dependency query error ignored")
	}
}
