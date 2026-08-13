package locallvmtransport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
)

type sourceMemory struct {
	mu                sync.Mutex
	identity          VolumeIdentity
	content           []byte
	holder            bool
	reads             int
	mutateAfterStream bool
}

func (s *sourceMemory) Inspect(_ context.Context, i VolumeIdentity) (VolumeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i != s.identity {
		return VolumeState{}, ErrAuthorityConflict
	}
	return VolumeState{SizeBytes: uint64(len(s.content)), HolderOpen: s.holder}, nil
}
func (s *sourceMemory) ReadAt(_ context.Context, i VolumeIdentity, p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i != s.identity {
		return 0, ErrAuthorityConflict
	}
	n := copy(p, s.content[off:])
	s.reads++
	if s.mutateAfterStream && s.reads == 5 {
		s.content[len(s.content)-1] ^= 0xff
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

type destinationMemory struct {
	mu       sync.Mutex
	identity VolumeIdentity
	content  []byte
	holder   bool
	flushes  int
}

func (d *destinationMemory) Inspect(_ context.Context, i VolumeIdentity) (VolumeState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i != d.identity {
		return VolumeState{}, ErrAuthorityConflict
	}
	return VolumeState{SizeBytes: uint64(len(d.content)), HolderOpen: d.holder}, nil
}
func (d *destinationMemory) WriteAt(_ context.Context, i VolumeIdentity, p []byte, off int64) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i != d.identity {
		return 0, ErrAuthorityConflict
	}
	return copy(d.content[off:], p), nil
}
func (d *destinationMemory) ReadAt(_ context.Context, i VolumeIdentity, p []byte, off int64) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i != d.identity {
		return 0, ErrAuthorityConflict
	}
	n := copy(p, d.content[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (d *destinationMemory) Flush(_ context.Context, i VolumeIdentity) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i != d.identity {
		return ErrAuthorityConflict
	}
	d.flushes++
	return nil
}

func certDigest(c tls.Certificate) string {
	sum := sha256.Sum256(c.Certificate[0])
	return hex.EncodeToString(sum[:])
}
func transportFixture(t *testing.T, size int) (Authority, *sourceMemory, *destinationMemory, *httptest.Server, *http.Client) {
	t.Helper()
	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	sourceID := VolumeIdentity{"host-a", "volume-a", "binding-a", "vg-a", "lv-a", 3}
	destinationID := VolumeIdentity{"host-b", "volume-b", "binding-b", "vg-b", "lv-b", 7}
	content := make([]byte, size)
	copy(content, []byte("base-image/unique-real-transport-guest-marker"))
	copy(content[size-64:], []byte("marker-near-end-of-real-transport-volume"))
	authority := Authority{TransportSessionID: "transport-1", TransportGeneration: 1, CopyOperationID: "copy-1", CopyGeneration: 1, Source: sourceID, Destination: destinationID, SourceHostAuthorityGeneration: 11, DestinationHostAuthorityGeneration: 13, SourceCredentialBindingRevision: 5, DestinationCredentialBindingRevision: 7, SourceSessionGeneration: 17, DestinationSessionGeneration: 19, ExactByteCount: uint64(size), ChunkSize: 4096, DigestAlgorithm: "SHA-256", TransportPolicyRevision: 1, ExpiresAt: time.Now().Add(time.Minute), SourceCertificateFingerprint: certDigest(serverTLS.Certificates[0]), DestinationCertificateFingerprint: certDigest(clientTLS.Certificates[0])}
	source := &sourceMemory{identity: sourceID, content: content}
	destination := &destinationMemory{identity: destinationID, content: make([]byte, size)}
	server := httptest.NewUnstartedServer(SourceHandler{Authority: authority, Reader: source})
	server.EnableHTTP2 = true
	server.TLS = serverTLS
	server.StartTLS()
	return authority, source, destination, server, NewMutualTLSClient(clientTLS)
}

func TestMutualTLSCrossHostTransferUsesSeparatedResolversAndFlushes(t *testing.T) {
	authority, source, destination, server, client := transportFixture(t, 16384)
	defer server.Close()
	metrics := &Metrics{}
	result, err := (DestinationClient{Authority: authority, Writer: destination, Client: client, Endpoint: server.URL, Metrics: metrics}).Transfer(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesTransferred != authority.ExactByteCount || result.SourceDigest != result.DestinationDigest || destination.flushes != 1 {
		t.Fatalf("result=%+v flushes=%d", result, destination.flushes)
	}
	if string(source.content) != string(destination.content) {
		t.Fatal("guest marker content not preserved")
	}
	snapshot := metrics.Snapshot()
	if snapshot.Sessions != 1 || snapshot.Bytes != authority.ExactByteCount || snapshot.Active != 0 {
		t.Fatalf("metrics=%+v", snapshot)
	}
}

func TestTransportRejectsWrongPeerHostIdentityAndHolder(t *testing.T) {
	t.Run("wrong destination Host", func(t *testing.T) {
		authority, _, destination, server, client := transportFixture(t, 8192)
		defer server.Close()
		authority.Destination.HostID = "host-c"
		if _, err := (DestinationClient{Authority: authority, Writer: destination, Client: client, Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
			t.Fatal("Host C consumed Host B session")
		}
	})
	t.Run("wrong peer certificate", func(t *testing.T) {
		authority, _, destination, server, _ := transportFixture(t, 8192)
		defer server.Close()
		_, wrongClientTLS, err := contracttest.TLSConfigs()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (DestinationClient{Authority: authority, Writer: destination, Client: NewMutualTLSClient(wrongClientTLS), Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
			t.Fatal("wrong mTLS peer consumed session")
		}
	})
	t.Run("source holder", func(t *testing.T) {
		authority, source, destination, server, client := transportFixture(t, 8192)
		defer server.Close()
		source.holder = true
		if _, err := (DestinationClient{Authority: authority, Writer: destination, Client: client, Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
			t.Fatal("open source holder accepted")
		}
	})
	t.Run("destination holder", func(t *testing.T) {
		authority, _, destination, server, client := transportFixture(t, 8192)
		defer server.Close()
		destination.holder = true
		if _, err := (DestinationClient{Authority: authority, Writer: destination, Client: client, Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
			t.Fatal("open destination holder accepted")
		}
	})
}

func TestTransportRejectsWrongLVBindingPartialCorruptionAndSourceDrift(t *testing.T) {
	for _, field := range []string{"source LV", "destination LV", "stale source binding", "stale destination binding"} {
		t.Run(field, func(t *testing.T) {
			authority, _, destination, server, client := transportFixture(t, 8192)
			defer server.Close()
			switch field {
			case "source LV":
				authority.Source.LVUUID = "other"
			case "destination LV":
				authority.Destination.LVUUID = "other"
			case "stale source binding":
				authority.Source.BindingGeneration--
			case "stale destination binding":
				authority.Destination.BindingGeneration--
			}
			if _, err := (DestinationClient{Authority: authority, Writer: destination, Client: client, Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
				t.Fatalf("%s accepted", field)
			}
		})
	}
	t.Run("partial", func(t *testing.T) {
		serverTLS, clientTLS, err := contracttest.TLSConfigs()
		if err != nil {
			t.Fatal(err)
		}
		destinationID := VolumeIdentity{"host-b", "volume-b", "binding-b", "vg-b", "lv-b", 7}
		authority := Authority{TransportSessionID: "transport-partial", TransportGeneration: 1, CopyOperationID: "copy-partial", CopyGeneration: 1, Source: VolumeIdentity{"host-a", "volume-a", "binding-a", "vg-a", "lv-a", 3}, Destination: destinationID, SourceHostAuthorityGeneration: 11, DestinationHostAuthorityGeneration: 13, SourceCredentialBindingRevision: 5, DestinationCredentialBindingRevision: 7, SourceSessionGeneration: 17, DestinationSessionGeneration: 19, ExactByteCount: 8192, ChunkSize: 4096, DigestAlgorithm: "SHA-256", TransportPolicyRevision: 1, ExpiresAt: time.Now().Add(time.Minute), SourceCertificateFingerprint: certDigest(serverTLS.Certificates[0]), DestinationCertificateFingerprint: certDigest(clientTLS.Certificates[0])}
		destination := &destinationMemory{identity: destinationID, content: make([]byte, 8192)}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Trailer", TrailerSourceDigest)
			w.WriteHeader(http.StatusOK)
			_ = writeFrame(w, 1, 0, make([]byte, 4096))
			w.Header().Set(TrailerSourceDigest, strings64("a"))
		}))
		server.EnableHTTP2 = true
		server.TLS = serverTLS
		server.StartTLS()
		defer server.Close()
		if result, err := (DestinationClient{Authority: authority, Writer: destination, Client: NewMutualTLSClient(clientTLS), Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil || result.BytesTransferred != 4096 || result.ResponseState != "UNKNOWN" {
			t.Fatal("partial stream verified")
		}
	})
	t.Run("source drift", func(t *testing.T) {
		authority, source, destination, server, client := transportFixture(t, 8192)
		defer server.Close()
		source.mutateAfterStream = true
		if _, err := (DestinationClient{Authority: authority, Writer: destination, Client: client, Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
			t.Fatal("source before/after drift verified")
		}
	})
	t.Run("destination corruption", func(t *testing.T) {
		authority, _, destination, server, client := transportFixture(t, 8192)
		defer server.Close()
		corrupt := &corruptingDestination{destinationMemory: destination}
		if _, err := (DestinationClient{Authority: authority, Writer: corrupt, Client: client, Endpoint: server.URL}).Transfer(t.Context(), 1); err == nil {
			t.Fatal("destination corruption verified")
		}
	})
}

type corruptingDestination struct{ *destinationMemory }

func (c *corruptingDestination) Flush(ctx context.Context, i VolumeIdentity) error {
	if err := c.destinationMemory.Flush(ctx, i); err != nil {
		return err
	}
	c.mu.Lock()
	c.content[len(c.content)/2] ^= 0xff
	c.mu.Unlock()
	return nil
}

func TestExactChunkDuplicateReplayConflictAndRestartFromZero(t *testing.T) {
	authority, source, destination, _, _ := transportFixture(t, 8192)
	writer := newExactChunkWriter(destination, authority.Destination, authority.ExactByteCount, authority.ChunkSize)
	first := append([]byte(nil), source.content[:4096]...)
	if err := writer.Write(t.Context(), 1, 0, first); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(t.Context(), 1, 0, first); err != nil {
		t.Fatalf("idempotent duplicate: %v", err)
	}
	conflict := append([]byte(nil), first...)
	conflict[0] ^= 0xff
	if err := writer.Write(t.Context(), 1, 0, conflict); !errors.Is(err, ErrAuthorityConflict) {
		t.Fatalf("conflicting duplicate=%v", err)
	}
	restart := newExactChunkWriter(destination, authority.Destination, authority.ExactByteCount, authority.ChunkSize)
	for sequence, offset := uint64(1), uint64(0); offset < authority.ExactByteCount; sequence, offset = sequence+1, offset+4096 {
		if err := restart.Write(t.Context(), sequence, offset, source.content[offset:offset+4096]); err != nil {
			t.Fatal(err)
		}
	}
	if err := destination.Flush(t.Context(), authority.Destination); err != nil {
		t.Fatal(err)
	}
	if string(source.content) != string(destination.content) {
		t.Fatal("restart from offset zero did not converge")
	}
}

func strings64(value string) string {
	return value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value + value
}
