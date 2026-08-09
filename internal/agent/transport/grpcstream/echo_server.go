package grpcstream

import (
	"context"
	"errors"
	"io"
	"time"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"google.golang.org/grpc"
)

// EchoServer is a Q-094 fixture endpoint, not a production Gateway handler.
type EchoServer struct {
	agentprotocolv1.UnimplementedAgentTransportServer
	FrameReadDelay time.Duration
	Reject         *agentprotocolv1.SessionRejected
}

// Connect requires Hello first and echoes subsequent envelopes.
func (server EchoServer) Connect(stream grpc.BidiStreamingServer[agentprotocolv1.Frame, agentprotocolv1.Frame]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.GetHello() == nil || first.GetHello().GetHostIdentity() == "" || first.GetHello().GetSessionGeneration() == 0 {
		return errors.New("first gRPC Agent frame must be a complete hello")
	}
	if server.Reject != nil {
		return stream.Send(&agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Rejected{Rejected: server.Reject}})
	}
	if err := stream.Send(&agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Accepted{Accepted: &agentprotocolv1.SessionAccepted{
		HostIdentity: first.GetHello().GetHostIdentity(), SessionGeneration: first.GetHello().GetSessionGeneration(),
		SessionAttemptId: first.GetHello().GetSessionAttemptId(),
	}}}); err != nil {
		return err
	}
	for {
		if server.FrameReadDelay > 0 {
			timer := time.NewTimer(server.FrameReadDelay)
			select {
			case <-timer.C:
			case <-stream.Context().Done():
				timer.Stop()
				return context.Cause(stream.Context())
			}
		}
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if frame.GetEnvelope() == nil {
			return errors.New("gRPC Agent fixture accepts envelope frames after hello")
		}
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
}
