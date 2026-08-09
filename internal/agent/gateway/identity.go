package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const forwardedClientCertificateHeader = "x-forwarded-client-cert"

// PeerIdentityEvidence separates the authenticated transport peer from the
// downstream certificate evidence forwarded by an explicitly trusted L7 proxy.
type PeerIdentityEvidence struct {
	Identity                       string
	CredentialCertificateSHA256    string
	TransportPeerCertificateSHA256 string
	ViaTrustedProxy                bool
}

// PeerIdentityResolver resolves identity evidence after the upstream mTLS
// handshake. It cannot grant Host session authority.
type PeerIdentityResolver interface {
	Resolve(context.Context) (PeerIdentityEvidence, error)
}

// DirectPeerIdentityResolver uses the certificate authenticated directly by
// the Gateway transport.
type DirectPeerIdentityResolver struct{}

func (DirectPeerIdentityResolver) Resolve(ctx context.Context) (PeerIdentityEvidence, error) {
	certificate, err := authenticatedPeerCertificate(ctx)
	if err != nil {
		return PeerIdentityEvidence{}, err
	}
	if certificate.Subject.CommonName == "" {
		return PeerIdentityEvidence{}, errors.New("Agent certificate identity is empty")
	}
	return PeerIdentityEvidence{
		Identity: certificate.Subject.CommonName, CredentialCertificateSHA256: certificateSHA256(certificate),
		TransportPeerCertificateSHA256: certificateSHA256(certificate),
	}, nil
}

// TrustedProxyPeerIdentityResolver accepts XFCC evidence only from a pinned
// proxy leaf certificate. Envoy must use SANITIZE_SET with JSON format so an
// Agent cannot supply or append its own XFCC element.
type TrustedProxyPeerIdentityResolver struct {
	AllowedProxyCertificateSHA256 map[string]struct{}
}

func (resolver TrustedProxyPeerIdentityResolver) Resolve(ctx context.Context) (PeerIdentityEvidence, error) {
	certificate, err := authenticatedPeerCertificate(ctx)
	if err != nil {
		return PeerIdentityEvidence{}, err
	}
	proxyHash := certificateSHA256(certificate)
	if _, allowed := resolver.AllowedProxyCertificateSHA256[proxyHash]; !allowed {
		return PeerIdentityEvidence{}, fmt.Errorf("transport peer certificate %s is not an allowed Agent L7 proxy", proxyHash)
	}
	values := metadata.ValueFromIncomingContext(ctx, forwardedClientCertificateHeader)
	if len(values) != 1 {
		return PeerIdentityEvidence{}, fmt.Errorf("trusted proxy XFCC value count = %d, want 1", len(values))
	}
	var elements []struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(values[0]), &elements); err != nil {
		return PeerIdentityEvidence{}, fmt.Errorf("decode trusted proxy XFCC JSON: %w", err)
	}
	if len(elements) != 1 {
		return PeerIdentityEvidence{}, fmt.Errorf("trusted proxy XFCC element count = %d, want 1", len(elements))
	}
	downstreamHash := strings.ToLower(elements[0].Hash)
	decoded, err := hex.DecodeString(downstreamHash)
	if err != nil || len(decoded) != sha256.Size {
		return PeerIdentityEvidence{}, errors.New("trusted proxy XFCC certificate hash is not SHA-256")
	}
	return PeerIdentityEvidence{
		Identity: "xfcc-sha256:" + downstreamHash, CredentialCertificateSHA256: downstreamHash,
		TransportPeerCertificateSHA256: proxyHash, ViaTrustedProxy: true,
	}, nil
}

func authenticatedPeerCertificate(ctx context.Context) (*x509.Certificate, error) {
	peerInfo, ok := peer.FromContext(ctx)
	if !ok {
		return nil, errors.New("Agent transport peer is unavailable")
	}
	tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, errors.New("verified Agent mTLS identity is required")
	}
	return tlsInfo.State.PeerCertificates[0], nil
}

func certificateSHA256(certificate *x509.Certificate) string {
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:])
}
