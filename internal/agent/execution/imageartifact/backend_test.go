package imageartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type sources map[string][]byte

func (s sources) Open(_ context.Context, id string) (io.ReadCloser, error) {
	value, ok := s[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func TestTypedIngestionWholeArtifactAndReadBack(t *testing.T) {
	content := []byte("base-image\x00unique-guest-mutation\x00tail-marker")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	root := t.TempDir()
	backend := Backend{CacheRoot: root, Sources: sources{"approved-source": content}}
	payload, _ := json.Marshal(map[string]any{"image_id": "00000000-0000-4000-8000-000000000001", "image_revision": 1, "artifact_generation": 1, "source_id": "approved-source", "expected_digest": digest, "maximum_bytes": uint64(len(content)), "desired_state": "VERIFIED"})
	lease := contract.CommandLease{TargetResourceID: "image:00000000-0000-4000-8000-000000000001:1", CommandPayload: payload, AttemptIndex: 1}
	result, err := backend.Execute(context.Background(), lease)
	if err != nil || result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("execute=%+v err=%v", result, err)
	}
	stored, err := os.ReadFile(root + "/sha256/" + digest)
	if err != nil || !bytes.Equal(stored, content) {
		t.Fatalf("published artifact mismatch: %v", err)
	}
	observation, err := backend.Observe(context.Background(), contract.VerificationRequest{TargetResourceID: lease.TargetResourceID, CommandPayload: payload, AttemptIndex: 2})
	if err != nil || observation.State != "MATCHED" {
		t.Fatalf("read-back=%+v err=%v", observation, err)
	}
	if strings.Contains(string(payload), root) || strings.Contains(string(payload), "/") {
		t.Fatalf("payload leaked a path: %s", payload)
	}
}

func TestTypedIngestionRejectsMismatchPartialAndArbitraryFields(t *testing.T) {
	content := []byte("partial")
	expected := sha256.Sum256([]byte("different complete artifact"))
	root := t.TempDir()
	backend := Backend{CacheRoot: root, Sources: sources{"approved-source": content}}
	base := map[string]any{"image_id": "00000000-0000-4000-8000-000000000001", "image_revision": 1, "artifact_generation": 1, "source_id": "approved-source", "expected_digest": hex.EncodeToString(expected[:]), "maximum_bytes": uint64(64), "desired_state": "VERIFIED"}
	payload, _ := json.Marshal(base)
	result, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "image:00000000-0000-4000-8000-000000000001:1", CommandPayload: payload, AttemptIndex: 1})
	if err != nil || result.Observation.State != "CONFLICTING" {
		t.Fatalf("mismatch=%+v err=%v", result, err)
	}
	base["source_path"] = "/etc/passwd"
	payload, _ = json.Marshal(base)
	if _, err := backend.Execute(context.Background(), contract.CommandLease{TargetResourceID: "image:00000000-0000-4000-8000-000000000001:1", CommandPayload: payload, AttemptIndex: 2}); err == nil {
		t.Fatal("arbitrary source path accepted")
	}
}
