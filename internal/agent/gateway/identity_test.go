package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestTrustedProxyPeerIdentityResolverRequiresPinnedProxyAndSanitizedXFCC(t *testing.T) {
	proxyCertificate := &x509.Certificate{Raw: []byte("proxy-certificate")}
	proxyHash := certificateSHA256(proxyCertificate)
	downstreamDigest := sha256.Sum256([]byte("agent-certificate"))
	downstreamHash := hex.EncodeToString(downstreamDigest[:])
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{proxyCertificate}, VerifiedChains: [][]*x509.Certificate{{proxyCertificate}},
	}}})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(forwardedClientCertificateHeader, `[{"hash":"`+downstreamHash+`"}]`))
	resolver := TrustedProxyPeerIdentityResolver{AllowedProxyCertificateSHA256: map[string]struct{}{proxyHash: {}}}
	evidence, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Identity != "xfcc-sha256:"+downstreamHash || evidence.CredentialCertificateSHA256 != downstreamHash || evidence.TransportPeerCertificateSHA256 != proxyHash || !evidence.ViaTrustedProxy {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestTrustedProxyPeerIdentityResolverRejectsUnpinnedPeer(t *testing.T) {
	proxyCertificate := &x509.Certificate{Raw: []byte("untrusted-proxy")}
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{proxyCertificate}, VerifiedChains: [][]*x509.Certificate{{proxyCertificate}},
	}}})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(forwardedClientCertificateHeader, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`))
	resolver := TrustedProxyPeerIdentityResolver{AllowedProxyCertificateSHA256: map[string]struct{}{}}
	if _, err := resolver.Resolve(ctx); err == nil {
		t.Fatal("unpinned proxy was accepted")
	}
}

func TestTrustedProxyPeerIdentityResolverRejectsMultipleElements(t *testing.T) {
	proxyCertificate := &x509.Certificate{Raw: []byte("proxy-certificate")}
	proxyHash := certificateSHA256(proxyCertificate)
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{proxyCertificate}, VerifiedChains: [][]*x509.Certificate{{proxyCertificate}},
	}}})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(forwardedClientCertificateHeader, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]`))
	resolver := TrustedProxyPeerIdentityResolver{AllowedProxyCertificateSHA256: map[string]struct{}{proxyHash: {}}}
	if _, err := resolver.Resolve(ctx); err == nil {
		t.Fatal("multiple XFCC elements were accepted")
	}
}

func TestTrustedProxyPeerIdentityResolverRejectsMissingMalformedAndInvalidHash(t *testing.T) {
	proxyCertificate := &x509.Certificate{Raw: []byte("proxy-certificate")}
	proxyHash := certificateSHA256(proxyCertificate)
	base := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{proxyCertificate}, VerifiedChains: [][]*x509.Certificate{{proxyCertificate}},
	}}})
	resolver := TrustedProxyPeerIdentityResolver{AllowedProxyCertificateSHA256: map[string]struct{}{proxyHash: {}}}
	for name, values := range map[string][]string{
		"missing":      nil,
		"malformed":    {`not-json`},
		"invalid hash": {`[{"hash":"abcd"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := base
			if len(values) > 0 {
				ctx = metadata.NewIncomingContext(ctx, metadata.MD{forwardedClientCertificateHeader: values})
			}
			if _, err := resolver.Resolve(ctx); err == nil {
				t.Fatalf("%s XFCC was accepted", name)
			}
		})
	}
}

func TestDirectPeerIdentityResolverIgnoresForwardedHeader(t *testing.T) {
	certificate := &x509.Certificate{Raw: []byte("direct-agent"), Subject: pkix.Name{CommonName: "direct-agent"}}
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}},
	}}})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(forwardedClientCertificateHeader, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`))
	evidence, err := (DirectPeerIdentityResolver{}).Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Identity != "direct-agent" || evidence.CredentialCertificateSHA256 != certificateSHA256(certificate) || evidence.ViaTrustedProxy {
		t.Fatalf("evidence = %#v", evidence)
	}
}
