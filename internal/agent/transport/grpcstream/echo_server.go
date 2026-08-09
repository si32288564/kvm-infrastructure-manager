package grpcstream

import (
	"errors"
	"io"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"google.golang.org/grpc"
)

// EchoServer is a Q-094 fixture endpoint, not a production Gateway handler.
type EchoServer struct {
	agentprotocolv1.UnimplementedAgentTransportServer
}

// Connect requires Hello first and echoes subsequent envelopes.
func (EchoServer) Connect(stream grpc.BidiStreamingServer[agentprotocolv1.Frame, agentprotocolv1.Frame]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.GetHello() == nil || first.GetHello().GetHostIdentity() == "" || first.GetHello().GetSessionGeneration() == 0 {
		return errors.New("first gRPC Agent frame must be a complete hello")
	}
	for {
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
