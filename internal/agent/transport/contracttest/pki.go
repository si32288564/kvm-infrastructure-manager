// Package contracttest provides shared Q-094 adapter fixtures.
package contracttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// TLSConfigs returns mutually trusted server/client configurations.
func TLSConfigs() (server, client *tls.Config, returnedErr error) {
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "KIM Q-094 Test Root"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, err
	}
	serverCertificate, err := issueCertificate(ca, caKey, big.NewInt(2), "kim-agent-gateway", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, now)
	if err != nil {
		return nil, nil, err
	}
	clientCertificate, err := issueCertificate(ca, caKey, big.NewInt(3), "kim-host-agent", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, nil, now)
	if err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	server = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		NextProtos:   []string{"h2"},
	}
	client = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{clientCertificate},
		RootCAs:      pool,
		ServerName:   "localhost",
		NextProtos:   []string{"h2"},
	}
	return server, client, nil
}

func issueCertificate(ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial *big.Int, commonName string, usages []x509.ExtKeyUsage, dnsNames []string, ipAddresses []net.IP, now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	return certificate, nil
}

// CountingListener records accepted physical connections.
type CountingListener struct {
	net.Listener
	accepted atomic.Int64
	mu       sync.Mutex
	active   map[net.Conn]struct{}
}

// NewCountingListener wraps listener.
func NewCountingListener(listener net.Listener) *CountingListener {
	return &CountingListener{Listener: listener, active: make(map[net.Conn]struct{})}
}

// Accept records one physical connection.
func (listener *CountingListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	listener.accepted.Add(1)
	wrapped := &trackedConnection{Conn: connection, onClose: func() {
		listener.mu.Lock()
		delete(listener.active, connection)
		listener.mu.Unlock()
	}}
	listener.mu.Lock()
	listener.active[connection] = struct{}{}
	listener.mu.Unlock()
	return wrapped, nil
}

// Accepted returns the cumulative physical connection count.
func (listener *CountingListener) Accepted() int64 { return listener.accepted.Load() }

// Active returns the current physical connection count.
func (listener *CountingListener) Active() int {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	return len(listener.active)
}

type trackedConnection struct {
	net.Conn
	once    sync.Once
	onClose func()
}

func (connection *trackedConnection) Close() error {
	var err error
	connection.once.Do(func() {
		err = connection.Conn.Close()
		connection.onClose()
	})
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
