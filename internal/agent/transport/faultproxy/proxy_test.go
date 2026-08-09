package faultproxy

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestDrainClosesCurrentStreamAndAllowsReconnect(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	go serveEcho(backend)

	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := New(front, backend.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := proxy.Serve(); err != nil {
			t.Errorf("Serve: %v", err)
		}
	}()
	defer func() { _ = proxy.Close() }()

	first := dialAndEcho(t, proxy.Addr().String(), "first")
	if drained := proxy.Drain(); drained != 1 {
		t.Fatalf("Drain() = %d, want 1", drained)
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := bufio.NewReader(first).ReadByte(); err == nil {
		t.Fatal("drained connection remained readable")
	}
	_ = first.Close()

	second := dialAndEcho(t, proxy.Addr().String(), "second")
	_ = second.Close()
	if proxy.Accepted() != 2 || proxy.Drained() != 1 {
		t.Fatalf("accepted=%d drained=%d, want 2/1", proxy.Accepted(), proxy.Drained())
	}
}

func dialAndEcho(t *testing.T, address, value string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := fmt.Fprintln(connection, value); err != nil {
		t.Fatal(err)
	}
	received, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || received != value+"\n" {
		t.Fatalf("echo=%q err=%v, want %q", received, err, value+"\n")
	}
	_ = connection.SetDeadline(time.Time{})
	return connection
}

func serveEcho(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = connection.Close() }()
			_, _ = bufio.NewReader(connection).WriteTo(connection)
		}()
	}
}
