package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/gateway"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/delivery"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/messaging/natsjs"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-agent-gateway", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	listenAddress := set.String("listen", ":9443", "Agent gRPC listen address")
	caPath := set.String("tls-client-ca", os.Getenv("KIM_GATEWAY_AGENT_CA"), "Agent client CA bundle")
	certPath := set.String("tls-cert", os.Getenv("KIM_GATEWAY_TLS_CERT"), "Gateway server certificate")
	keyPath := set.String("tls-key", os.Getenv("KIM_GATEWAY_TLS_KEY"), "Gateway server private key")
	natsURL := set.String("nats-url", os.Getenv("KIM_NATS_URL"), "internal NATS URL")
	natsCredentials := set.String("nats-credentials", os.Getenv("KIM_NATS_CREDENTIALS"), "NATS user credentials file")
	natsCA := set.String("nats-tls-ca", os.Getenv("KIM_NATS_TLS_CA"), "NATS server CA bundle")
	streamName := set.String("nats-stream", "KIM_AGENT_COMMAND", "provisioned JetStream name")
	consumerName := set.String("nats-consumer", "kim-agent-gateway-command-v1", "provisioned durable consumer name")
	applicationAdmission := set.Int("session-admission-limit", 32, "maximum concurrent PostgreSQL Session Grant transactions")
	tlsAdmission := set.Int("tls-handshake-limit", 16, "maximum concurrent pre-auth TLS handshakes")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-agent-gateway %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" || *caPath == "" || *certPath == "" || *keyPath == "" || !strings.HasPrefix(*natsURL, "tls://") || *natsCredentials == "" || *natsCA == "" || *streamName == "" || *consumerName == "" || *applicationAdmission < 1 || *tlsAdmission < 1 {
		fmt.Fprintln(stderr, "kim-agent-gateway configuration error: PostgreSQL, mTLS, NATS, stream/consumer, and positive limits are required")
		return 2
	}
	tlsConfig, err := loadServerTLS(*caPath, *certPath, *keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "kim-agent-gateway TLS error: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Open(ctx, *databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "kim-agent-gateway PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	natsConnection, err := nats.Connect(*natsURL, nats.Name("kim-agent-gateway-command-consumer"), nats.UserCredentials(*natsCredentials), nats.RootCAs(*natsCA), nats.NoEcho(), nats.MaxReconnects(-1))
	if err != nil {
		fmt.Fprintf(stderr, "kim-agent-gateway NATS error: %v\n", err)
		return 1
	}
	defer natsConnection.Close()
	js, err := jetstream.New(natsConnection)
	if err != nil {
		fmt.Fprintf(stderr, "kim-agent-gateway JetStream error: %v\n", err)
		return 1
	}
	commandConsumer, err := js.Consumer(ctx, *streamName, *consumerName)
	if err != nil {
		fmt.Fprintf(stderr, "kim-agent-gateway JetStream consumer error: %v\n", err)
		return 1
	}
	registry := gateway.NewOutboundRegistry()
	applicationLimiter, err := gateway.NewAdmissionLimiter(*applicationAdmission)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	handshakeLimiter, err := gateway.NewHandshakeLimiter(*tlsAdmission)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	limitedCredentials, err := gateway.NewLimitedTransportCredentials(credentials.NewTLS(tlsConfig), handshakeLimiter)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintf(stderr, "kim-agent-gateway listen error: %v\n", err)
		return 1
	}
	defer listener.Close()
	grpcServer := grpc.NewServer(grpc.Creds(limitedCredentials))
	agentprotocolv1.RegisterAgentTransportServer(grpcServer, gateway.GRPCServer{Authorizer: gateway.PostgresSessionAuthorizer{DB: pool, Admission: applicationLimiter, RetryAfter: time.Second}, IdentityResolver: gateway.DirectPeerIdentityResolver{}, MessageReceiver: gateway.PostgresMessageReceiver{DB: pool, MaxMessageBytes: 4 << 20}, OutboundRegistry: registry})
	consumer := natsjs.Consumer{Consumer: commandConsumer, Handler: delivery.GatewayHandler{DB: pool, Registry: registry, Consumer: *consumerName, MaxMessageBytes: 4 << 20}, PollWait: time.Second, NakDelay: time.Second}
	errorsFound := make(chan error, 2)
	go func() { errorsFound <- grpcServer.Serve(listener) }()
	go func() { errorsFound <- consumer.Run(ctx) }()
	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		return 0
	case err := <-errorsFound:
		stop()
		grpcServer.Stop()
		if err != nil {
			fmt.Fprintf(stderr, "kim-agent-gateway stopped: %v\n", err)
			return 1
		}
		return 0
	}
}

func loadServerTLS(caPath, certPath, keyPath string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("Agent client CA bundle contains no certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
