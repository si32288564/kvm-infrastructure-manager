package gateway

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc/credentials"
)

func TestLimitedTransportCredentialsBoundsServerHandshakes(t *testing.T) {
	limiter, err := NewHandshakeLimiter(1)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	limited, err := NewLimitedTransportCredentials(&blockingCredentials{entered: entered, release: release}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	firstClient, firstServer := net.Pipe()
	defer firstClient.Close()
	defer firstServer.Close()
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := limited.ServerHandshake(firstServer)
		firstDone <- err
	}()
	<-entered
	secondClient, secondServer := net.Pipe()
	defer secondClient.Close()
	defer secondServer.Close()
	if _, _, err := limited.ServerHandshake(secondServer); !errors.Is(err, ErrPreAuthHandshakeLimited) {
		t.Fatalf("second ServerHandshake error = %v", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if limiter.Peak() != 1 || limiter.Rejected() != 1 {
		t.Fatalf("peak/rejected = %d/%d", limiter.Peak(), limiter.Rejected())
	}
}

type blockingCredentials struct {
	entered chan struct{}
	release chan struct{}
}

func (credentials *blockingCredentials) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("unused")
}
func (credentials *blockingCredentials) ServerHandshake(connection net.Conn) (net.Conn, credentials.AuthInfo, error) {
	credentials.entered <- struct{}{}
	<-credentials.release
	return connection, testAuthInfo{}, nil
}
func (*blockingCredentials) Info() credentials.ProtocolInfo { return credentials.ProtocolInfo{} }
func (credentials *blockingCredentials) Clone() credentials.TransportCredentials {
	return &blockingCredentials{entered: credentials.entered, release: credentials.release}
}
func (*blockingCredentials) OverrideServerName(string) error { return nil }

type testAuthInfo struct{}

func (testAuthInfo) AuthType() string { return "test" }
