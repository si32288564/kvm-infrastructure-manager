package natsjs

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/delivery"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type scriptedHandler struct {
	mu          sync.Mutex
	disposition delivery.ConsumeDisposition
	calls       int
	called      chan struct{}
	release     chan struct{}
}

func (handler *scriptedHandler) Handle(ctx context.Context, messageID string, payload []byte) (delivery.ConsumeDisposition, error) {
	if messageID == "" || len(payload) == 0 {
		return delivery.ConsumeTerm, fmt.Errorf("missing stable message identity")
	}
	handler.mu.Lock()
	handler.calls++
	disposition := handler.disposition
	handler.mu.Unlock()
	select {
	case handler.called <- struct{}{}:
	default:
	}
	if handler.release != nil {
		select {
		case <-handler.release:
		case <-ctx.Done():
			return delivery.ConsumeNak, ctx.Err()
		}
	}
	return disposition, nil
}

func (handler *scriptedHandler) callCount() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.calls
}

func TestJetStreamClusterLeaderFailoverAndDurableConsumerRestart(t *testing.T) {
	servers := startJetStreamCluster(t, 3)
	t.Log("cluster formed")
	defer func() {
		for _, instance := range servers {
			if instance != nil {
				instance.Shutdown()
			}
		}
	}()

	urls := make([]string, 0, len(servers))
	serverByName := make(map[string]*server.Server)
	urlByName := make(map[string]string)
	for _, instance := range servers {
		urls = append(urls, instance.ClientURL())
		serverByName[instance.Name()] = instance
		urlByName[instance.Name()] = instance.ClientURL()
	}
	connection, err := nats.Connect(strings.Join(urls, ","), nats.UserInfo("kim", "fixture-secret"), nats.Name("kim-p1b-cluster-fixture"), nats.MaxReconnects(-1), nats.ReconnectWait(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "KIM_AGENT_COMMAND", Subjects: []string{delivery.Subject}, Storage: jetstream.FileStorage, Replicas: 3, Duplicates: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{Name: "kim-agent-gateway-command-v1", Durable: "kim-agent-gateway-command-v1", AckPolicy: jetstream.AckExplicitPolicy, AckWait: 250 * time.Millisecond, MaxDeliver: 16, Replicas: 3})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() bool {
		streamInfo, streamErr := stream.Info(ctx)
		consumerInfo, consumerErr := consumer.Info(ctx)
		return streamErr == nil && consumerErr == nil && clusterReplicasCurrent(streamInfo.Cluster, 2) && clusterReplicasCurrent(consumerInfo.Cluster, 2)
	}, "stream and consumer replicas did not become current before fault injection")
	t.Log("stream and consumer replicas current")

	publisher := Publisher{JetStream: js}
	payload := []byte(`{"schema_version":"fixture","payload":"stable"}`)
	firstAck, err := publisher.Publish(ctx, delivery.Subject, "command-delivery/fixture/1", payload)
	if err != nil || firstAck.Duplicate {
		t.Fatalf("first publish = %#v, %v", firstAck, err)
	}
	duplicateAck, err := publisher.Publish(ctx, delivery.Subject, "command-delivery/fixture/1", payload)
	if err != nil || !duplicateAck.Duplicate || duplicateAck.Sequence != firstAck.Sequence {
		t.Fatalf("duplicate publish = %#v, %v; first=%#v", duplicateAck, err, firstAck)
	}
	t.Log("stable message deduplicated")

	info, err := stream.Info(ctx)
	if err != nil || info.Cluster == nil || info.Cluster.Leader == "" {
		t.Fatalf("stream leader info = %#v, %v", info, err)
	}
	oldLeader := info.Cluster.Leader
	leaderServer := serverByName[oldLeader]
	if leaderServer == nil {
		t.Fatalf("stream leader %q not found", oldLeader)
	}
	leaderServer.Shutdown()
	serverByName[oldLeader] = nil
	survivingURLs := make([]string, 0, 2)
	for name, instance := range serverByName {
		if instance != nil {
			survivingURLs = append(survivingURLs, urlByName[name])
		}
	}
	failoverConnection, err := nats.Connect(strings.Join(survivingURLs, ","), nats.UserInfo("kim", "fixture-secret"), nats.Name("kim-p1b-cluster-fixture-restarted"), nats.MaxReconnects(-1), nats.ReconnectWait(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer failoverConnection.Close()
	failoverJS, err := jetstream.New(failoverConnection)
	if err != nil {
		t.Fatal(err)
	}
	var failoverStream jetstream.Stream
	var lastFailoverInfo *jetstream.StreamInfo
	var lastFailoverErr error
	if !waitFor(10*time.Second, func() bool {
		attemptContext, stopAttempt := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stopAttempt()
		failoverStream, err = failoverJS.Stream(attemptContext, "KIM_AGENT_COMMAND")
		if err != nil {
			lastFailoverErr = err
			return false
		}
		lastFailoverInfo, lastFailoverErr = failoverStream.Info(attemptContext)
		return lastFailoverErr == nil && lastFailoverInfo.Cluster != nil && lastFailoverInfo.Cluster.Leader != "" && lastFailoverInfo.Cluster.Leader != oldLeader && lastFailoverInfo.State.Msgs == 1
	}) {
		t.Fatalf("replicated stream did not elect a new leader with one deduplicated message: info=%#v err=%v connection=%s", lastFailoverInfo, lastFailoverErr, failoverConnection.Status())
	}
	t.Log("stream leader failover complete")
	consumer, err = failoverStream.Consumer(ctx, "kim-agent-gateway-command-v1")
	if err != nil {
		t.Fatal(err)
	}

	firstHandler := &scriptedHandler{disposition: delivery.ConsumeNak, called: make(chan struct{}, 1), release: make(chan struct{})}
	firstContext, stopFirst := context.WithCancel(ctx)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- (Consumer{Consumer: consumer, Handler: firstHandler, PollWait: 50 * time.Millisecond, NakDelay: 2 * time.Second}).Run(firstContext)
	}()
	select {
	case <-firstHandler.called:
	case <-ctx.Done():
		t.Fatal("first durable consumer did not receive the message")
	}
	eventually(t, 5*time.Second, func() bool {
		consumerInfo, err := consumer.Info(ctx)
		return err == nil && consumerInfo.NumAckPending == 1
	}, "first delivery did not become ack-pending")
	close(firstHandler.release)
	time.Sleep(100 * time.Millisecond)
	t.Log("first consumer returned NAK")
	stopFirst()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Gateway consumer stop: %v", err)
	}
	t.Log("first consumer stopped")

	restartedConsumer, err := failoverJS.Consumer(ctx, "KIM_AGENT_COMMAND", "kim-agent-gateway-command-v1")
	if err != nil {
		t.Fatal(err)
	}
	secondHandler := &scriptedHandler{disposition: delivery.ConsumeAck, called: make(chan struct{}, 1)}
	secondContext, stopSecond := context.WithCancel(ctx)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- (Consumer{Consumer: restartedConsumer, Handler: secondHandler, PollWait: 50 * time.Millisecond, NakDelay: 25 * time.Millisecond}).Run(secondContext)
	}()
	select {
	case <-secondHandler.called:
	case <-ctx.Done():
		t.Fatal("restarted durable consumer did not receive the redelivery")
	}
	t.Log("restarted consumer received redelivery")
	eventually(t, 5*time.Second, func() bool {
		consumerInfo, err := restartedConsumer.Info(ctx)
		return err == nil && consumerInfo.NumAckPending == 0 && consumerInfo.NumPending == 0
	}, "restarted consumer did not durably acknowledge the redelivery")
	t.Log("redelivery durably acknowledged")
	stopSecond()
	if err := <-secondDone; err != nil {
		t.Fatalf("second Gateway consumer stop: %v", err)
	}
	if firstHandler.callCount() != 1 || secondHandler.callCount() != 1 {
		t.Fatalf("delivery calls before/after restart = %d/%d", firstHandler.callCount(), secondHandler.callCount())
	}
}

func startJetStreamCluster(t *testing.T, count int) []*server.Server {
	t.Helper()
	ports := reservePorts(t, count)
	routes := make([]string, count)
	for index, port := range ports {
		routes[index] = fmt.Sprintf("nats-route://127.0.0.1:%d", port)
	}
	instances := make([]*server.Server, 0, count)
	for index, port := range ports {
		store := filepath.Join(t.TempDir(), fmt.Sprintf("node-%d", index+1))
		if err := os.MkdirAll(store, 0o700); err != nil {
			t.Fatal(err)
		}
		otherRoutes := make([]string, 0, count-1)
		for routeIndex, route := range routes {
			if routeIndex != index {
				otherRoutes = append(otherRoutes, fmt.Sprintf("%q", route))
			}
		}
		config := fmt.Sprintf(`
listen: 127.0.0.1:-1
server_name: KIM-NATS-%d
jetstream: {store_dir: %q, max_mem_store: 64MB, max_file_store: 256MB}
cluster {name: KIM-P1B, listen: 127.0.0.1:%d, routes: [%s]}
accounts {
  KIM {users: [{user: kim, password: fixture-secret}], jetstream: enabled}
  SYS {users: [{user: sys, password: fixture-system}]}
}
system_account: SYS
`, index+1, store, port, strings.Join(otherRoutes, ","))
		configPath := filepath.Join(t.TempDir(), fmt.Sprintf("nats-%d.conf", index+1))
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		options, err := server.ProcessConfigFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		options.NoLog, options.NoSigs = true, true
		instance, err := server.NewServer(options)
		if err != nil {
			t.Fatal(err)
		}
		go instance.Start()
		if !instance.ReadyForConnections(10 * time.Second) {
			t.Fatal("NATS server did not become ready")
		}
		instances = append(instances, instance)
	}
	t.Cleanup(func() {
		for _, instance := range instances {
			instance.Shutdown()
		}
	})
	eventually(t, 10*time.Second, func() bool {
		peerReady := false
		for _, instance := range instances {
			if instance.NumRoutes() < count-1 {
				return false
			}
			if len(instance.JetStreamClusterPeers()) == count {
				peerReady = true
			}
		}
		return peerReady
	}, "JetStream cluster did not form")
	return instances
}

func reservePorts(t *testing.T, count int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return ports
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	if !waitFor(timeout, condition) {
		t.Fatal(message)
	}
}

func waitFor(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func clusterReplicasCurrent(info *jetstream.ClusterInfo, want int) bool {
	if info == nil || info.Leader == "" || len(info.Replicas) != want {
		return false
	}
	for _, replica := range info.Replicas {
		if !replica.Current {
			return false
		}
	}
	return true
}
