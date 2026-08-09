// Command kim-q094-envoy-fixture creates short-lived, non-production PKI and
// Envoy configuration for the Q-094 L7 transport fixture.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type manifest struct {
	EnvoyImage                       string `json:"envoy_image"`
	EnvoyImageDigest                 string `json:"envoy_image_digest"`
	EnvoyUpstreamCertificateSHA256   string `json:"envoy_upstream_certificate_sha256"`
	AgentDownstreamCertificateSHA256 string `json:"agent_downstream_certificate_sha256"`
}

func main() {
	output := flag.String("output-directory", "", "empty directory for short-lived fixture material")
	gatewayAddress := flag.String("gateway-address", "host.docker.internal", "Gateway address visible from Envoy")
	gatewayPort := flag.Int("gateway-port", 55451, "Gateway port visible from Envoy")
	listenerPort := flag.Int("listener-port", 18443, "Envoy downstream Agent listener port")
	adminPort := flag.Int("admin-port", 19000, "Envoy admin port")
	flag.Parse()
	if *output == "" || *gatewayAddress == "" || *gatewayPort < 1 || *listenerPort < 1 || *adminPort < 1 {
		fatal(errors.New("output directory, Gateway address, and positive ports are required"))
	}
	if err := os.MkdirAll(*output, 0o700); err != nil {
		fatal(err)
	}
	now := time.Now().UTC()
	ca, caKey, caPEM, err := createCA(now)
	if err != nil {
		fatal(err)
	}
	gateway, err := issue(ca, caKey, 2, "kim-agent-gateway", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost", "host.docker.internal"}, []net.IP{net.ParseIP("127.0.0.1")}, now)
	if err != nil {
		fatal(err)
	}
	agent, err := issue(ca, caKey, 3, "kim-host-agent", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, nil, now)
	if err != nil {
		fatal(err)
	}
	envoy, err := issue(ca, caKey, 4, "kim-agent-l7-proxy", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, []string{"localhost", "kim-agent-l7-proxy"}, []net.IP{net.ParseIP("127.0.0.1")}, now)
	if err != nil {
		fatal(err)
	}
	files := map[string][]byte{
		"ca.pem": caPEM, "gateway.pem": gateway.certificatePEM, "gateway-key.pem": gateway.keyPEM,
		"agent.pem": agent.certificatePEM, "agent-key.pem": agent.keyPEM,
		"envoy.pem": envoy.certificatePEM, "envoy-key.pem": envoy.keyPEM,
	}
	for name, content := range files {
		mode := os.FileMode(0o600)
		if name == "ca.pem" || filepath.Ext(name) == ".yaml" {
			mode = 0o644
		}
		if err := os.WriteFile(filepath.Join(*output, name), content, mode); err != nil {
			fatal(err)
		}
	}
	configuration := envoyConfiguration(*gatewayAddress, *gatewayPort, *listenerPort, *adminPort)
	if err := os.WriteFile(filepath.Join(*output, "envoy.yaml"), []byte(configuration), 0o644); err != nil {
		fatal(err)
	}
	record := manifest{
		EnvoyImage: "envoyproxy/envoy:v1.38.0", EnvoyImageDigest: "sha256:8146b97ee61a42cd216514709e4e3198af75f014974e3d9f310aef9c901fcbdf",
		EnvoyUpstreamCertificateSHA256: envoy.sha256, AgentDownstreamCertificateSHA256: agent.sha256,
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(*output, "manifest.json"), encoded, 0o644); err != nil {
		fatal(err)
	}
	_, _ = os.Stdout.Write(encoded)
}

type issuedCertificate struct {
	certificatePEM []byte
	keyPEM         []byte
	sha256         string
}

func createCA(now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KIM Q-094 Envoy Test Root"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), err
}

func issue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, commonName string, usages []x509.ExtKeyUsage, dnsNames []string, ipAddresses []net.IP, now time.Time) (issuedCertificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return issuedCertificate{}, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: commonName}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages, DNSNames: dnsNames, IPAddresses: ipAddresses}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return issuedCertificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return issuedCertificate{}, err
	}
	digest := sha256.Sum256(der)
	return issuedCertificate{certificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), sha256: hex.EncodeToString(digest[:])}, nil
}

func envoyConfiguration(gatewayAddress string, gatewayPort, listenerPort, adminPort int) string {
	return fmt.Sprintf(`admin:
  address:
    socket_address: { address: 0.0.0.0, port_value: %d }
static_resources:
  listeners:
  - name: kim_agent_l7
    address:
      socket_address: { address: 0.0.0.0, port_value: %d }
    filter_chains:
    - transport_socket:
        name: envoy.transport_sockets.tls
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
          require_client_certificate: true
          common_tls_context:
            alpn_protocols: ["h2"]
            tls_params:
              tls_minimum_protocol_version: TLSv1_3
              tls_maximum_protocol_version: TLSv1_3
            tls_certificates:
            - certificate_chain: { filename: /fixture/envoy.pem }
              private_key: { filename: /fixture/envoy-key.pem }
            validation_context:
              trusted_ca: { filename: /fixture/ca.pem }
      filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: kim_agent_l7
          codec_type: HTTP2
          drain_timeout: 1s
          stream_idle_timeout: 0s
          forward_client_cert_details: SANITIZE_SET
          set_current_client_cert_details:
            subject: true
            format: JSON
          route_config:
            name: kim_agent_route
            virtual_hosts:
            - name: kim_agent_gateway
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { cluster: kim_agent_gateway, timeout: 0s }
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
  - name: kim_agent_gateway
    type: STRICT_DNS
    dns_lookup_family: V4_ONLY
    connect_timeout: 2s
    load_assignment:
      cluster_name: kim_agent_gateway
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: %s, port_value: %d }
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    transport_socket:
      name: envoy.transport_sockets.tls
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
        sni: host.docker.internal
        common_tls_context:
          alpn_protocols: ["h2"]
          tls_params:
            tls_minimum_protocol_version: TLSv1_3
            tls_maximum_protocol_version: TLSv1_3
          tls_certificates:
          - certificate_chain: { filename: /fixture/envoy.pem }
            private_key: { filename: /fixture/envoy-key.pem }
          validation_context:
            trusted_ca: { filename: /fixture/ca.pem }
            match_typed_subject_alt_names:
            - san_type: DNS
              matcher: { exact: host.docker.internal }
`, adminPort, listenerPort, gatewayAddress, gatewayPort)
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
