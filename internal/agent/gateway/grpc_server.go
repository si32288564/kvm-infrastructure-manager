package gateway

import (
	"context"
	"errors"
	"io"
	"time"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// SessionAuthorizer converts authenticated Hello evidence into an explicit
// PostgreSQL-backed session decision.
type SessionAuthorizer interface {
	Authorize(context.Context, *agentprotocolv1.SessionHello, string) (*agentprotocolv1.SessionAccepted, *agentprotocolv1.SessionRejected)
}

type PostgresSessionAuthorizer struct {
	DB         postgres.TxBeginner
	Admission  *AdmissionLimiter
	RetryAfter time.Duration
}

func (authorizer PostgresSessionAuthorizer) Authorize(ctx context.Context, hello *agentprotocolv1.SessionHello, peerIdentity string) (*agentprotocolv1.SessionAccepted, *agentprotocolv1.SessionRejected) {
	if authorizer.DB == nil || authorizer.Admission == nil || hello == nil || peerIdentity == "" {
		return nil, rejection("INVALID_HANDSHAKE", false, 0)
	}
	release, err := authorizer.Admission.TryAcquire()
	if errors.Is(err, ErrAdmissionLimited) {
		return nil, rejection("GATEWAY_ADMISSION_LIMITED", true, authorizer.RetryAfter)
	}
	if err != nil {
		return nil, rejection("GATEWAY_ADMISSION_FAILED", true, authorizer.RetryAfter)
	}
	defer release()
	grant, err := postgres.AdmitAgentSession(ctx, authorizer.DB, postgres.AgentSessionAdmission{
		SessionAttemptID: hello.GetSessionAttemptId(), HostID: hello.GetHostIdentity(),
		ConnectionInstanceID: hello.GetConnectionInstanceId(), TransportProfile: "grpc-v1",
		ProtocolVersion: hello.GetProtocolVersion(), AgentArtifactDigest: hello.GetAgentArtifactDigest(),
		CredentialBindingRevision: hello.GetCredentialBindingRevision(),
		ExpectedSessionGeneration: int64(hello.GetSessionGeneration()),
		HandshakeEvidence:         map[string]any{"peer_identity": peerIdentity, "capabilities": hello.GetCapabilities()},
	})
	if err == nil {
		return &agentprotocolv1.SessionAccepted{
			HostIdentity: grant.HostID, SessionGeneration: uint64(grant.SessionGeneration), SessionAttemptId: grant.SessionAttemptID,
		}, nil
	}
	switch {
	case errors.Is(err, postgres.ErrDatabaseAuthorityNotActive):
		return nil, rejection("DATABASE_AUTHORITY_NOT_ACTIVE", true, authorizer.RetryAfter)
	case errors.Is(err, postgres.ErrHostNotApproved):
		return nil, rejection("HOST_NOT_APPROVED", false, 0)
	case errors.Is(err, postgres.ErrSessionGenerationConflict):
		return nil, rejection("SESSION_GENERATION_CONFLICT", false, 0)
	case errors.Is(err, postgres.ErrSessionAttemptConflict):
		return nil, rejection("SESSION_ATTEMPT_CONFLICT", false, 0)
	default:
		return nil, rejection("DATABASE_ADMISSION_UNAVAILABLE", true, authorizer.RetryAfter)
	}
}

// GRPCServer requires verified mTLS peer evidence, sends exactly one session
// decision, then accepts only the granted Host/generation on the live stream.
type GRPCServer struct {
	agentprotocolv1.UnimplementedAgentTransportServer
	Authorizer SessionAuthorizer
}

func (server GRPCServer) Connect(stream grpc.BidiStreamingServer[agentprotocolv1.Frame, agentprotocolv1.Frame]) error {
	peerIdentity, err := authenticatedPeerIdentity(stream.Context())
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return errors.New("first Agent frame must be SessionHello")
	}
	if server.Authorizer == nil {
		return errors.New("Agent Session Authorizer is required")
	}
	accepted, rejected := server.Authorizer.Authorize(stream.Context(), hello, peerIdentity)
	if rejected != nil {
		if err := stream.Send(&agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Rejected{Rejected: rejected}}); err != nil {
			return err
		}
		return nil
	}
	if accepted == nil {
		return errors.New("Agent Session Authorizer returned no decision")
	}
	if err := stream.Send(&agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Accepted{Accepted: accepted}}); err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		envelope := frame.GetEnvelope()
		if envelope == nil || envelope.GetHostIdentity() != accepted.GetHostIdentity() || envelope.GetSessionGeneration() != accepted.GetSessionGeneration() {
			return errors.New("Agent envelope does not match granted session authority")
		}
	}
}

func authenticatedPeerIdentity(ctx context.Context) (string, error) {
	peerInfo, ok := peer.FromContext(ctx)
	if !ok {
		return "", errors.New("Agent transport peer is unavailable")
	}
	tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.PeerCertificates) == 0 {
		return "", errors.New("verified Agent mTLS identity is required")
	}
	identity := tlsInfo.State.PeerCertificates[0].Subject.CommonName
	if identity == "" {
		return "", errors.New("Agent certificate identity is empty")
	}
	return identity, nil
}

func rejection(code string, retryable bool, retryAfter time.Duration) *agentprotocolv1.SessionRejected {
	if retryAfter < 0 {
		retryAfter = 0
	}
	return &agentprotocolv1.SessionRejected{Code: code, Retryable: retryable, RetryAfterMillis: uint64(retryAfter / time.Millisecond)}
}

var _ SessionAuthorizer = PostgresSessionAuthorizer{}
