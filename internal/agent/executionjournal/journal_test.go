package executionjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestJournalWriteBeforeExecuteSurvivesRestartAndFencesConflicts(t *testing.T) {
	directory := t.TempDir()
	commandDigest := testDigest("command")
	journal, err := Open(directory, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	startedDigest, err := journal.Prepare("command-1", 1, commandDigest, "vm-1")
	if err != nil || len(startedDigest) != 64 {
		t.Fatalf("Prepare digest/error = %s/%v", startedDigest, err)
	}
	if _, err := journal.Prepare("command-1", 1, testDigest("different"), "vm-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Command digest error = %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	journal, err = Open(directory, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	records, err := journal.Records()
	if err != nil || len(records) != 1 || records[0].State != "STARTED" {
		t.Fatalf("recovered records/error = %#v/%v", records, err)
	}
	resultDigest := testDigest("result")
	completedDigest, err := journal.Complete("command-1", 1, resultDigest, "SUCCEEDED")
	if err != nil || completedDigest == startedDigest {
		t.Fatalf("Complete digest/error = %s/%v", completedDigest, err)
	}
	if duplicate, err := journal.Complete("command-1", 1, resultDigest, "SUCCEEDED"); err != nil || duplicate != completedDigest {
		t.Fatalf("idempotent Complete digest/error = %s/%v", duplicate, err)
	}
	if _, err := journal.Complete("command-1", 1, testDigest("conflict"), "SUCCEEDED"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Result error = %v", err)
	}
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
