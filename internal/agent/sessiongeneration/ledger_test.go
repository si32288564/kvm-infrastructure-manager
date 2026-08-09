package sessiongeneration

import (
	"path/filepath"
	"testing"
)

func TestAcceptedGenerationSurvivesRestartAndRejectDoesNotConsume(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "generation")
	ledger, err := Open(directory, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if next, _ := ledger.Next(); next != 1 {
		t.Fatalf("initial proposal = %d", next)
	}
	if next, _ := ledger.Next(); next != 1 {
		t.Fatalf("unaccepted proposal consumed generation: %d", next)
	}
	if err := ledger.CommitAccepted(1); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if next, _ := reopened.Next(); next != 2 {
		t.Fatalf("restart proposal = %d", next)
	}
	if err := reopened.CommitAccepted(3); err == nil {
		t.Fatal("generation gap accepted")
	}
}
