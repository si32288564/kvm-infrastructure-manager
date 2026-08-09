// Command kim-agent-transport-scale runs the non-certifying Q-094 scale fixture.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/grpcstream"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/http2stream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type result struct {
	Candidate               string        `json:"candidate"`
	Sessions                int           `json:"sessions"`
	Concurrency             int           `json:"concurrency"`
	OpenDuration            time.Duration `json:"open_duration_ns"`
	ReconnectDuration       time.Duration `json:"reconnect_duration_ns"`
	EchoP50                 time.Duration `json:"echo_p50_ns"`
	EchoP95                 time.Duration `json:"echo_p95_ns"`
	EchoP99                 time.Duration `json:"echo_p99_ns"`
	GoroutinesAtIdle        int           `json:"goroutines_at_idle"`
	HeapAllocAtIdle         uint64        `json:"heap_alloc_at_idle_bytes"`
	HeapSysAtIdle           uint64        `json:"heap_sys_at_idle_bytes"`
	OpenFileDescriptors     int           `json:"open_file_descriptors"`
	AcceptedConnections     int64         `json:"accepted_connections_total"`
	ActiveConnections       int           `json:"active_connections"`
	OldConnectionsDrained   bool          `json:"old_connections_drained_before_reconnect"`
	GarbageCollectionCycles uint32        `json:"gc_cycles"`
}

type fixture struct {
	adapter  session.TransportAdapter
	listener *contracttest.CountingListener
	close    func()
}

func main() {
	candidate := flag.String("candidate", "grpc", "grpc or http2")
	sessions := flag.Int("sessions", 1000, "concurrent long-lived sessions")
	concurrency := flag.Int("concurrency", 100, "parallel open/echo workers")
	flag.Parse()
	if *sessions < 1 || *concurrency < 1 {
		fatal(errors.New("sessions and concurrency must be positive"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fixture, err := newFixture(*candidate)
	if err != nil {
		fatal(err)
	}
	defer fixture.close()

	connections, openDuration, err := openSessions(ctx, fixture.adapter, *sessions, *concurrency, 1)
	if err != nil {
		fatal(err)
	}
	latencies, err := echoAll(ctx, connections, *concurrency, 1)
	if err != nil {
		closeAll(connections)
		fatal(err)
	}
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	idleGoroutines := runtime.NumGoroutine()
	openFDs := countFileDescriptors()
	reconnectStarted := time.Now()
	closeAll(connections)
	drained := waitForActive(fixture.listener, 0, 5*time.Second)
	reconnected, _, err := openSessions(ctx, fixture.adapter, *sessions, *concurrency, 2)
	if err != nil {
		fatal(err)
	}
	defer closeAll(reconnected)
	if _, err := echoAll(ctx, reconnected, *concurrency, 2); err != nil {
		fatal(err)
	}

	output := result{
		Candidate:               *candidate,
		Sessions:                *sessions,
		Concurrency:             *concurrency,
		OpenDuration:            openDuration,
		ReconnectDuration:       time.Since(reconnectStarted),
		EchoP50:                 percentile(latencies, 50),
		EchoP95:                 percentile(latencies, 95),
		EchoP99:                 percentile(latencies, 99),
		GoroutinesAtIdle:        idleGoroutines,
		HeapAllocAtIdle:         memory.HeapAlloc,
		HeapSysAtIdle:           memory.HeapSys,
		OpenFileDescriptors:     openFDs,
		AcceptedConnections:     fixture.listener.Accepted(),
		ActiveConnections:       fixture.listener.Active(),
		OldConnectionsDrained:   drained,
		GarbageCollectionCycles: memory.NumGC,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fatal(err)
	}
}

func newFixture(candidate string) (*fixture, error) {
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		return nil, err
	}
	switch candidate {
	case "grpc":
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		counting := contracttest.NewCountingListener(listener)
		server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
		agentprotocolv1.RegisterAgentTransportServer(server, grpcstream.EchoServer{})
		go func() { _ = server.Serve(counting) }()
		return &fixture{
			adapter:  &grpcstream.Adapter{Target: counting.Addr().String(), TLSConfig: clientTLS, MaxMessageBytes: 512 * 1024},
			listener: counting,
			close:    server.Stop,
		}, nil
	case "http2":
		server := httptest.NewUnstartedServer(http2stream.EchoHandler{MaxMessageBytes: 512 * 1024})
		counting := contracttest.NewCountingListener(server.Listener)
		server.Listener = counting
		server.TLS = serverTLS
		server.EnableHTTP2 = true
		server.StartTLS()
		return &fixture{
			adapter:  &http2stream.Adapter{Endpoint: server.URL, TLSConfig: clientTLS, MaxMessageBytes: 512 * 1024},
			listener: counting,
			close:    server.Close,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported candidate %q", candidate)
	}
}

func openSessions(ctx context.Context, adapter session.TransportAdapter, count, concurrency int, generation uint64) ([]session.TransportConnection, time.Duration, error) {
	connections := make([]session.TransportConnection, count)
	started := time.Now()
	err := parallel(count, concurrency, func(index int) error {
		connection, openErr := adapter.Open(ctx, session.Handshake{
			HostIdentity:      fmt.Sprintf("host-%06d", index),
			SessionGeneration: generation,
			ProtocolVersion:   "v1",
			Capabilities:      []string{"kim.agent.libvirt.v1", "kim.agent.storage.v1", "kim.agent.network.v1", "kim.agent.clock.v1", "kim.agent.compliance.v1"},
		})
		connections[index] = connection
		return openErr
	})
	if err != nil {
		closeAll(connections)
	}
	return connections, time.Since(started), err
}

func echoAll(ctx context.Context, connections []session.TransportConnection, concurrency int, generation uint64) ([]time.Duration, error) {
	latencies := make([]time.Duration, len(connections))
	err := parallel(len(connections), concurrency, func(index int) error {
		envelope := session.NewEnvelope(fmt.Sprintf("host-%06d", index), generation, session.StreamResult, fmt.Sprintf("result-%d", index), "v1", fmt.Sprintf("attempt-%d", index), 1, []byte("scale-echo"))
		started := time.Now()
		if err := connections[index].Send(ctx, envelope); err != nil {
			return err
		}
		received, err := connections[index].Receive(ctx)
		latencies[index] = time.Since(started)
		if err != nil {
			return err
		}
		if received.MessageID != envelope.MessageID {
			return fmt.Errorf("host %d received message %q, want %q", index, received.MessageID, envelope.MessageID)
		}
		return nil
	})
	return latencies, err
}

func parallel(count, concurrency int, function func(int) error) error {
	indices := make(chan int)
	errorsChannel := make(chan error, 1)
	var workers sync.WaitGroup
	for worker := 0; worker < min(count, concurrency); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indices {
				if err := function(index); err != nil {
					select {
					case errorsChannel <- err:
					default:
					}
				}
			}
		}()
	}
	for index := 0; index < count; index++ {
		indices <- index
	}
	close(indices)
	workers.Wait()
	select {
	case err := <-errorsChannel:
		return err
	default:
		return nil
	}
}

func closeAll(connections []session.TransportConnection) {
	for _, connection := range connections {
		if connection != nil {
			_ = connection.Close()
		}
	}
}

func percentile(values []time.Duration, percentage int) time.Duration {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	index := (len(sorted)*percentage + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}

func countFileDescriptors() int {
	for _, path := range []string{"/dev/fd", "/proc/self/fd"} {
		entries, err := os.ReadDir(path)
		if err == nil {
			return len(entries)
		}
	}
	output, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(os.Getpid()), "-Fn").Output()
	if err == nil {
		count := 0
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "f") {
				count++
			}
		}
		return count
	}
	return -1
}

func waitForActive(listener *contracttest.CountingListener, target int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if listener.Active() <= target {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return listener.Active() <= target
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
