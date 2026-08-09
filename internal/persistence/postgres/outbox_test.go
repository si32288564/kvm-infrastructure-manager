package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v5"
)

func TestClaimOutboxTxRecordsDurableAttempt(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	expires := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("WITH candidates AS").
		WithArgs(2, "publisher-a", int64(time.Minute/time.Microsecond), []string(nil)).
		WillReturnRows(pgxmock.NewRows([]string{
			"message_id", "aggregate_type", "aggregate_id", "event_type", "schema_version",
			"payload_digest", "payload", "claim_generation", "claim_expires_at",
		}).AddRow("msg-1", "Host", "host-1", "HostObserved", "v1", digest64("a"), []byte(`{"host":"host-1"}`), int64(1), expires))
	mock.ExpectExec("INSERT INTO kim.outbox_delivery_attempts").
		WithArgs("msg-1", int64(1), "publisher-a", expires).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO kim.outbox_delivery_events").
		WithArgs("msg-1", int64(1), "CLAIM_GRANTED", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	tx, err := mock.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	messages, err := ClaimOutboxTx(context.Background(), tx, OutboxClaimRequest{
		Owner: "publisher-a",
		Limit: 2,
		Lease: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ClaimGeneration != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMarkOutboxDeliveredRejectsStaleClaim(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE kim.outbox_messages").
		WithArgs("msg-1", "old-publisher", int64(1)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	tx, err := mock.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = MarkOutboxDeliveredTx(context.Background(), tx, OutboxClaim{
		MessageID:       "msg-1",
		Owner:           "old-publisher",
		ClaimGeneration: 1,
	}, map[string]any{"receipt": "late"})
	if !errors.Is(err, ErrStaleOutboxClaim) {
		t.Fatalf("MarkOutboxDeliveredTx error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxEvidenceDigestConflict(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO kim.outbox_delivery_events").
		WithArgs("msg-1", int64(2), "DISPATCH_UNKNOWN", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery("SELECT event_payload_digest").
		WithArgs("msg-1", int64(2), "DISPATCH_UNKNOWN").
		WillReturnRows(pgxmock.NewRows([]string{"event_payload_digest"}).AddRow(digest64("f")))
	tx, err := mock.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = RecordOutboxDispatchUnknownTx(context.Background(), tx, OutboxClaim{
		MessageID:       "msg-1",
		Owner:           "publisher-b",
		ClaimGeneration: 2,
	}, map[string]any{"reason": "response_lost"})
	if !errors.Is(err, ErrOutboxEvidenceConflict) {
		t.Fatalf("RecordOutboxDispatchUnknownTx error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func digest64(character string) string {
	result := ""
	for len(result) < 64 {
		result += character
	}
	return result
}
