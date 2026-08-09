package p1bfault

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

type fixturePKI struct {
	caPath, gatewayCert, gatewayKey, agentCert, agentKey, natsCert, natsKey string
	agentFingerprint                                                        string
}

type fixtureNATSIdentity struct {
	operatorJWT, systemAccount, systemJWT, accountJWT, accountPublic, credsPath string
}

type fixtureProcess struct {
	name string
	cmd  *exec.Cmd
	log  string
	done chan error
	once sync.Once
}

func buildFixtureBinary(t *testing.T, root, output, target string) string {
	t.Helper()
	path := filepath.Join(output, filepath.Base(target))
	command := exec.Command("go", "build", "-o", path, target)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", target, err, output)
	}
	return path
}

func startFixtureProcess(t *testing.T, name, binary, logPath string, args ...string) *fixtureProcess {
	t.Helper()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, args...)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start %s: %v", name, err)
	}
	process := &fixtureProcess{name: name, cmd: command, log: logPath, done: make(chan error, 1)}
	go func() {
		process.done <- command.Wait()
		_ = logFile.Close()
	}()
	t.Cleanup(func() { process.stop(t) })
	return process
}

func (process *fixtureProcess) stop(t *testing.T) {
	t.Helper()
	process.once.Do(func() {
		if process.cmd.Process == nil {
			return
		}
		select {
		case <-process.done:
			return
		default:
		}
		_ = process.cmd.Process.Signal(os.Interrupt)
		select {
		case <-process.done:
		case <-time.After(3 * time.Second):
			_ = process.cmd.Process.Kill()
			<-process.done
		}
	})
}

func (process *fixtureProcess) kill(t *testing.T) {
	t.Helper()
	process.once.Do(func() {
		if process.cmd.Process == nil {
			return
		}
		select {
		case <-process.done:
			return
		default:
		}
		if err := process.cmd.Process.Kill(); err != nil {
			t.Fatalf("kill %s: %v", process.name, err)
		}
		<-process.done
	})
}

func (process *fixtureProcess) requireRunning(t *testing.T) {
	t.Helper()
	select {
	case err := <-process.done:
		contents, _ := os.ReadFile(process.log)
		t.Fatalf("%s stopped unexpectedly (%v): %s", process.name, err, contents)
	default:
	}
}

func reserveFixturePorts(t *testing.T, count int) []int {
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
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(message)
}

func generateFixturePKI(t *testing.T, directory string) fixturePKI {
	t.Helper()
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KIM P1-B Qualification CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(directory, "ca.pem")
	writeFixtureFile(t, caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644)
	issue := func(serial int64, name string, usages []x509.ExtKeyUsage) (string, string, string) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: []string{"localhost", name}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: usages}
		der, err := x509.CreateCertificate(rand.Reader, template, caTemplate, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		certPath := filepath.Join(directory, name+".pem")
		keyPath := filepath.Join(directory, name+"-key.pem")
		writeFixtureFile(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
		writeFixtureFile(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)
		digest := sha256.Sum256(der)
		return certPath, keyPath, hex.EncodeToString(digest[:])
	}
	gatewayCert, gatewayKey, _ := issue(2, "kim-agent-gateway", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	agentCert, agentKey, agentFingerprint := issue(3, "host-p1b-process", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	natsCert, natsKey, _ := issue(4, "kim-nats", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth})
	return fixturePKI{caPath: caPath, gatewayCert: gatewayCert, gatewayKey: gatewayKey, agentCert: agentCert, agentKey: agentKey, natsCert: natsCert, natsKey: natsKey, agentFingerprint: agentFingerprint}
}

func generateFixtureNATSIdentity(t *testing.T, directory string) fixtureNATSIdentity {
	t.Helper()
	operatorKey, _ := nkeys.CreateOperator()
	operatorPublic, _ := operatorKey.PublicKey()
	systemKey, _ := nkeys.CreateAccount()
	systemPublic, _ := systemKey.PublicKey()
	accountKey, _ := nkeys.CreateAccount()
	accountPublic, _ := accountKey.PublicKey()
	operatorClaims := jwt.NewOperatorClaims(operatorPublic)
	operatorClaims.SystemAccount = systemPublic
	operatorJWT, err := operatorClaims.Encode(operatorKey)
	if err != nil {
		t.Fatal(err)
	}
	systemClaims := jwt.NewAccountClaims(systemPublic)
	systemJWT, err := systemClaims.Encode(operatorKey)
	if err != nil {
		t.Fatal(err)
	}
	accountClaims := jwt.NewAccountClaims(accountPublic)
	accountClaims.Limits.JetStreamLimits = jwt.JetStreamLimits{MemoryStorage: 64 << 20, DiskStorage: 256 << 20, Streams: 8, Consumer: 16}
	accountJWT, err := accountClaims.Encode(operatorKey)
	if err != nil {
		t.Fatal(err)
	}
	userKey, _ := nkeys.CreateUser()
	userPublic, _ := userKey.PublicKey()
	userClaims := jwt.NewUserClaims(userPublic)
	userClaims.IssuerAccount = accountPublic
	userJWT, err := userClaims.Encode(accountKey)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := userKey.Seed()
	credentials, err := jwt.FormatUserConfig(userJWT, seed)
	if err != nil {
		t.Fatal(err)
	}
	credsPath := filepath.Join(directory, "kim.creds")
	writeFixtureFile(t, credsPath, credentials, 0o600)
	return fixtureNATSIdentity{operatorJWT: operatorJWT, systemAccount: systemPublic, systemJWT: systemJWT, accountJWT: accountJWT, accountPublic: accountPublic, credsPath: credsPath}
}

func writeNATSConfigs(t *testing.T, directory string, routePorts, clientPorts []int, pki fixturePKI, identity fixtureNATSIdentity) []string {
	t.Helper()
	if len(routePorts) != len(clientPorts) {
		t.Fatal("NATS route/client port count mismatch")
	}
	routes := make([]string, len(routePorts))
	for index, port := range routePorts {
		routes[index] = fmt.Sprintf("nats-route://127.0.0.1:%d", port)
	}
	paths := make([]string, len(routePorts))
	for index, routePort := range routePorts {
		otherRoutes := make([]string, 0, len(routePorts)-1)
		for routeIndex, route := range routes {
			if routeIndex != index {
				otherRoutes = append(otherRoutes, fmt.Sprintf("%q", route))
			}
		}
		clientPort := clientPorts[index]
		store := filepath.Join(directory, fmt.Sprintf("nats-store-%d", index+1))
		if err := os.MkdirAll(store, 0o700); err != nil {
			t.Fatal(err)
		}
		config := fmt.Sprintf(`
listen: 127.0.0.1:%d
server_name: KIM-NATS-%d
jetstream: {store_dir: %q, max_mem_store: 64MB, max_file_store: 256MB}
operator: %s
system_account: %s
resolver: MEMORY
resolver_preload: {%s: %s, %s: %s}
tls: {cert_file: %q, key_file: %q, ca_file: %q, timeout: 2}
cluster {name: KIM-P1B-PROCESS, listen: 127.0.0.1:%d, routes: [%s], tls: {cert_file: %q, key_file: %q, ca_file: %q, timeout: 2}}
`, clientPort, index+1, store, identity.operatorJWT, identity.systemAccount, identity.accountPublic, identity.accountJWT, identity.systemAccount, identity.systemJWT, pki.natsCert, pki.natsKey, pki.caPath, routePort, strings.Join(otherRoutes, ","), pki.natsCert, pki.natsKey, pki.caPath)
		paths[index] = filepath.Join(directory, fmt.Sprintf("nats-%d.conf", index+1))
		writeFixtureFile(t, paths[index], []byte(config), 0o600)
	}
	return paths
}

func writeFixtureFile(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func waitTCP(ctx context.Context, address string) bool {
	for {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(25 * time.Millisecond):
		}
	}
}
