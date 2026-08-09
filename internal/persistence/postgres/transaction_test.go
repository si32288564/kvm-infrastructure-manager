package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryableTransactionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "serialization", err: &pgconn.PgError{Code: "40001"}, want: true},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, want: true},
		{name: "unique violation", err: &pgconn.PgError{Code: "23505"}, want: false},
		{name: "application error", err: errors.New("rejected"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableTransactionError(test.err); got != test.want {
				t.Fatalf("isRetryableTransactionError() = %v, want %v", got, test.want)
			}
		})
	}
}
