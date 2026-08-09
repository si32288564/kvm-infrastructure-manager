package http2stream

import "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"

func testHandshake() session.Handshake {
	return session.Handshake{HostIdentity: "host-1", SessionGeneration: 1, ProtocolVersion: "v1"}
}
