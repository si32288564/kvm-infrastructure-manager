package spool

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

func TestDurableEnvelopeSurvivesRestartAndCurrentSessionRebind(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "spool")
	first := openTestSpool(t, directory, 4)
	envelope := testEnvelope(1, "result-1", []byte("result"))
	if err := first.Enqueue(envelope); err != nil {
		t.Fatal(err)
	}
	digestBefore, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestSpool(t, directory, 4)
	defer reopened.Close()
	pending, err := reopened.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	bound := pending[0].BindSession(2)
	if bound.SessionGeneration != 2 || bound.MessageID != envelope.MessageID || bound.PayloadDigest != envelope.PayloadDigest {
		t.Fatalf("bound envelope = %#v", bound)
	}
	digestAfter, _ := reopened.Digest()
	if digestAfter != digestBefore {
		t.Fatalf("digest after restart=%s, want %s", digestAfter, digestBefore)
	}
	receipt := session.Receipt{HostIdentity: envelope.HostIdentity, AcceptedSessionGeneration: 1, Stream: envelope.Stream, MessageID: envelope.MessageID, SequenceScope: envelope.SequenceScope, Sequence: envelope.Sequence, PayloadDigest: envelope.PayloadDigest, Disposition: "ACCEPTED"}
	if err := reopened.Acknowledge(receipt); err != nil {
		t.Fatal(err)
	}
	if stats := reopened.Stats(); stats.QueuedEntries != 0 {
		t.Fatalf("queued entries=%d", stats.QueuedEntries)
	}
}

func TestNonReleasingReceiptRetainsEntryWithoutFailingSessionHandler(t *testing.T) {
	journal := openTestSpool(t, filepath.Join(t.TempDir(), "spool"), 4)
	defer journal.Close()
	envelope := testEnvelope(1, "result-stale", []byte("result"))
	if err := journal.Enqueue(envelope); err != nil {
		t.Fatal(err)
	}
	receipt := session.Receipt{HostIdentity: envelope.HostIdentity, AcceptedSessionGeneration: 2, Stream: envelope.Stream, MessageID: envelope.MessageID, SequenceScope: envelope.SequenceScope, Sequence: envelope.Sequence, PayloadDigest: envelope.PayloadDigest, Disposition: "STALE"}
	if err := journal.HandleReceipt(t.Context(), receipt); err != nil {
		t.Fatalf("durable STALE Receipt ended transport session: %v", err)
	}
	if stats := journal.Stats(); stats.QueuedEntries != 1 {
		t.Fatalf("STALE Receipt released durable evidence: %#v", stats)
	}
}

func TestSpoolRejectsDigestConflictAndCapacityOverflow(t *testing.T) {
	spool := openTestSpool(t, filepath.Join(t.TempDir(), "spool"), 1)
	defer spool.Close()
	first := testEnvelope(1, "result-1", []byte("first"))
	if err := spool.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	conflict := testEnvelope(1, "result-1", []byte("different"))
	if err := spool.Enqueue(conflict); !errors.Is(err, ErrMessageConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if err := spool.Enqueue(testEnvelope(2, "result-2", []byte("second"))); !errors.Is(err, ErrFull) {
		t.Fatalf("full error=%v", err)
	}
}

func openTestSpool(t *testing.T, directory string, maxEntries int) *Spool {
	t.Helper()
	spool, err := Open(Config{Directory: directory, HostIdentity: "host-1", MaxEntries: maxEntries, MaxBytes: 1024 * 1024, MaxMessageBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return spool
}

func testEnvelope(sequence uint64, messageID string, payload []byte) session.Envelope {
	return session.NewEnvelope("host-1", 1, session.StreamResult, messageID, "v1", "attempt-1", sequence, payload)
}
