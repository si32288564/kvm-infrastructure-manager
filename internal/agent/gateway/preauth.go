package gateway

import (
	"context"
	"errors"
	"net"
	"sync/atomic"

	"google.golang.org/grpc/credentials"
)

var ErrPreAuthHandshakeLimited = errors.New("pre-auth TLS handshake concurrency is exhausted")

// HandshakeLimiter protects unauthenticated TLS CPU/memory without making an
// identity or session-authority decision.
type HandshakeLimiter struct {
	permits  chan struct{}
	active   atomic.Int64
	peak     atomic.Int64
	rejected atomic.Int64
}

func NewHandshakeLimiter(maxConcurrent int) (*HandshakeLimiter, error) {
	if maxConcurrent < 1 {
		return nil, errors.New("pre-auth TLS handshake concurrency must be positive")
	}
	return &HandshakeLimiter{permits: make(chan struct{}, maxConcurrent)}, nil
}

func (limiter *HandshakeLimiter) tryAcquire() (func(), error) {
	select {
	case limiter.permits <- struct{}{}:
		active := limiter.active.Add(1)
		for {
			peak := limiter.peak.Load()
			if active <= peak || limiter.peak.CompareAndSwap(peak, active) {
				break
			}
		}
		var released atomic.Bool
		return func() {
			if released.CompareAndSwap(false, true) {
				<-limiter.permits
				limiter.active.Add(-1)
			}
		}, nil
	default:
		limiter.rejected.Add(1)
		return nil, ErrPreAuthHandshakeLimited
	}
}

func (limiter *HandshakeLimiter) Peak() int64     { return limiter.peak.Load() }
func (limiter *HandshakeLimiter) Rejected() int64 { return limiter.rejected.Load() }

// LimitedTransportCredentials wraps server-side TLS handshakes only. Client
// behavior and authenticated application admission remain delegated/separate.
type LimitedTransportCredentials struct {
	underlying credentials.TransportCredentials
	limiter    *HandshakeLimiter
}

func NewLimitedTransportCredentials(underlying credentials.TransportCredentials, limiter *HandshakeLimiter) (*LimitedTransportCredentials, error) {
	if underlying == nil || limiter == nil {
		return nil, errors.New("underlying transport credentials and handshake limiter are required")
	}
	return &LimitedTransportCredentials{underlying: underlying, limiter: limiter}, nil
}

func (limited *LimitedTransportCredentials) ClientHandshake(ctx context.Context, authority string, connection net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return limited.underlying.ClientHandshake(ctx, authority, connection)
}

func (limited *LimitedTransportCredentials) ServerHandshake(connection net.Conn) (net.Conn, credentials.AuthInfo, error) {
	release, err := limited.limiter.tryAcquire()
	if err != nil {
		return nil, nil, err
	}
	defer release()
	return limited.underlying.ServerHandshake(connection)
}

func (limited *LimitedTransportCredentials) Info() credentials.ProtocolInfo {
	return limited.underlying.Info()
}

func (limited *LimitedTransportCredentials) Clone() credentials.TransportCredentials {
	return &LimitedTransportCredentials{underlying: limited.underlying.Clone(), limiter: limited.limiter}
}

func (limited *LimitedTransportCredentials) OverrideServerName(name string) error {
	return limited.underlying.OverrideServerName(name)
}

var _ credentials.TransportCredentials = (*LimitedTransportCredentials)(nil)
