// Package grpcstream implements the Q-094 gRPC bidirectional-stream candidate.
package grpcstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Adapter opens one gRPC ClientConn and one bidirectional stream per Manager.
type Adapter struct {
	Target          string
	TLSConfig       *tls.Config
	MaxMessageBytes int
}

// Open implements session.TransportAdapter.
func (adapter *Adapter) Open(ctx context.Context, handshake session.Handshake) (session.TransportConnection, error) {
	if adapter.Target == "" || adapter.TLSConfig == nil || adapter.MaxMessageBytes < 1 {
		return nil, errors.New("gRPC target, TLS configuration, and positive message limit are required")
	}
	clientConnection, err := grpc.NewClient(
		adapter.Target,
		grpc.WithTransportCredentials(credentials.NewTLS(adapter.TLSConfig.Clone())),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(adapter.MaxMessageBytes),
			grpc.MaxCallRecvMsgSize(adapter.MaxMessageBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create gRPC Agent connection: %w", err)
	}
	streamContext, cancel := context.WithCancel(ctx)
	stream, err := agentprotocolv1.NewAgentTransportClient(clientConnection).Connect(streamContext)
	if err != nil {
		cancel()
		_ = clientConnection.Close()
		return nil, fmt.Errorf("open gRPC Agent stream: %w", err)
	}
	if err := stream.Send(&agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Hello{Hello: wire.HelloToProto(handshake)}}); err != nil {
		cancel()
		_ = clientConnection.Close()
		return nil, fmt.Errorf("send gRPC Agent hello: %w", err)
	}
	return &connection{clientConnection: clientConnection, stream: stream, cancel: cancel}, nil
}

type connection struct {
	clientConnection *grpc.ClientConn
	stream           grpc.BidiStreamingClient[agentprotocolv1.Frame, agentprotocolv1.Frame]
	cancel           context.CancelFunc
	sendMu           sync.Mutex
	receiveMu        sync.Mutex
	closeOnce        sync.Once
	closeErr         error
}

func (connection *connection) Send(_ context.Context, envelope session.Envelope) error {
	connection.sendMu.Lock()
	defer connection.sendMu.Unlock()
	frame := &agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Envelope{Envelope: wire.EnvelopeToProto(envelope)}}
	if err := connection.stream.Send(frame); err != nil {
		return fmt.Errorf("send gRPC Agent envelope: %w", err)
	}
	return nil
}

func (connection *connection) Receive(ctx context.Context) (session.Envelope, error) {
	connection.receiveMu.Lock()
	defer connection.receiveMu.Unlock()
	type receiveResult struct {
		frame *agentprotocolv1.Frame
		err   error
	}
	resultChannel := make(chan receiveResult, 1)
	go func() {
		frame, err := connection.stream.Recv()
		resultChannel <- receiveResult{frame: frame, err: err}
	}()
	var result receiveResult
	select {
	case <-ctx.Done():
		connection.cancel()
		<-resultChannel
		return session.Envelope{}, context.Cause(ctx)
	case result = <-resultChannel:
	}
	if result.err != nil {
		return session.Envelope{}, fmt.Errorf("receive gRPC Agent envelope: %w", result.err)
	}
	if result.frame.GetEnvelope() == nil {
		return session.Envelope{}, errors.New("gRPC Agent stream received non-envelope frame")
	}
	return wire.EnvelopeFromProto(result.frame.GetEnvelope())
}

func (connection *connection) Close() error {
	connection.closeOnce.Do(func() {
		connection.cancel()
		if err := connection.stream.CloseSend(); err != nil && !errors.Is(err, io.EOF) {
			connection.closeErr = err
		}
		if err := connection.clientConnection.Close(); err != nil && connection.closeErr == nil {
			connection.closeErr = err
		}
	})
	return connection.closeErr
}
