package locallvmtransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const (
	SourceAuthorizeCommandType = "LOCAL_LVM_TRANSPORT_SOURCE_AUTHORIZE"
	SourceAuthorizeSchema      = "kim.command.local-lvm-transport-source-authorize/v1"
	DestinationCommandType     = "LOCAL_LVM_CROSS_HOST_TRANSPORT_START"
	DestinationSchema          = "kim.command.local-lvm-cross-host-transport-start/v1"
)

var runtimeTarget = regexp.MustCompile(`^local-lvm-transport:([A-Za-z0-9][A-Za-z0-9._:-]{0,255})$`)

type commandPayload struct {
	Authority Authority `json:"authority"`
}

type SourceAuthorizeBackend struct {
	Registry                    *SourceRegistry
	Reader                      SourceReader
	LocalCertificateFingerprint string
	CredentialRevision          uint64
}

func (SourceAuthorizeBackend) CommandType() string    { return SourceAuthorizeCommandType }
func (SourceAuthorizeBackend) SchemaVersion() string  { return SourceAuthorizeSchema }
func (SourceAuthorizeBackend) Capabilities() []string { return []string{Capability} }

func (backend SourceAuthorizeBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	authority, err := decodeAuthority(lease.TargetResourceID, lease.CommandPayload)
	if err != nil || lease.HostID != authority.Source.HostID || uint64(lease.HostAuthorityGeneration) != authority.SourceHostAuthorityGeneration || uint64(lease.SessionGeneration) != authority.SourceSessionGeneration || backend.CredentialRevision != authority.SourceCredentialBindingRevision || backend.LocalCertificateFingerprint != authority.SourceCertificateFingerprint {
		return agentexecution.BackendResult{}, ErrAuthorityConflict
	}
	if err := backend.Registry.Authorize(authority, uint64(lease.HostAuthorityGeneration), uint64(lease.SessionGeneration)); err != nil {
		return agentexecution.BackendResult{}, err
	}
	observation, err := peerObservation(ctx, authority, "SOURCE", backend.Reader, nil, backend.LocalCertificateFingerprint, lease.AttemptIndex)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	return agentexecution.BackendResult{Outcome: "SUCCEEDED", Result: observation.Evidence, Observation: observation}, nil
}

func (backend SourceAuthorizeBackend) Observe(ctx context.Context, request contract.VerificationRequest) (contract.Observation, error) {
	authority, err := decodeAuthority(request.TargetResourceID, request.CommandPayload)
	if err != nil || request.HostID != authority.Source.HostID || uint64(request.SessionGeneration) != authority.SourceSessionGeneration || backend.CredentialRevision != authority.SourceCredentialBindingRevision || backend.LocalCertificateFingerprint != authority.SourceCertificateFingerprint {
		return contract.Observation{}, ErrAuthorityConflict
	}
	return peerObservation(ctx, authority, "SOURCE", backend.Reader, nil, backend.LocalCertificateFingerprint, request.AttemptIndex)
}

type DestinationBackend struct {
	HostID, LocalCertificateFingerprint string
	CredentialRevision                  uint64
	Writer                              DestinationWriter
	Client                              *http.Client
	Endpoints                           EndpointRegistry
	Metrics                             *Metrics
}

func (DestinationBackend) CommandType() string    { return DestinationCommandType }
func (DestinationBackend) SchemaVersion() string  { return DestinationSchema }
func (DestinationBackend) Capabilities() []string { return []string{Capability} }

func (backend DestinationBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	authority, err := decodeAuthority(lease.TargetResourceID, lease.CommandPayload)
	if err != nil || lease.HostID != backend.HostID || lease.HostID != authority.Destination.HostID || uint64(lease.HostAuthorityGeneration) != authority.DestinationHostAuthorityGeneration || uint64(lease.SessionGeneration) != authority.DestinationSessionGeneration || backend.CredentialRevision != authority.DestinationCredentialBindingRevision || backend.LocalCertificateFingerprint != authority.DestinationCertificateFingerprint {
		return agentexecution.BackendResult{}, ErrAuthorityConflict
	}
	endpoint, err := backend.Endpoints.endpoint(authority.Source.HostID)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	result, err := (DestinationClient{Authority: authority, Writer: backend.Writer, Client: backend.Client, Endpoint: endpoint, Metrics: backend.Metrics}).Transfer(ctx, lease.AttemptIndex)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	observation, err := peerObservation(ctx, authority, "DESTINATION", nil, backend.Writer, backend.LocalCertificateFingerprint, lease.AttemptIndex)
	if err != nil {
		return agentexecution.BackendResult{}, err
	}
	observation.Evidence["bytes_transferred"] = result.BytesTransferred
	observation.Evidence["source_content_digest"] = result.SourceDigest
	observation.Evidence["response_state"] = result.ResponseState
	return agentexecution.BackendResult{Outcome: "SUCCEEDED", Result: observation.Evidence, Observation: observation}, nil
}

func (backend DestinationBackend) Observe(ctx context.Context, request contract.VerificationRequest) (contract.Observation, error) {
	authority, err := decodeAuthority(request.TargetResourceID, request.CommandPayload)
	if err != nil || request.HostID != backend.HostID || request.HostID != authority.Destination.HostID || uint64(request.SessionGeneration) != authority.DestinationSessionGeneration || backend.CredentialRevision != authority.DestinationCredentialBindingRevision || backend.LocalCertificateFingerprint != authority.DestinationCertificateFingerprint {
		return contract.Observation{}, ErrAuthorityConflict
	}
	return peerObservation(ctx, authority, "DESTINATION", nil, backend.Writer, backend.LocalCertificateFingerprint, request.AttemptIndex)
}

func EncodeCommandPayload(authority Authority) ([]byte, error) {
	if authority.Validate(time.Now()) != nil {
		return nil, ErrAuthorityConflict
	}
	return json.Marshal(commandPayload{Authority: authority})
}

func decodeAuthority(target string, payload []byte) (Authority, error) {
	match := runtimeTarget.FindStringSubmatch(target)
	if len(match) != 2 {
		return Authority{}, ErrAuthorityConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var command commandPayload
	if err := decoder.Decode(&command); err != nil {
		return Authority{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Authority{}, ErrAuthorityConflict
	}
	if command.Authority.TransportSessionID != match[1] || command.Authority.Validate(time.Now()) != nil {
		return Authority{}, ErrAuthorityConflict
	}
	return command.Authority, nil
}

func peerObservation(ctx context.Context, authority Authority, role string, reader SourceReader, writer DestinationWriter, certificateFingerprint string, generation int) (contract.Observation, error) {
	identity, credential, session := authority.Source, authority.SourceCredentialBindingRevision, authority.SourceSessionGeneration
	var state VolumeState
	var digest string
	var err error
	if role == "SOURCE" && reader != nil {
		state, err = reader.Inspect(ctx, identity)
		if err == nil {
			digest, err = digestReader(ctx, reader, identity, authority.ExactByteCount, authority.ChunkSize)
		}
	} else if role == "DESTINATION" && writer != nil {
		identity, credential, session = authority.Destination, authority.DestinationCredentialBindingRevision, authority.DestinationSessionGeneration
		state, err = writer.Inspect(ctx, identity)
		if err == nil {
			digest, err = digestWriter(ctx, writer, identity, authority.ExactByteCount, authority.ChunkSize)
		}
	} else {
		return contract.Observation{}, ErrAuthorityConflict
	}
	if err != nil || state.HolderOpen || state.SizeBytes != authority.ExactByteCount || len(digest) != 64 {
		return contract.Observation{}, ErrAuthorityConflict
	}
	evidence := map[string]any{"transport_session_id": authority.TransportSessionID, "transport_generation": authority.TransportGeneration, "authority_digest": authority.Digest(), "peer_role": role, "host_id": identity.HostID, "credential_binding_revision": credential, "session_generation": session, "certificate_fingerprint": certificateFingerprint, "volume_id": identity.VolumeID, "binding_id": identity.BindingID, "binding_generation": identity.BindingGeneration, "lv_uuid": identity.LVUUID, "size_bytes": state.SizeBytes, "holder_open": state.HolderOpen, "digest_algorithm": "SHA-256", "content_digest": digest}
	raw, _ := json.Marshal(evidence)
	observationDigest := sha256.Sum256(raw)
	return contract.Observation{State: "MATCHED", Generation: int64(generation), Digest: hex.EncodeToString(observationDigest[:]), Evidence: evidence}, nil
}

var _ agentexecution.Backend = SourceAuthorizeBackend{}
var _ agentexecution.Observer = SourceAuthorizeBackend{}
var _ agentexecution.Backend = DestinationBackend{}
var _ agentexecution.Observer = DestinationBackend{}
