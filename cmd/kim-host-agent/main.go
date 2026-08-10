package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/hostruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/reconnect"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/grpcstream"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-host-agent", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	gateway := set.String("gateway", os.Getenv("KIM_AGENT_GATEWAY_TARGET"), "Agent Gateway gRPC target")
	hostID := set.String("host-id", os.Getenv("KIM_HOST_ID"), "approved KIM Host identity")
	serverName := set.String("tls-server-name", os.Getenv("KIM_AGENT_GATEWAY_SERVER_NAME"), "Gateway TLS server name")
	caPath := set.String("tls-ca", os.Getenv("KIM_AGENT_TLS_CA"), "Agent trust bundle path")
	certPath := set.String("tls-cert", os.Getenv("KIM_AGENT_TLS_CERT"), "Agent certificate path")
	keyPath := set.String("tls-key", os.Getenv("KIM_AGENT_TLS_KEY"), "Agent private key path")
	artifactDigest := set.String("artifact-digest", os.Getenv("KIM_AGENT_ARTIFACT_DIGEST"), "Agent artifact SHA-256")
	verifierDigest := set.String("verifier-digest", os.Getenv("KIM_AGENT_VERIFIER_DIGEST"), "Verifier artifact SHA-256")
	credentialRevision := set.Int64("credential-binding-revision", 1, "current Credential Binding revision")
	stateRoot := set.String("state-root", "/var/lib/kvm-infrastructure-manager/agent", "private Agent state root")
	libvirtURI := set.String("libvirt-uri", os.Getenv("KIM_AGENT_LIBVIRT_URI"), "standard libvirt connection URI; empty disables VM power operations")
	localLVMVGUUID := set.String("local-lvm-vg-uuid", os.Getenv("KIM_AGENT_LOCAL_LVM_VG_UUID"), "allowed Local LVM VG UUID; empty disables Local LVM realization")
	localLVMVGName := set.String("local-lvm-vg-name", os.Getenv("KIM_AGENT_LOCAL_LVM_VG_NAME"), "admin-configured Local LVM VG name paired with the allowed UUID")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-host-agent %s\n", componentmain.Version)
		return 0
	}
	tlsConfig, err := loadTLS(*caPath, *certPath, *keyPath, *serverName)
	if err != nil || *gateway == "" || *hostID == "" || len(*artifactDigest) != 64 || len(*verifierDigest) != 64 || *credentialRevision < 1 {
		fmt.Fprintf(stderr, "kim-host-agent configuration error: %v\n", errorsOrRequired(err))
		return 2
	}
	limits := session.QueueLimits{MaxMessageBytes: 4 << 20, MaxTotalMessages: 1024, MaxTotalBytes: 64 << 20, ReservedPriorityMsgs: 128, ReservedPriorityBytes: 4 << 20, MaxConsecutivePriority: 32, PerStreamMessages: map[session.Stream]int{session.StreamControl: 128, session.StreamCommand: 256, session.StreamResult: 256, session.StreamHeartbeat: 128, session.StreamCredential: 128, session.StreamInventory: 128, session.StreamResync: 128}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var executionBackends []agentexecution.Backend
	if *libvirtURI != "" {
		backend, closeBackend, backendErr := libvirtdomain.New(*libvirtURI)
		if backendErr != nil {
			fmt.Fprintf(stderr, "kim-host-agent libvirt error: %v\n", backendErr)
			return 2
		}
		defer closeBackend()
		executionBackends = append(executionBackends, backend)
	}
	if (*localLVMVGUUID == "") != (*localLVMVGName == "") {
		fmt.Fprintln(stderr, "kim-host-agent Local LVM error: VG UUID and VG name must be configured together")
		return 2
	}
	if *localLVMVGUUID != "" {
		client, clientErr := locallvm.NewCLIClient()
		if clientErr != nil {
			fmt.Fprintf(stderr, "kim-host-agent Local LVM error: %v\n", clientErr)
			return 2
		}
		volumeGroups := map[string]string{*localLVMVGUUID: *localLVMVGName}
		executionBackends = append(executionBackends, locallvm.Backend{Client: client, VolumeGroups: volumeGroups})
		if *libvirtURI != "" {
			attachmentBackend, closeAttachmentBackend, attachmentErr := libvirtvolume.New(*libvirtURI, client, volumeGroups)
			if attachmentErr != nil {
				fmt.Fprintf(stderr, "kim-host-agent libvirt Volume error: %v\n", attachmentErr)
				return 2
			}
			defer closeAttachmentBackend()
			executionBackends = append(executionBackends, attachmentBackend)
			vmBackend, closeVMBackend, vmErr := libvirtvm.New(*libvirtURI, libvirtvolume.LocalLVMResolver{Client: client, VolumeGroups: volumeGroups})
			if vmErr != nil {
				fmt.Fprintf(stderr, "kim-host-agent libvirt VM error: %v\n", vmErr)
				return 2
			}
			defer closeVMBackend()
			executionBackends = append(executionBackends, vmBackend)
		}
	}
	err = hostruntime.Run(ctx, hostruntime.Config{HostID: *hostID, ProtocolVersion: "v1", AgentArtifactDigest: *artifactDigest, CredentialBindingRevision: *credentialRevision, VerifierDigest: *verifierDigest, StateDirectory: filepath.Join(*stateRoot, "qualification-state"), SpoolDirectory: filepath.Join(*stateRoot, "spool"), JournalDirectory: filepath.Join(*stateRoot, "execution-journal"), GenerationDirectory: filepath.Join(*stateRoot, "session-generation"), Adapter: &grpcstream.Adapter{Target: *gateway, TLSConfig: tlsConfig, MaxMessageBytes: limits.MaxMessageBytes}, QueueLimits: limits, SpoolMaxEntries: 4096, SpoolMaxBytes: 256 << 20, FlushInterval: 10 * time.Millisecond, ReconnectBackoff: reconnect.Backoff{Base: 250 * time.Millisecond, Max: 30 * time.Second}, ExecutionBackends: executionBackends})
	if err != nil {
		fmt.Fprintf(stderr, "kim-host-agent stopped: %v\n", err)
		return 1
	}
	return 0
}

func loadTLS(caPath, certPath, keyPath, serverName string) (*tls.Config, error) {
	if caPath == "" || certPath == "" || keyPath == "" || serverName == "" {
		return nil, fmt.Errorf("Gateway, Host identity, TLS CA/certificate/key/server-name are required")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("Agent trust bundle contains no certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: serverName}, nil
}

func errorsOrRequired(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("Gateway and Host identity are required")
}
