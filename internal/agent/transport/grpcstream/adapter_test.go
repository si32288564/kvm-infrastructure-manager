package grpcstream

import (
	"errors"
	"net"
	"testing"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestGRPCAdapterContract(t *testing.T) {
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	countingListener := contracttest.NewCountingListener(listener)
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	agentprotocolv1.RegisterAgentTransportServer(server, EchoServer{})
	go func() { _ = server.Serve(countingListener) }()
	defer server.Stop()

	contracttest.ExerciseEcho(t, &Adapter{
		Target:          countingListener.Addr().String(),
		TLSConfig:       clientTLS,
		MaxMessageBytes: 64 * 1024,
	})
	if countingListener.Accepted() != 1 {
		t.Fatalf("physical connection count = %d, want 1", countingListener.Accepted())
	}
}

func TestGRPCAdapterReturnsTypedAdmissionRejection(t *testing.T) {
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	agentprotocolv1.RegisterAgentTransportServer(server, EchoServer{Reject: &agentprotocolv1.SessionRejected{Code: "GATEWAY_ADMISSION_LIMITED", Retryable: true, RetryAfterMillis: 25}})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	_, err = (&Adapter{Target: listener.Addr().String(), TLSConfig: clientTLS, MaxMessageBytes: 64 * 1024}).Open(t.Context(), session.Handshake{HostIdentity: "host-reject", SessionGeneration: 1, ProtocolVersion: "v1"})
	var rejection *session.AdmissionRejectedError
	if !errors.As(err, &rejection) || rejection.Code != "GATEWAY_ADMISSION_LIMITED" {
		t.Fatalf("Open rejection = %#v, error = %v", rejection, err)
	}
}

func TestGRPCAdapterRejectsMissingClientCertificate(t *testing.T) {
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	agentprotocolv1.RegisterAgentTransportServer(server, EchoServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	clientTLS.Certificates = nil

	adapter := &Adapter{Target: listener.Addr().String(), TLSConfig: clientTLS, MaxMessageBytes: 64 * 1024}
	connection, err := adapter.Open(t.Context(), session.Handshake{HostIdentity: "host-1", SessionGeneration: 1, ProtocolVersion: "v1"})
	if err == nil {
		_ = connection.Close()
		t.Fatal("Open accepted a client without certificate")
	}
}

func TestGRPCAdapterReceiveCancellation(t *testing.T) {
	server, _, adapter := newGRPCFixture(t)
	defer server.Stop()
	contracttest.ExerciseReceiveCancellation(t, adapter)
}

func TestGRPCAdapterPersistentReceiveLoop(t *testing.T) {
	server, _, adapter := newGRPCFixture(t)
	defer server.Stop()
	contracttest.ExercisePersistentReceiveLoop(t, adapter)
}

func TestGRPCAdapterDetectsDisconnect(t *testing.T) {
	server, _, adapter := newGRPCFixture(t)
	contracttest.ExerciseDisconnect(t, adapter, server.Stop)
}

func BenchmarkGRPCRoundTrip1KiB(b *testing.B) {
	server, _, adapter := newGRPCFixture(b)
	defer server.Stop()
	contracttest.BenchmarkRoundTrip(b, adapter, 1024)
}

func BenchmarkGRPCRoundTrip256KiB(b *testing.B) {
	server, _, adapter := newGRPCFixture(b)
	defer server.Stop()
	contracttest.BenchmarkRoundTrip(b, adapter, 256*1024)
}

func newGRPCFixture(t testing.TB) (*grpc.Server, net.Listener, *Adapter) {
	t.Helper()
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	agentprotocolv1.RegisterAgentTransportServer(server, EchoServer{})
	go func() { _ = server.Serve(listener) }()
	return server, listener, &Adapter{Target: listener.Addr().String(), TLSConfig: clientTLS, MaxMessageBytes: 512 * 1024}
}
