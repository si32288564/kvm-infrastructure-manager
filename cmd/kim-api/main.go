package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/auth"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/availabilitypolicy"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/flavor"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-api", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	listenAddress := set.String("listen", ":8443", "Northbound HTTPS listen address")
	issuer := set.String("oidc-issuer", os.Getenv("KIM_API_OIDC_ISSUER"), "accepted OIDC issuer")
	audience := set.String("oidc-audience", os.Getenv("KIM_API_OIDC_AUDIENCE"), "required access-token audience")
	jwksFile := set.String("oidc-jwks-file", os.Getenv("KIM_API_OIDC_JWKS_FILE"), "rotatable OIDC JWKS file")
	tlsCertificate := set.String("tls-cert", os.Getenv("KIM_API_TLS_CERT"), "Northbound TLS certificate")
	tlsKey := set.String("tls-key", os.Getenv("KIM_API_TLS_KEY"), "Northbound TLS private key")
	insecureHTTP := set.Bool("insecure-http", false, "explicit development-only cleartext listener; authentication remains required")
	requestTimeout := set.Duration("request-timeout", 10*time.Second, "maximum request duration")
	shutdownTimeout := set.Duration("shutdown-timeout", 10*time.Second, "graceful shutdown bound")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-api %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" || *listenAddress == "" || *issuer == "" || *audience == "" || *jwksFile == "" || *requestTimeout <= 0 || *shutdownTimeout <= 0 || (!*insecureHTTP && (*tlsCertificate == "" || *tlsKey == "")) {
		fmt.Fprintln(stderr, "kim-api configuration error: PostgreSQL, OIDC issuer/audience/JWKS, positive timeouts, and TLS are required; cleartext requires explicit --insecure-http")
		return 2
	}
	verifier, err := auth.NewFileJWKSVerifier(*issuer, *audience, *jwksFile)
	if err != nil {
		fmt.Fprintf(stderr, "kim-api OIDC configuration error: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Open(ctx, *databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "kim-api PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	store := postgres.NorthboundProjectStore{DB: pool}
	flavorStore := postgres.NorthboundFlavorStore{DB: pool}
	availabilityPolicyStore := postgres.NorthboundAvailabilityPolicyStore{DB: pool}
	if err := store.Ready(ctx); err != nil {
		fmt.Fprintf(stderr, "kim-api readiness error: %v\n", err)
		return 1
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintf(stderr, "kim-api listen error: %v\n", err)
		return 1
	}
	defer listener.Close()
	handler := httpapi.Server{Projects: project.Service{Store: store}, Flavors: flavor.Service{Store: flavorStore}, AvailabilityPolicies: availabilitypolicy.Service{Store: availabilityPolicyStore}, Authenticator: verifier, Logger: stderr, RequestTimeout: *requestTimeout}.Handler()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	errorsFound := make(chan error, 1)
	go func() {
		if *insecureHTTP {
			errorsFound <- server.Serve(listener)
			return
		}
		errorsFound <- server.ServeTLS(listener, *tlsCertificate, *tlsKey)
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			fmt.Fprintf(stderr, "kim-api graceful shutdown error: %v\n", err)
			return 1
		}
		return 0
	case err := <-errorsFound:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "kim-api stopped: %v\n", err)
			return 1
		}
		return 0
	}
}
