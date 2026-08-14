package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

type flavorDependencyQuery struct{ dependent bool }

func (q flavorDependencyQuery) QueryRow(context.Context, string, ...any) pgx.Row {
	return flavorDependencyRow{dependent: q.dependent}
}

type flavorDependencyRow struct{ dependent bool }

func (r flavorDependencyRow) Scan(dest ...any) error { *(dest[0].(*bool)) = r.dependent; return nil }

func TestFlavorDependencyGuardConsumesHistoricalPlacementReferences(t *testing.T) {
	for _, dependent := range []bool{false, true} {
		got, err := flavorDependenciesExist(context.Background(), flavorDependencyQuery{dependent: dependent}, "flavor")
		if err != nil || got != dependent {
			t.Fatalf("dependent=%t got=%t err=%v", dependent, got, err)
		}
	}
}
