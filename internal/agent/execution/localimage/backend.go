// Package localimage implements closed typed Image materialization from an
// admin-configured, digest-addressed cache into an identity-verified Local LVM
// root Volume. Command callers cannot supply a URI, source path, target path,
// offset, executable, argv, or copy flag.
package localimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	CommandType     = "LOCAL_IMAGE_TO_LVM_ENSURE"
	SchemaVersion   = "kim.command.local-image-to-lvm/v1"
	StateRealized   = "REALIZED"
	maxArtifactSize = uint64(16 * 1024 * 1024 * 1024 * 1024)
)

var vmTargetPattern = regexp.MustCompile(`^vm:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type Backend struct {
	CacheRoot string
	Volumes   libvirtvolume.VolumeResolver
}

func (Backend) CommandType() string   { return CommandType }
func (Backend) SchemaVersion() string { return SchemaVersion }

type request struct {
	DomainUUID                string `json:"domain_uuid"`
	MaterializationGeneration uint64 `json:"materialization_generation"`
	ImageID                   string `json:"image_id"`
	ImageRevision             uint64 `json:"image_revision"`
	ImageChecksum             string `json:"image_checksum"`
	ImageSizeBytes            uint64 `json:"image_size_bytes"`
	VolumeID                  string `json:"volume_id"`
	VGUUID                    string `json:"vg_uuid"`
	LVUUID                    string `json:"lv_uuid"`
	BackendResourceKey        string `json:"backend_resource_key"`
	DesiredState              string `json:"desired_state"`
}

type decoded struct {
	request request
	volume  locallvm.LogicalVolume
	target  string
}

func (backend Backend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	decoded, err := backend.decode(ctx, lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	matched, err := backend.targetMatches(ctx, decoded)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if !matched {
		if err := backend.copyArtifact(ctx, decoded); err != nil {
			return agentexecution.BackendResult{}, err
		}
	}
	return backend.observe(ctx, decoded, lease.AttemptIndex)
}

func (backend Backend) Observe(ctx context.Context, verification contract.VerificationRequest) (contract.Observation, error) {
	decoded, err := backend.decode(ctx, verification.TargetResourceID, verification.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	result, err := backend.observe(ctx, decoded, verification.AttemptIndex)
	return result.Observation, err
}

func (backend Backend) observe(ctx context.Context, decoded decoded, generation int) (agentexecution.BackendResult, error) {
	matched, err := backend.targetMatches(ctx, decoded)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	state, outcome := "CONFLICTING", "UNKNOWN"
	if matched {
		state, outcome = "MATCHED", "SUCCEEDED"
	}
	evidence := map[string]any{
		"domain_uuid": decoded.request.DomainUUID, "materialization_generation": decoded.request.MaterializationGeneration,
		"image_id": decoded.request.ImageID, "image_revision": decoded.request.ImageRevision,
		"expected_content_digest": decoded.request.ImageChecksum,
		"observed_content_digest": func() string {
			if matched {
				return decoded.request.ImageChecksum
			}
			return ""
		}(),
		"image_size_bytes": decoded.request.ImageSizeBytes, "volume_id": decoded.request.VolumeID,
		"observed_vg_uuid": decoded.volume.VGUUID, "observed_lv_uuid": decoded.volume.LVUUID,
		"backend_resource_key": decoded.request.BackendResourceKey,
		"holder_open":          decoded.volume.DeviceOpen, "content_identity_matches": matched,
		"source": "digest_addressed_cache+local_lvm_content_readback",
	}
	encoded, _ := json.Marshal(evidence)
	digest := sha256.Sum256(encoded)
	observation := contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}
	return agentexecution.BackendResult{Outcome: outcome, Result: evidence, Observation: observation}, nil
}

func (backend Backend) decode(ctx context.Context, target string, payload []byte) (decoded, error) {
	match := vmTargetPattern.FindStringSubmatch(target)
	if match == nil || backend.CacheRoot == "" || backend.Volumes == nil {
		return decoded{}, errors.New("complete typed Image materialization authority is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var desired request
	if err := decoder.Decode(&desired); err != nil {
		return decoded{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return decoded{}, errors.New("trailing Image materialization payload")
	}
	if desired.DomainUUID != match[1] || desired.MaterializationGeneration == 0 || desired.ImageID == "" || desired.ImageRevision == 0 || len(desired.ImageChecksum) != 64 || desired.ImageSizeBytes == 0 || desired.ImageSizeBytes > maxArtifactSize || desired.VolumeID == "" || desired.VGUUID == "" || desired.LVUUID == "" || desired.BackendResourceKey != locallvm.ResourceKey(desired.VolumeID) || desired.DesiredState != StateRealized {
		return decoded{}, errors.New("invalid typed Image materialization plan")
	}
	if _, err := hex.DecodeString(desired.ImageChecksum); err != nil {
		return decoded{}, errors.New("invalid Image SHA-256")
	}
	volume, targetPath, err := backend.Volumes.Resolve(ctx, desired.VGUUID, desired.BackendResourceKey, desired.LVUUID)
	if err != nil {
		return decoded{}, err
	}
	if volume.DeviceOpen || volume.SizeBytes < desired.ImageSizeBytes {
		return decoded{}, errors.New("root Volume is open or too small")
	}
	return decoded{request: desired, volume: volume, target: targetPath}, nil
}

func (backend Backend) copyArtifact(ctx context.Context, decoded decoded) error {
	root, err := os.OpenRoot(backend.CacheRoot)
	if err != nil {
		return errors.New("open Image cache root failed")
	}
	defer root.Close()
	source, err := root.Open("sha256/" + decoded.request.ImageChecksum)
	if err != nil {
		return errors.New("open verified Image cache artifact failed")
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || uint64(info.Size()) != decoded.request.ImageSizeBytes {
		return errors.New("Image cache artifact size/type mismatch")
	}
	sourceDigest, err := hashReader(ctx, source, decoded.request.ImageSizeBytes)
	if err != nil || sourceDigest != decoded.request.ImageChecksum {
		return errors.New("Image cache artifact digest mismatch")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return errors.New("rewind Image cache artifact failed")
	}
	destination, err := os.OpenFile(decoded.target, os.O_WRONLY, 0)
	if err != nil {
		return errors.New("open Local LVM target failed")
	}
	if _, err := io.CopyN(destination, source, int64(decoded.request.ImageSizeBytes)); err != nil {
		_ = destination.Close()
		return errors.New("write Image to Local LVM target failed")
	}
	if err := destination.Sync(); err != nil {
		_ = destination.Close()
		return errors.New("fsync Local LVM target failed")
	}
	if err := destination.Close(); err != nil {
		return errors.New("close Local LVM target failed")
	}
	return nil
}

func (backend Backend) targetMatches(ctx context.Context, decoded decoded) (bool, error) {
	target, err := os.Open(decoded.target)
	if err != nil {
		return false, errors.New("open Local LVM target for read-back failed")
	}
	defer target.Close()
	digest, err := hashReader(ctx, target, decoded.request.ImageSizeBytes)
	return err == nil && digest == decoded.request.ImageChecksum, err
}

func hashReader(ctx context.Context, reader io.Reader, size uint64) (string, error) {
	hash := sha256.New()
	written, err := io.CopyN(hash, &contextReader{ctx: ctx, reader: reader}, int64(size))
	if err != nil || uint64(written) != size {
		return "", errors.New("read bounded Image content failed")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
