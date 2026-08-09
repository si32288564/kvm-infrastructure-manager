package statemarker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

func TestBackendUsesHashedKIMOwnedPathAndReadBack(t *testing.T) {
	directory := t.TempDir()
	payload := json.RawMessage(`{"value":"ready"}`)
	result, err := (Backend{Directory: directory}).Execute(context.Background(), contract.CommandLease{TargetResourceID: "../../outside", AttemptIndex: 1, CommandPayload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("backend result = %#v", result)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries/error = %d/%v", len(entries), err)
	}
	digest := sha256.Sum256([]byte("../../outside"))
	want := hex.EncodeToString(digest[:]) + ".json"
	if entries[0].Name() != want {
		t.Fatalf("marker filename = %s, want %s", entries[0].Name(), want)
	}
}

func TestBackendRejectsUnknownPayloadField(t *testing.T) {
	_, err := (Backend{Directory: t.TempDir()}).Execute(context.Background(), contract.CommandLease{TargetResourceID: "target", AttemptIndex: 1, CommandPayload: json.RawMessage(`{"value":"ready","path":"/tmp/escape"}`)})
	if err == nil {
		t.Fatal("unknown payload field accepted")
	}
}
