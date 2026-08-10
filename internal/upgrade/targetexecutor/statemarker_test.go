package targetexecutor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateMarkerApplyAndReadBack(t *testing.T) {
	directory := t.TempDir()
	backend, err := NewStateMarkerBackend(directory)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{TargetID: "target-1", ComponentType: "API", ComponentID: "api-1",
		TargetArtifactDigest: strings.Repeat("a", 64), TargetDigest: strings.Repeat("b", 64)}
	before, err := backend.Observe(context.Background(), target)
	if err != nil || before.State != "ABSENT" {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	if err := backend.Apply(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := backend.Apply(context.Background(), target); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	after, err := backend.Observe(context.Background(), target)
	if err != nil || after.State != "MATCHED" {
		t.Fatalf("after=%+v err=%v", after, err)
	}
	if err := os.WriteFile(MarkerPath(directory, target.TargetID), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict, err := backend.Observe(context.Background(), target)
	if err != nil || conflict.State != "CONFLICTING" {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	if filepath.Base(MarkerPath(directory, target.TargetID)) == target.TargetID+".json" {
		t.Fatal("caller target identity was used as a path")
	}
}
