// Package faultproxy provides an in-process TLS passthrough proxy fixture.
// It deliberately has no session or authority semantics.
package faultproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Proxy forwards opaque TCP streams to one backend and can drain all current
// streams. TLS remains end-to-end between Agent and Gateway.
type Proxy struct {
	listener    net.Listener
	target      string
	dialTimeout time.Duration

	accepted atomic.Int64
	drained  atomic.Int64
	mu       sync.Mutex
	active   map[*connectionPair]struct{}
	closed   bool
	wait     sync.WaitGroup
}

// New creates a fixed-target TLS passthrough proxy.
func New(listener net.Listener, target string, dialTimeout time.Duration) (*Proxy, error) {
	if listener == nil || target == "" || dialTimeout <= 0 {
		return nil, errors.New("listener, target, and positive dial timeout are required")
	}
	return &Proxy{listener: listener, target: target, dialTimeout: dialTimeout, active: make(map[*connectionPair]struct{})}, nil
}

// Addr returns the Agent-facing proxy address.
func (proxy *Proxy) Addr() net.Addr { return proxy.listener.Addr() }

// Serve accepts and forwards connections until Close is called.
func (proxy *Proxy) Serve() error {
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			proxy.mu.Lock()
			closed := proxy.closed
			proxy.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		proxy.accepted.Add(1)
		proxy.wait.Add(1)
		go proxy.forward(client)
	}
}

func (proxy *Proxy) forward(client net.Conn) {
	defer proxy.wait.Done()
	ctx, cancel := context.WithTimeout(context.Background(), proxy.dialTimeout)
	backend, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxy.target)
	cancel()
	if err != nil {
		_ = client.Close()
		return
	}
	pair := &connectionPair{client: client, backend: backend}
	proxy.mu.Lock()
	if proxy.closed {
		proxy.mu.Unlock()
		pair.close()
		return
	}
	proxy.active[pair] = struct{}{}
	proxy.mu.Unlock()

	var copies sync.WaitGroup
	copies.Add(2)
	go copyAndCloseWrite(&copies, backend, client)
	go copyAndCloseWrite(&copies, client, backend)
	copies.Wait()
	pair.close()
	proxy.mu.Lock()
	delete(proxy.active, pair)
	proxy.mu.Unlock()
}

func copyAndCloseWrite(wait *sync.WaitGroup, destination net.Conn, source net.Conn) {
	defer wait.Done()
	_, _ = io.Copy(destination, source)
	if tcp, ok := destination.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

// Drain force-closes all streams that were active at the drain boundary.
// New connections remain allowed and must establish a new transport session.
func (proxy *Proxy) Drain() int {
	proxy.mu.Lock()
	pairs := make([]*connectionPair, 0, len(proxy.active))
	for pair := range proxy.active {
		pairs = append(pairs, pair)
	}
	proxy.mu.Unlock()
	for _, pair := range pairs {
		pair.close()
	}
	proxy.drained.Add(int64(len(pairs)))
	return len(pairs)
}

// Active returns the current proxied stream count.
func (proxy *Proxy) Active() int {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	return len(proxy.active)
}

// Accepted returns cumulative Agent-facing physical connections.
func (proxy *Proxy) Accepted() int64 { return proxy.accepted.Load() }

// Drained returns cumulative streams selected by Drain.
func (proxy *Proxy) Drained() int64 { return proxy.drained.Load() }

// Close stops accepting and closes all current streams.
func (proxy *Proxy) Close() error {
	proxy.mu.Lock()
	if proxy.closed {
		proxy.mu.Unlock()
		return nil
	}
	proxy.closed = true
	proxy.mu.Unlock()
	err := proxy.listener.Close()
	proxy.Drain()
	proxy.wait.Wait()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

type connectionPair struct {
	client  net.Conn
	backend net.Conn
	once    sync.Once
}

func (pair *connectionPair) close() {
	pair.once.Do(func() {
		_ = pair.client.Close()
		_ = pair.backend.Close()
	})
}
