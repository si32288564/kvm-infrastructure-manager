package http2stream

import (
	"net/http/httptest"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
)

func TestHTTP2AdapterContract(t *testing.T) {
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(EchoHandler{MaxMessageBytes: 64 * 1024})
	countingListener := contracttest.NewCountingListener(server.Listener)
	server.Listener = countingListener
	server.TLS = serverTLS
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	contracttest.ExerciseEcho(t, &Adapter{
		Endpoint:        server.URL,
		TLSConfig:       clientTLS,
		MaxMessageBytes: 64 * 1024,
	})
	if countingListener.Accepted() != 1 {
		t.Fatalf("physical connection count = %d, want 1", countingListener.Accepted())
	}
}

func TestHTTP2AdapterRejectsMissingClientCertificate(t *testing.T) {
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(EchoHandler{MaxMessageBytes: 64 * 1024})
	server.TLS = serverTLS
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	clientTLS.Certificates = nil

	adapter := &Adapter{Endpoint: server.URL, TLSConfig: clientTLS, MaxMessageBytes: 64 * 1024}
	if connection, err := adapter.Open(t.Context(), testHandshake()); err == nil {
		_ = connection.Close()
		t.Fatal("Open accepted a client without certificate")
	}
}

func TestHTTP2AdapterReceiveCancellation(t *testing.T) {
	server, adapter := newHTTP2Fixture(t)
	defer server.Close()
	contracttest.ExerciseReceiveCancellation(t, adapter)
}

func TestHTTP2AdapterDetectsDisconnect(t *testing.T) {
	server, adapter := newHTTP2Fixture(t)
	defer server.Close()
	contracttest.ExerciseDisconnect(t, adapter, server.CloseClientConnections)
}

func BenchmarkHTTP2RoundTrip1KiB(b *testing.B) {
	server, adapter := newHTTP2Fixture(b)
	defer server.Close()
	contracttest.BenchmarkRoundTrip(b, adapter, 1024)
}

func BenchmarkHTTP2RoundTrip256KiB(b *testing.B) {
	server, adapter := newHTTP2Fixture(b)
	defer server.Close()
	contracttest.BenchmarkRoundTrip(b, adapter, 256*1024)
}

func newHTTP2Fixture(t testing.TB) (*httptest.Server, *Adapter) {
	t.Helper()
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(EchoHandler{MaxMessageBytes: 512 * 1024})
	server.TLS = serverTLS
	server.EnableHTTP2 = true
	server.StartTLS()
	return server, &Adapter{Endpoint: server.URL, TLSConfig: clientTLS, MaxMessageBytes: 512 * 1024}
}
