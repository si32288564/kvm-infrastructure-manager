// Package imageartifact implements closed, digest-addressed Image ingestion.
// Source and destination paths are administrator configuration, never Command fields.
package imageartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const CommandType = "IMAGE_ARTIFACT_INGEST"
const SchemaVersion = "kim.command.image-artifact-ingest/v1"
const maximumArtifactBytes = uint64(16 * 1024 * 1024 * 1024 * 1024)

var targetPattern = regexp.MustCompile(`^image:([0-9a-f-]{36}):([1-9][0-9]*)$`)

type SourceRegistry interface {
	Open(context.Context, string) (io.ReadCloser, error)
}
type FileRegistry map[string]string

func (r FileRegistry) Open(_ context.Context, id string) (io.ReadCloser, error) {
	path, ok := r[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return os.Open(path)
}

type Backend struct {
	CacheRoot string
	Sources   SourceRegistry
}

func (Backend) CommandType() string    { return CommandType }
func (Backend) SchemaVersion() string  { return SchemaVersion }
func (Backend) Capabilities() []string { return []string{"kim.image-artifact-ingest/v1"} }

type request struct {
	ImageID            string `json:"image_id"`
	ImageRevision      uint64 `json:"image_revision"`
	ArtifactGeneration uint64 `json:"artifact_generation"`
	SourceID           string `json:"source_id"`
	ExpectedDigest     string `json:"expected_digest"`
	MaximumBytes       uint64 `json:"maximum_bytes"`
	DesiredState       string `json:"desired_state"`
}

func (b Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	r, err := b.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	matched, size, digest, err := b.readBack(ctx, r)
	if err == nil && matched {
		return result(r, size, digest, lease.AttemptIndex, "MATCHED"), nil
	}
	source, err := b.Sources.Open(ctx, r.SourceID)
	if err != nil {
		return agentexecution.BackendResult{}, errors.New("trusted Image source unavailable")
	}
	defer source.Close()
	root, err := os.OpenRoot(b.CacheRoot)
	if err != nil {
		return agentexecution.BackendResult{}, errors.New("open Image artifact root failed")
	}
	defer root.Close()
	if err := root.MkdirAll("staging", 0700); err != nil {
		return agentexecution.BackendResult{}, err
	}
	if err := root.MkdirAll("sha256", 0700); err != nil {
		return agentexecution.BackendResult{}, err
	}
	stageName := fmt.Sprintf("staging/%s-%d-%d", r.ImageID, r.ImageRevision, r.ArtifactGeneration)
	stage, err := root.OpenFile(stageName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		_ = root.Remove(stageName)
		stage, err = root.OpenFile(stageName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if err != nil {
		return agentexecution.BackendResult{}, errors.New("create bounded staging artifact failed")
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(stage, hash), io.LimitReader(source, int64(r.MaximumBytes)+1))
	syncErr := stage.Sync()
	closeErr := stage.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || n < 0 || uint64(n) > r.MaximumBytes {
		_ = root.Remove(stageName)
		return agentexecution.BackendResult{}, errors.New("bounded Image transfer failed")
	}
	observed := hex.EncodeToString(hash.Sum(nil))
	if observed != r.ExpectedDigest {
		_ = root.Remove(stageName)
		return result(r, uint64(n), observed, lease.AttemptIndex, "CONFLICTING"), nil
	}
	finalName := "sha256/" + r.ExpectedDigest
	if err := root.Rename(stageName, finalName); err != nil {
		matched, size, digest, readErr := b.readBack(ctx, r)
		if readErr != nil || !matched {
			return agentexecution.BackendResult{}, errors.New("atomic Image artifact publication failed")
		}
		return result(r, size, digest, lease.AttemptIndex, "MATCHED"), nil
	}
	return result(r, uint64(n), observed, lease.AttemptIndex, "MATCHED"), nil
}
func (b Backend) Observe(ctx context.Context, v contract.VerificationRequest) (contract.Observation, error) {
	r, err := b.decode(v.TargetResourceID, v.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	matched, size, digest, err := b.readBack(ctx, r)
	if err != nil {
		return contract.Observation{State: "UNKNOWN", Generation: int64(v.AttemptIndex), Digest: sha("absent"), Evidence: map[string]any{"read_back_state": "ABSENT"}}, nil
	}
	state := "CONFLICTING"
	if matched {
		state = "MATCHED"
	}
	return result(r, size, digest, v.AttemptIndex, state).Observation, nil
}
func (b Backend) decode(target string, payload []byte) (request, error) {
	if b.CacheRoot == "" || b.Sources == nil {
		return request{}, errors.New("Image artifact backend configuration required")
	}
	match := targetPattern.FindStringSubmatch(target)
	if match == nil {
		return request{}, errors.New("invalid Image target identity")
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	var r request
	if d.Decode(&r) != nil {
		return request{}, errors.New("invalid typed Image ingestion payload")
	}
	var trailing any
	if !errors.Is(d.Decode(&trailing), io.EOF) {
		return request{}, errors.New("trailing Image ingestion payload")
	}
	if r.ImageID != match[1] || r.ImageRevision == 0 || r.ArtifactGeneration == 0 || r.SourceID == "" || len(r.SourceID) > 128 || len(r.ExpectedDigest) != 64 || r.MaximumBytes == 0 || r.MaximumBytes > maximumArtifactBytes || r.DesiredState != "VERIFIED" {
		return request{}, errors.New("incomplete typed Image ingestion authority")
	}
	if _, err := hex.DecodeString(r.ExpectedDigest); err != nil {
		return request{}, errors.New("invalid expected Image digest")
	}
	return r, nil
}
func (b Backend) readBack(ctx context.Context, r request) (bool, uint64, string, error) {
	root, err := os.OpenRoot(b.CacheRoot)
	if err != nil {
		return false, 0, "", err
	}
	defer root.Close()
	f, err := root.Open("sha256/" + r.ExpectedDigest)
	if err != nil {
		return false, 0, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || uint64(info.Size()) > r.MaximumBytes {
		return false, 0, "", errors.New("conflicting Image artifact")
	}
	h := sha256.New()
	n, err := io.Copy(h, &contextReader{ctx: ctx, r: f})
	if err != nil {
		return false, 0, "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	return digest == r.ExpectedDigest, uint64(n), digest, nil
}
func result(r request, size uint64, digest string, generation int, state string) agentexecution.BackendResult {
	e := map[string]any{"image_id": r.ImageID, "image_revision": r.ImageRevision, "artifact_generation": r.ArtifactGeneration, "source_id": r.SourceID, "observed_size_bytes": size, "digest_algorithm": "SHA256", "observed_digest": digest, "read_back_state": map[bool]string{true: "COMPLETE", false: "CONFLICTING"}[state == "MATCHED"]}
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	outcome := "UNKNOWN"
	if state == "MATCHED" {
		outcome = "SUCCEEDED"
	}
	return agentexecution.BackendResult{Outcome: outcome, Result: e, Observation: contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(sum[:]), Evidence: e}}
}
func sha(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
