// Package http2stream implements the Q-094 typed HTTP/2 streaming candidate.
package http2stream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/wire"
)

const sessionPath = "/v1/agent/session"

// Adapter opens one full-duplex HTTP/2 request stream on one mTLS connection.
type Adapter struct {
	Endpoint        string
	TLSConfig       *tls.Config
	MaxMessageBytes int
}

// Open implements session.TransportAdapter.
func (adapter *Adapter) Open(ctx context.Context, handshake session.Handshake) (session.TransportConnection, error) {
	if adapter.Endpoint == "" || adapter.TLSConfig == nil || adapter.MaxMessageBytes < 1 {
		return nil, errors.New("HTTP/2 endpoint, TLS configuration, and positive message limit are required")
	}
	endpoint, err := url.Parse(adapter.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse HTTP/2 Agent endpoint: %w", err)
	}
	if endpoint.Scheme != "https" {
		return nil, errors.New("HTTP/2 Agent endpoint must use https")
	}
	endpoint.Path = sessionPath

	requestReader, requestWriter := io.Pipe()
	requestContext, cancel := context.WithCancel(ctx)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint.String(), requestReader)
	if err != nil {
		cancel()
		_ = requestReader.Close()
		_ = requestWriter.Close()
		return nil, fmt.Errorf("create HTTP/2 Agent request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-kim-agent-protobuf-stream")

	transport := &http.Transport{
		TLSClientConfig:   adapter.TLSConfig.Clone(),
		ForceAttemptHTTP2: true,
		MaxConnsPerHost:   1,
	}
	client := &http.Client{Transport: transport}
	type responseResult struct {
		response *http.Response
		err      error
	}
	responseChannel := make(chan responseResult, 1)
	go func() {
		response, err := client.Do(request)
		responseChannel <- responseResult{response: response, err: err}
	}()

	hello := &agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Hello{Hello: wire.HelloToProto(handshake)}}
	if err := wire.WriteFrame(requestWriter, hello, adapter.MaxMessageBytes); err != nil {
		cancel()
		_ = requestWriter.CloseWithError(err)
		return nil, fmt.Errorf("send HTTP/2 Agent hello: %w", err)
	}

	select {
	case <-ctx.Done():
		cancel()
		_ = requestWriter.CloseWithError(context.Cause(ctx))
		return nil, context.Cause(ctx)
	case result := <-responseChannel:
		if result.err != nil {
			cancel()
			_ = requestWriter.CloseWithError(result.err)
			return nil, fmt.Errorf("open HTTP/2 Agent stream: %w", result.err)
		}
		if result.response.StatusCode != http.StatusOK || result.response.ProtoMajor != 2 {
			cancel()
			_ = requestWriter.Close()
			_ = result.response.Body.Close()
			return nil, fmt.Errorf("HTTP/2 Agent stream response is %s over %s", result.response.Status, result.response.Proto)
		}
		return &connection{
			requestWriter: requestWriter,
			responseBody:  result.response.Body,
			transport:     transport,
			cancel:        cancel,
			maxBytes:      adapter.MaxMessageBytes,
		}, nil
	}
}

type connection struct {
	requestWriter *io.PipeWriter
	responseBody  io.ReadCloser
	transport     *http.Transport
	cancel        context.CancelFunc
	maxBytes      int
	sendMu        sync.Mutex
	receiveMu     sync.Mutex
	closeOnce     sync.Once
	closeErr      error
}

func (connection *connection) Send(_ context.Context, envelope session.Envelope) error {
	connection.sendMu.Lock()
	defer connection.sendMu.Unlock()
	frame := &agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Envelope{Envelope: wire.EnvelopeToProto(envelope)}}
	if err := wire.WriteFrame(connection.requestWriter, frame, connection.maxBytes); err != nil {
		return fmt.Errorf("send HTTP/2 Agent envelope: %w", err)
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
		frame, err := wire.ReadFrame(connection.responseBody, connection.maxBytes)
		resultChannel <- receiveResult{frame: frame, err: err}
	}()
	var result receiveResult
	select {
	case <-ctx.Done():
		connection.cancel()
		_ = connection.responseBody.Close()
		<-resultChannel
		return session.Envelope{}, context.Cause(ctx)
	case result = <-resultChannel:
	}
	if result.err != nil {
		return session.Envelope{}, fmt.Errorf("receive HTTP/2 Agent envelope: %w", result.err)
	}
	if result.frame.GetEnvelope() == nil {
		return session.Envelope{}, errors.New("HTTP/2 Agent stream received non-envelope frame")
	}
	return wire.EnvelopeFromProto(result.frame.GetEnvelope())
}

func (connection *connection) Close() error {
	connection.closeOnce.Do(func() {
		if err := connection.requestWriter.Close(); err != nil {
			connection.closeErr = err
		}
		connection.cancel()
		if err := connection.responseBody.Close(); err != nil && connection.closeErr == nil {
			connection.closeErr = err
		}
		connection.transport.CloseIdleConnections()
	})
	return connection.closeErr
}
