package locallvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync/atomic"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	RelocationCopyCommandType              = "VIRTUAL_MACHINE_ROOT_VOLUME_COPY"
	RelocationCopySchema                   = "kim.command.virtual-machine-root-volume-copy/v1"
	relocationChunkBytes                   = 1024 * 1024
	MetricLocalLVMCopyActive               = "local_lvm_copy_active"
	MetricLocalLVMCopyBytes                = "local_lvm_copy_bytes"
	MetricLocalLVMCopyAttempts             = "local_lvm_copy_attempts"
	MetricLocalLVMCopyUnknown              = "local_lvm_copy_unknown"
	MetricLocalLVMCopyVerificationFailures = "local_lvm_copy_verification_failures"
	MetricLocalLVMCopyDuration             = "local_lvm_copy_duration"
)

var relocationTargetPattern = regexp.MustCompile(`^local-lvm-relocation:([a-zA-Z0-9][a-zA-Z0-9._:-]{0,255})$`)

type RelocationVolumeIdentity struct {
	HostID, VolumeID, BindingID, VGUUID, LVUUID string
	BindingGeneration                           uint64
}
type RelocationVolumeState struct {
	SizeBytes  uint64
	HolderOpen bool
}

// RelocationClient is the bounded data plane behind the typed authority. An
// implementation resolves stable identities through administrator-owned
// mappings; it never receives a caller path or argv.
type RelocationClient interface {
	Inspect(context.Context, RelocationVolumeIdentity) (RelocationVolumeState, error)
	ReadAt(context.Context, RelocationVolumeIdentity, []byte, int64) (int, error)
	WriteAt(context.Context, RelocationVolumeIdentity, []byte, int64) (int, error)
}

type relocationRequest struct {
	CopyOperationID              string `json:"copy_operation_id"`
	CopyGeneration               uint64 `json:"copy_generation"`
	SourceHostID                 string `json:"source_host_id"`
	SourceVolumeID               string `json:"source_volume_id"`
	SourceBindingID              string `json:"source_binding_id"`
	SourceBindingGeneration      uint64 `json:"source_binding_generation"`
	SourceVGUUID                 string `json:"source_vg_uuid"`
	SourceLVUUID                 string `json:"source_lv_uuid"`
	DestinationHostID            string `json:"destination_host_id"`
	DestinationVolumeID          string `json:"destination_volume_id"`
	DestinationBindingID         string `json:"destination_binding_id"`
	DestinationBindingGeneration uint64 `json:"destination_binding_generation"`
	DestinationVGUUID            string `json:"destination_vg_uuid"`
	DestinationLVUUID            string `json:"destination_lv_uuid"`
	ExactByteCount               uint64 `json:"exact_byte_count"`
	DigestAlgorithm              string `json:"digest_algorithm"`
	CopyPolicyRevision           uint64 `json:"copy_policy_revision"`
	DesiredState                 string `json:"desired_state"`
}

type RelocationMetrics struct{ Active, Bytes, Attempts, Unknown, VerificationFailures, DurationNanoseconds atomic.Uint64 }
type RelocationMetricsSnapshot struct{ Active, Bytes, Attempts, Unknown, VerificationFailures, DurationNanoseconds uint64 }

func (m *RelocationMetrics) Snapshot() RelocationMetricsSnapshot {
	if m == nil {
		return RelocationMetricsSnapshot{}
	}
	return RelocationMetricsSnapshot{m.Active.Load(), m.Bytes.Load(), m.Attempts.Load(), m.Unknown.Load(), m.VerificationFailures.Load(), m.DurationNanoseconds.Load()}
}

type RelocationBackend struct {
	Client  RelocationClient
	Metrics *RelocationMetrics
}

func (RelocationBackend) CommandType() string   { return RelocationCopyCommandType }
func (RelocationBackend) SchemaVersion() string { return RelocationCopySchema }

func (backend RelocationBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	decoded, source, destination, err := backend.decode(lease.TargetResourceID, lease.CommandPayload)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	start := time.Now()
	if backend.Metrics != nil {
		backend.Metrics.Active.Add(1)
		backend.Metrics.Attempts.Add(1)
		defer func() {
			backend.Metrics.Active.Add(^uint64(0))
			backend.Metrics.DurationNanoseconds.Add(uint64(time.Since(start)))
		}()
	}
	sourceState, err := backend.Client.Inspect(ctx, source)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	destinationState, err := backend.Client.Inspect(ctx, destination)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if sourceState.HolderOpen || destinationState.HolderOpen || sourceState.SizeBytes != decoded.ExactByteCount || destinationState.SizeBytes != decoded.ExactByteCount {
		return agentexecution.BackendResult{}, errors.New("Local LVM relocation identity or holder fence mismatch")
	}
	before, err := backend.digest(ctx, source, decoded.ExactByteCount)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	buffer := make([]byte, relocationChunkBytes)
	for offset := uint64(0); offset < decoded.ExactByteCount; {
		length := uint64(len(buffer))
		if remaining := decoded.ExactByteCount - offset; remaining < length {
			length = remaining
		}
		chunk := buffer[:length]
		n, readErr := backend.Client.ReadAt(ctx, source, chunk, int64(offset))
		if readErr != nil && readErr != io.EOF {
			return agentexecution.BackendResult{}, readErr
		}
		if n != len(chunk) {
			return agentexecution.BackendResult{}, io.ErrUnexpectedEOF
		}
		written, writeErr := backend.Client.WriteAt(ctx, destination, chunk, int64(offset))
		if writeErr != nil {
			return agentexecution.BackendResult{}, writeErr
		}
		if written != len(chunk) {
			return agentexecution.BackendResult{}, io.ErrShortWrite
		}
		offset += uint64(n)
		if backend.Metrics != nil {
			backend.Metrics.Bytes.Add(uint64(n))
		}
	}
	after, err := backend.digest(ctx, source, decoded.ExactByteCount)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	if before != after {
		if backend.Metrics != nil {
			backend.Metrics.VerificationFailures.Add(1)
		}
		return agentexecution.BackendResult{}, errors.New("Local LVM source content drifted during copy")
	}
	observation, err := backend.observe(ctx, decoded, source, destination, lease.AttemptIndex)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	outcome := "UNKNOWN"
	if observation.State == "MATCHED" {
		outcome = "SUCCEEDED"
	} else if backend.Metrics != nil {
		backend.Metrics.Unknown.Add(1)
	}
	return agentexecution.BackendResult{Outcome: outcome, Result: observation.Evidence, Observation: observation}, nil
}

func (backend RelocationBackend) Observe(ctx context.Context, request contract.VerificationRequest) (contract.Observation, error) {
	decoded, source, destination, err := backend.decode(request.TargetResourceID, request.CommandPayload)
	if err != nil {
		return contract.Observation{}, err
	}
	return backend.observe(ctx, decoded, source, destination, request.AttemptIndex)
}

func (backend RelocationBackend) decode(target string, payload []byte) (relocationRequest, RelocationVolumeIdentity, RelocationVolumeIdentity, error) {
	var request relocationRequest
	if backend.Client == nil || relocationTargetPattern.FindStringSubmatch(target) == nil {
		return request, RelocationVolumeIdentity{}, RelocationVolumeIdentity{}, errors.New("invalid Local LVM relocation target")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, RelocationVolumeIdentity{}, RelocationVolumeIdentity{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return request, RelocationVolumeIdentity{}, RelocationVolumeIdentity{}, errors.New("trailing Local LVM relocation payload")
	}
	match := relocationTargetPattern.FindStringSubmatch(target)
	if request.CopyOperationID != match[1] || request.CopyGeneration == 0 || request.SourceHostID == "" || request.SourceVolumeID == "" || request.SourceBindingID == "" || request.SourceBindingGeneration == 0 || request.SourceVGUUID == "" || request.SourceLVUUID == "" || request.DestinationHostID == "" || request.DestinationVolumeID == "" || request.DestinationBindingID == "" || request.DestinationBindingGeneration == 0 || request.DestinationVGUUID == "" || request.DestinationLVUUID == "" || request.SourceHostID == request.DestinationHostID || request.SourceLVUUID == request.DestinationLVUUID || request.ExactByteCount == 0 || request.DigestAlgorithm != "SHA-256" || request.CopyPolicyRevision != 1 || request.DesiredState != "CONTENT_IDENTICAL" {
		return request, RelocationVolumeIdentity{}, RelocationVolumeIdentity{}, errors.New("invalid closed Local LVM relocation request")
	}
	source := RelocationVolumeIdentity{request.SourceHostID, request.SourceVolumeID, request.SourceBindingID, request.SourceVGUUID, request.SourceLVUUID, request.SourceBindingGeneration}
	destination := RelocationVolumeIdentity{request.DestinationHostID, request.DestinationVolumeID, request.DestinationBindingID, request.DestinationVGUUID, request.DestinationLVUUID, request.DestinationBindingGeneration}
	return request, source, destination, nil
}

func (backend RelocationBackend) observe(ctx context.Context, request relocationRequest, source, destination RelocationVolumeIdentity, generation int) (contract.Observation, error) {
	sourceState, err := backend.Client.Inspect(ctx, source)
	if err != nil {
		return contract.Observation{}, err
	}
	destinationState, err := backend.Client.Inspect(ctx, destination)
	if err != nil {
		return contract.Observation{}, err
	}
	sourceDigest, sourceErr := backend.digest(ctx, source, request.ExactByteCount)
	destinationDigest, destinationErr := backend.digest(ctx, destination, request.ExactByteCount)
	state := "CONFLICTING"
	copyState := "INCOMPLETE"
	if sourceErr == nil && destinationErr == nil && !sourceState.HolderOpen && !destinationState.HolderOpen && sourceState.SizeBytes == request.ExactByteCount && destinationState.SizeBytes == request.ExactByteCount && sourceDigest == destinationDigest {
		state = "MATCHED"
		copyState = "COMPLETE"
	} else if sourceErr != nil || destinationErr != nil {
		state = "UNKNOWN"
	}
	evidence := map[string]any{"copy_operation_id": request.CopyOperationID, "source_host_id": source.HostID, "source_volume_id": source.VolumeID, "source_binding_id": source.BindingID, "source_binding_generation": source.BindingGeneration, "source_lv_uuid": source.LVUUID, "destination_host_id": destination.HostID, "destination_volume_id": destination.VolumeID, "destination_binding_id": destination.BindingID, "destination_binding_generation": destination.BindingGeneration, "destination_lv_uuid": destination.LVUUID, "source_size_bytes": sourceState.SizeBytes, "destination_size_bytes": destinationState.SizeBytes, "digest_algorithm": "SHA-256", "source_content_digest": sourceDigest, "destination_content_digest": destinationDigest, "copy_state": copyState}
	raw, _ := json.Marshal(evidence)
	digest := sha256.Sum256(raw)
	if state != "MATCHED" && backend.Metrics != nil {
		backend.Metrics.VerificationFailures.Add(1)
	}
	return contract.Observation{State: state, Generation: int64(generation), Digest: hex.EncodeToString(digest[:]), Evidence: evidence}, nil
}

func (backend RelocationBackend) digest(ctx context.Context, identity RelocationVolumeIdentity, size uint64) (string, error) {
	hash := sha256.New()
	buffer := make([]byte, relocationChunkBytes)
	for offset := uint64(0); offset < size; {
		length := uint64(len(buffer))
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		chunk := buffer[:length]
		n, err := backend.Client.ReadAt(ctx, identity, chunk, int64(offset))
		if err != nil && err != io.EOF {
			return "", err
		}
		if n != len(chunk) {
			return "", io.ErrUnexpectedEOF
		}
		_, _ = hash.Write(chunk)
		offset += uint64(n)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

var _ agentexecution.Backend = RelocationBackend{}
var _ agentexecution.Observer = RelocationBackend{}
