// Package locallvmtransport implements the closed cross-Host Local LVM data
// plane. It uses mutually authenticated TLS 1.3 HTTP/2 and stable authority
// identities only; neither endpoint accepts a path, command, or argv.
package locallvmtransport

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ProtocolVersion       = "kim.local-lvm-cross-host/v1"
	HeaderSessionID       = "Kim-Transport-Session-Id"
	HeaderGeneration      = "Kim-Transport-Generation"
	HeaderCopyOperation   = "Kim-Copy-Operation-Id"
	HeaderCopyGeneration  = "Kim-Copy-Generation"
	HeaderDestinationHost = "Kim-Destination-Host-Id"
	HeaderAuthorityDigest = "Kim-Transport-Authority-Sha256"
	TrailerSourceDigest   = "Kim-Source-Content-Sha256"
	frameHeaderBytes      = 8 + 8 + 4 + sha256.Size
	MetricActive          = "local_lvm_transport_active"
	MetricBytes           = "local_lvm_transport_bytes"
	MetricSessions        = "local_lvm_transport_sessions"
	MetricRetries         = "local_lvm_transport_retries"
	MetricUnknown         = "local_lvm_transport_unknown"
	MetricIntegrity       = "local_lvm_transport_integrity_failures"
	MetricDuration        = "local_lvm_transport_duration"
)

var ErrAuthorityConflict = errors.New("Local LVM transport authority conflict")

type VolumeIdentity struct {
	HostID, VolumeID, BindingID, VGUUID, LVUUID string
	BindingGeneration                           uint64
}
type VolumeState struct {
	SizeBytes  uint64
	HolderOpen bool
}

type Authority struct {
	TransportSessionID                                                    string
	TransportGeneration, CopyGeneration                                   uint64
	CopyOperationID                                                       string
	Source, Destination                                                   VolumeIdentity
	SourceHostAuthorityGeneration, DestinationHostAuthorityGeneration     uint64
	SourceCredentialBindingRevision, DestinationCredentialBindingRevision uint64
	SourceSessionGeneration, DestinationSessionGeneration                 uint64
	ExactByteCount                                                        uint64
	ChunkSize                                                             int
	DigestAlgorithm                                                       string
	TransportPolicyRevision                                               uint64
	ExpiresAt                                                             time.Time
	SourceCertificateFingerprint, DestinationCertificateFingerprint       string
}

func (a Authority) Validate(now time.Time) error {
	_, sourceFingerprintErr := hex.DecodeString(a.SourceCertificateFingerprint)
	_, destinationFingerprintErr := hex.DecodeString(a.DestinationCertificateFingerprint)
	if a.TransportSessionID == "" || a.TransportGeneration == 0 || a.CopyOperationID == "" || a.CopyGeneration == 0 || a.Source.HostID == "" || a.Destination.HostID == "" || a.Source.HostID == a.Destination.HostID || a.Source.VolumeID == "" || a.Source.BindingID == "" || a.Source.BindingGeneration == 0 || a.Source.VGUUID == "" || a.Source.LVUUID == "" || a.Destination.VolumeID == "" || a.Destination.BindingID == "" || a.Destination.BindingGeneration == 0 || a.Destination.VGUUID == "" || a.Destination.LVUUID == "" || a.Source.LVUUID == a.Destination.LVUUID || a.SourceHostAuthorityGeneration == 0 || a.DestinationHostAuthorityGeneration == 0 || a.SourceCredentialBindingRevision == 0 || a.DestinationCredentialBindingRevision == 0 || a.SourceSessionGeneration == 0 || a.DestinationSessionGeneration == 0 || a.ExactByteCount == 0 || a.ChunkSize < 4096 || a.ChunkSize > 4<<20 || a.DigestAlgorithm != "SHA-256" || a.TransportPolicyRevision == 0 || !a.ExpiresAt.After(now) || len(a.SourceCertificateFingerprint) != 64 || len(a.DestinationCertificateFingerprint) != 64 || sourceFingerprintErr != nil || destinationFingerprintErr != nil {
		return ErrAuthorityConflict
	}
	return nil
}

func (a Authority) Digest() string {
	uintString := func(value uint64) string { return strconv.FormatUint(value, 10) }
	value := strings.Join([]string{
		ProtocolVersion, a.TransportSessionID, uintString(a.TransportGeneration), a.CopyOperationID, uintString(a.CopyGeneration),
		a.Source.HostID, uintString(a.SourceHostAuthorityGeneration), uintString(a.SourceCredentialBindingRevision), uintString(a.SourceSessionGeneration), a.SourceCertificateFingerprint,
		a.Source.VolumeID, a.Source.BindingID, uintString(a.Source.BindingGeneration), a.Source.VGUUID, a.Source.LVUUID,
		a.Destination.HostID, uintString(a.DestinationHostAuthorityGeneration), uintString(a.DestinationCredentialBindingRevision), uintString(a.DestinationSessionGeneration), a.DestinationCertificateFingerprint,
		a.Destination.VolumeID, a.Destination.BindingID, uintString(a.Destination.BindingGeneration), a.Destination.VGUUID, a.Destination.LVUUID,
		uintString(a.ExactByteCount), strconv.Itoa(a.ChunkSize), a.DigestAlgorithm, uintString(a.TransportPolicyRevision), strconv.FormatInt(a.ExpiresAt.UnixNano(), 10),
	}, "\n")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type SourceReader interface {
	Inspect(context.Context, VolumeIdentity) (VolumeState, error)
	ReadAt(context.Context, VolumeIdentity, []byte, int64) (int, error)
}
type DestinationWriter interface {
	Inspect(context.Context, VolumeIdentity) (VolumeState, error)
	WriteAt(context.Context, VolumeIdentity, []byte, int64) (int, error)
	Flush(context.Context, VolumeIdentity) error
	ReadAt(context.Context, VolumeIdentity, []byte, int64) (int, error)
}

type Metrics struct{ Active, Bytes, Sessions, Retries, Unknown, IntegrityFailures, DurationNanoseconds atomic.Uint64 }
type MetricsSnapshot struct{ Active, Bytes, Sessions, Retries, Unknown, IntegrityFailures, DurationNanoseconds uint64 }

func (m *Metrics) Snapshot() MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{}
	}
	return MetricsSnapshot{m.Active.Load(), m.Bytes.Load(), m.Sessions.Load(), m.Retries.Load(), m.Unknown.Load(), m.IntegrityFailures.Load(), m.DurationNanoseconds.Load()}
}

type SourceHandler struct {
	Authority Authority
	Reader    SourceReader
	Metrics   *Metrics
}

func (h SourceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if h.Metrics != nil {
		h.Metrics.Active.Add(1)
		h.Metrics.Sessions.Add(1)
		defer func() { h.Metrics.Active.Add(^uint64(0)); h.Metrics.DurationNanoseconds.Add(uint64(time.Since(start))) }()
	}
	if r.Method != "POST" || r.ProtoMajor != 2 || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || h.Reader == nil || h.Authority.Validate(time.Now()) != nil {
		http.Error(w, "closed transport authority required", http.StatusUnauthorized)
		return
	}
	peer := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	if hex.EncodeToString(peer[:]) != h.Authority.DestinationCertificateFingerprint || header(r, HeaderAuthorityDigest) != h.Authority.Digest() || header(r, HeaderSessionID) != h.Authority.TransportSessionID || headerUint(r, HeaderGeneration) != h.Authority.TransportGeneration || header(r, HeaderCopyOperation) != h.Authority.CopyOperationID || headerUint(r, HeaderCopyGeneration) != h.Authority.CopyGeneration || header(r, HeaderDestinationHost) != h.Authority.Destination.HostID {
		http.Error(w, "transport peer or session mismatch", http.StatusForbidden)
		return
	}
	state, err := h.Reader.Inspect(r.Context(), h.Authority.Source)
	if err != nil || state.HolderOpen || state.SizeBytes != h.Authority.ExactByteCount {
		http.Error(w, "source identity or holder fence mismatch", http.StatusConflict)
		return
	}
	before, err := digestReader(r.Context(), h.Reader, h.Authority.Source, h.Authority.ExactByteCount, h.Authority.ChunkSize)
	if err != nil {
		http.Error(w, "source digest unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.kim.local-lvm-block-stream")
	w.Header().Set("Trailer", TrailerSourceDigest)
	w.WriteHeader(http.StatusOK)
	buffer := make([]byte, h.Authority.ChunkSize)
	var sequence uint64
	for offset := uint64(0); offset < h.Authority.ExactByteCount; {
		length := len(buffer)
		if remaining := h.Authority.ExactByteCount - offset; remaining < uint64(length) {
			length = int(remaining)
		}
		chunk := buffer[:length]
		n, readErr := h.Reader.ReadAt(r.Context(), h.Authority.Source, chunk, int64(offset))
		if readErr != nil && readErr != io.EOF || n != length {
			return
		}
		sequence++
		if writeFrame(w, sequence, offset, chunk) != nil {
			return
		}
		offset += uint64(n)
		if h.Metrics != nil {
			h.Metrics.Bytes.Add(uint64(n))
		}
	}
	after, err := digestReader(r.Context(), h.Reader, h.Authority.Source, h.Authority.ExactByteCount, h.Authority.ChunkSize)
	afterState, inspectErr := h.Reader.Inspect(r.Context(), h.Authority.Source)
	if err != nil || inspectErr != nil || afterState.HolderOpen || afterState.SizeBytes != h.Authority.ExactByteCount || before != after {
		if h.Metrics != nil {
			h.Metrics.IntegrityFailures.Add(1)
		}
		w.Header().Set(TrailerSourceDigest, "CONFLICTING")
		return
	}
	w.Header().Set(TrailerSourceDigest, after)
}

type DestinationClient struct {
	Authority Authority
	Writer    DestinationWriter
	Client    *http.Client
	Endpoint  string
	Metrics   *Metrics
}
type Result struct {
	BytesTransferred                uint64
	SourceDigest, DestinationDigest string
	ResponseState                   string
	AttemptIndex                    int
}

func (c DestinationClient) Transfer(ctx context.Context, attempt int) (Result, error) {
	var out Result
	if attempt < 1 || c.Writer == nil || c.Client == nil || c.Endpoint == "" || c.Authority.Validate(time.Now()) != nil {
		return out, ErrAuthorityConflict
	}
	if c.Metrics != nil {
		c.Metrics.Active.Add(1)
		c.Metrics.Sessions.Add(1)
		if attempt > 1 {
			c.Metrics.Retries.Add(1)
		}
		start := time.Now()
		defer func() { c.Metrics.Active.Add(^uint64(0)); c.Metrics.DurationNanoseconds.Add(uint64(time.Since(start))) }()
	}
	state, err := c.Writer.Inspect(ctx, c.Authority.Destination)
	if err != nil || state.HolderOpen || state.SizeBytes != c.Authority.ExactByteCount {
		return out, ErrAuthorityConflict
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set(HeaderSessionID, c.Authority.TransportSessionID)
	req.Header.Set(HeaderGeneration, strconv.FormatUint(c.Authority.TransportGeneration, 10))
	req.Header.Set(HeaderCopyOperation, c.Authority.CopyOperationID)
	req.Header.Set(HeaderCopyGeneration, strconv.FormatUint(c.Authority.CopyGeneration, 10))
	req.Header.Set(HeaderDestinationHost, c.Authority.Destination.HostID)
	req.Header.Set(HeaderAuthorityDigest, c.Authority.Digest())
	response, err := c.Client.Do(req)
	if err != nil {
		if c.Metrics != nil {
			c.Metrics.Unknown.Add(1)
		}
		out.ResponseState = "LOST"
		return out, err
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 || response.TLS == nil || len(response.TLS.PeerCertificates) == 0 {
		return out, ErrAuthorityConflict
	}
	peer := sha256.Sum256(response.TLS.PeerCertificates[0].Raw)
	if hex.EncodeToString(peer[:]) != c.Authority.SourceCertificateFingerprint {
		return out, ErrAuthorityConflict
	}
	if response.StatusCode != http.StatusOK {
		return out, ErrAuthorityConflict
	}
	writer := newExactChunkWriter(c.Writer, c.Authority.Destination, c.Authority.ExactByteCount, c.Authority.ChunkSize)
	reader := bufio.NewReader(response.Body)
	for {
		sequence, offset, chunk, frameErr := readFrame(reader, c.Authority.ChunkSize)
		if errors.Is(frameErr, io.EOF) {
			break
		}
		if frameErr != nil {
			out.BytesTransferred = writer.Bytes()
			out.ResponseState = "UNKNOWN"
			if c.Metrics != nil {
				c.Metrics.Unknown.Add(1)
			}
			return out, frameErr
		}
		if err := writer.Write(ctx, sequence, offset, chunk); err != nil {
			return out, err
		}
		if c.Metrics != nil {
			c.Metrics.Bytes.Add(uint64(len(chunk)))
		}
	}
	if writer.Bytes() != c.Authority.ExactByteCount {
		return Result{BytesTransferred: writer.Bytes(), ResponseState: "UNKNOWN", AttemptIndex: attempt}, io.ErrUnexpectedEOF
	}
	if err := c.Writer.Flush(ctx, c.Authority.Destination); err != nil {
		return out, err
	}
	afterState, err := c.Writer.Inspect(ctx, c.Authority.Destination)
	if err != nil || afterState.HolderOpen || afterState.SizeBytes != c.Authority.ExactByteCount {
		return out, ErrAuthorityConflict
	}
	destinationDigest, err := digestWriter(ctx, c.Writer, c.Authority.Destination, c.Authority.ExactByteCount, c.Authority.ChunkSize)
	if err != nil {
		return out, err
	}
	sourceDigest := response.Trailer.Get(TrailerSourceDigest)
	out = Result{BytesTransferred: writer.Bytes(), SourceDigest: sourceDigest, DestinationDigest: destinationDigest, ResponseState: "RECEIVED", AttemptIndex: attempt}
	if len(sourceDigest) != 64 || sourceDigest != destinationDigest {
		if c.Metrics != nil {
			c.Metrics.IntegrityFailures.Add(1)
		}
		return out, ErrAuthorityConflict
	}
	return out, nil
}

func NewMutualTLSClient(config *tls.Config) *http.Client {
	transport := &http.Transport{TLSClientConfig: config.Clone(), ForceAttemptHTTP2: true}
	return &http.Client{Transport: transport}
}

type exactChunkWriter struct {
	writer       DestinationWriter
	identity     VolumeIdentity
	exact        uint64
	chunk        int
	mu           sync.Mutex
	seen         map[uint64][32]byte
	bytes        uint64
	nextSequence uint64
}

func newExactChunkWriter(w DestinationWriter, i VolumeIdentity, exact uint64, chunk int) *exactChunkWriter {
	return &exactChunkWriter{writer: w, identity: i, exact: exact, chunk: chunk, seen: map[uint64][32]byte{}}
}
func (w *exactChunkWriter) Write(ctx context.Context, sequence, offset uint64, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sequence == 0 || offset >= w.exact || uint64(len(data)) > w.exact-offset || len(data) == 0 || len(data) > w.chunk {
		return ErrAuthorityConflict
	}
	digest := sha256.Sum256(data)
	if prior, ok := w.seen[offset]; ok {
		if prior != digest {
			return ErrAuthorityConflict
		}
		return nil
	}
	if sequence != w.nextSequence+1 || offset != w.bytes {
		return ErrAuthorityConflict
	}
	n, err := w.writer.WriteAt(ctx, w.identity, data, int64(offset))
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	w.seen[offset] = digest
	w.bytes += uint64(n)
	w.nextSequence = sequence
	return nil
}
func (w *exactChunkWriter) Bytes() uint64 { w.mu.Lock(); defer w.mu.Unlock(); return w.bytes }

func writeFrame(w io.Writer, sequence, offset uint64, data []byte) error {
	header := make([]byte, frameHeaderBytes)
	binary.BigEndian.PutUint64(header[0:8], sequence)
	binary.BigEndian.PutUint64(header[8:16], offset)
	binary.BigEndian.PutUint32(header[16:20], uint32(len(data)))
	digest := sha256.Sum256(data)
	copy(header[20:], digest[:])
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}
func readFrame(r io.Reader, max int) (uint64, uint64, []byte, error) {
	header := make([]byte, frameHeaderBytes)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, 0, nil, err
	}
	sequence, offset, length := binary.BigEndian.Uint64(header[0:8]), binary.BigEndian.Uint64(header[8:16]), binary.BigEndian.Uint32(header[16:20])
	if length == 0 || int(length) > max {
		return 0, 0, nil, ErrAuthorityConflict
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, 0, nil, err
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), hex.EncodeToString(header[20:])) {
		return 0, 0, nil, ErrAuthorityConflict
	}
	return sequence, offset, data, nil
}

func digestReader(ctx context.Context, r SourceReader, i VolumeIdentity, size uint64, chunk int) (string, error) {
	return digestExact(ctx, size, chunk, func(p []byte, offset int64) (int, error) { return r.ReadAt(ctx, i, p, offset) })
}
func digestWriter(ctx context.Context, r DestinationWriter, i VolumeIdentity, size uint64, chunk int) (string, error) {
	return digestExact(ctx, size, chunk, func(p []byte, offset int64) (int, error) { return r.ReadAt(ctx, i, p, offset) })
}
func digestExact(ctx context.Context, size uint64, chunk int, read func([]byte, int64) (int, error)) (string, error) {
	var h hash.Hash = sha256.New()
	buffer := make([]byte, chunk)
	for offset := uint64(0); offset < size; {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		length := len(buffer)
		if remaining := size - offset; remaining < uint64(length) {
			length = int(remaining)
		}
		n, err := read(buffer[:length], int64(offset))
		if err != nil && err != io.EOF {
			return "", err
		}
		if n != length {
			return "", io.ErrUnexpectedEOF
		}
		_, _ = h.Write(buffer[:n])
		offset += uint64(n)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func header(r *http.Request, key string) string { return r.Header.Get(key) }
func headerUint(r *http.Request, key string) uint64 {
	value, _ := strconv.ParseUint(r.Header.Get(key), 10, 64)
	return value
}
