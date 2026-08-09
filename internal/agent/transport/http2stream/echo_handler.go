package http2stream

import (
	"errors"
	"io"
	"net/http"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/wire"
)

// EchoHandler is a Q-094 fixture endpoint, not a production Gateway handler.
type EchoHandler struct {
	MaxMessageBytes int
}

// ServeHTTP requires mTLS and HTTP/2, validates Hello first, and echoes envelopes.
func (handler EchoHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != sessionPath {
		http.NotFound(writer, request)
		return
	}
	if request.ProtoMajor != 2 || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		http.Error(writer, "HTTP/2 mTLS is required", http.StatusUnauthorized)
		return
	}
	if handler.MaxMessageBytes < 1 {
		http.Error(writer, "invalid fixture message limit", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/x-kim-agent-protobuf-stream")
	writer.WriteHeader(http.StatusOK)
	if err := http.NewResponseController(writer).Flush(); err != nil {
		return
	}

	first, err := wire.ReadFrame(request.Body, handler.MaxMessageBytes)
	if err != nil || first.GetHello() == nil || first.GetHello().GetHostIdentity() == "" || first.GetHello().GetSessionGeneration() == 0 {
		return
	}
	for {
		frame, err := wire.ReadFrame(request.Body, handler.MaxMessageBytes)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return
		}
		if err != nil || frame.GetEnvelope() == nil {
			return
		}
		if err := wire.WriteFrame(writer, frame, handler.MaxMessageBytes); err != nil {
			return
		}
		if err := http.NewResponseController(writer).Flush(); err != nil {
			return
		}
	}
}
