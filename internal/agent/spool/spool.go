// Package spool implements the bounded durable Agent outbound journal.
package spool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"golang.org/x/sys/unix"
)

// HandleReceipt allows Spool to serve as the Session Runner's durable receipt
// handler. Context cancellation does not remove an unacknowledged entry.
func (spool *Spool) HandleReceipt(ctx context.Context, receipt session.Receipt) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return spool.Acknowledge(receipt)
}

const recordVersion = "kim.agent.spool/v1"

var (
	ErrLocked          = errors.New("Agent spool is already locked")
	ErrFull            = errors.New("Agent spool capacity exceeded")
	ErrEmpty           = errors.New("Agent spool is empty")
	ErrMessageConflict = errors.New("Agent spool message digest conflict")
)

type Config struct {
	Directory       string
	HostIdentity    string
	MaxEntries      int
	MaxBytes        int64
	MaxMessageBytes int
}

type Stats struct {
	QueuedEntries int
	QueuedBytes   int64
	MaxEntries    int
	MaxBytes      int64
}

type record struct {
	Version  string           `json:"version"`
	Envelope session.Envelope `json:"envelope"`
}

type entry struct {
	path     string
	envelope session.Envelope
	size     int64
}

type Spool struct {
	mu              sync.Mutex
	directory       string
	queueDirectory  string
	hostIdentity    string
	maxEntries      int
	maxBytes        int64
	maxMessageBytes int
	lock            *os.File
	entries         map[string]entry
	closed          bool
}

func Open(config Config) (*Spool, error) {
	if config.Directory == "" || config.HostIdentity == "" || config.MaxEntries < 1 || config.MaxBytes < 1 || config.MaxMessageBytes < 1 {
		return nil, errors.New("spool directory, Host identity, and positive bounds are required")
	}
	if err := ensurePrivateDirectory(config.Directory); err != nil {
		return nil, err
	}
	queueDirectory := filepath.Join(config.Directory, "queue")
	if err := ensurePrivateDirectory(queueDirectory); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(config.Directory, "lock")
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open spool lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock Agent spool: %w", err)
	}
	spool := &Spool{
		directory: config.Directory, queueDirectory: queueDirectory, hostIdentity: config.HostIdentity,
		maxEntries: config.MaxEntries, maxBytes: config.MaxBytes, maxMessageBytes: config.MaxMessageBytes,
		lock: lock, entries: make(map[string]entry),
	}
	if err := spool.load(); err != nil {
		_ = spool.Close()
		return nil, err
	}
	return spool, nil
}

func (spool *Spool) Enqueue(envelope session.Envelope) error {
	if err := envelope.Validate(spool.maxMessageBytes); err != nil {
		return fmt.Errorf("validate durable Agent envelope: %w", err)
	}
	if envelope.HostIdentity != spool.hostIdentity {
		return errors.New("durable Agent envelope Host identity mismatch")
	}
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.closed {
		return errors.New("Agent spool is closed")
	}
	if existing, ok := spool.entries[envelope.MessageID]; ok {
		if sameMessage(existing.envelope, envelope) {
			return nil
		}
		return ErrMessageConflict
	}
	payload, err := json.Marshal(record{Version: recordVersion, Envelope: envelope})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	stats := spool.statsLocked()
	if stats.QueuedEntries+1 > spool.maxEntries || stats.QueuedBytes+int64(len(payload)) > spool.maxBytes {
		return ErrFull
	}
	path := filepath.Join(spool.queueDirectory, messageFilename(envelope.MessageID))
	if err := writeNewAtomic(spool.queueDirectory, path, payload); err != nil {
		return fmt.Errorf("persist durable Agent envelope: %w", err)
	}
	spool.entries[envelope.MessageID] = entry{path: path, envelope: cloneEnvelope(envelope), size: int64(len(payload))}
	return nil
}

// Pending returns stable application messages. Callers bind each copy to the
// current transport generation immediately before sending.
func (spool *Spool) Pending() ([]session.Envelope, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.closed {
		return nil, errors.New("Agent spool is closed")
	}
	entries := make([]entry, 0, len(spool.entries))
	for _, item := range spool.entries {
		entries = append(entries, item)
	}
	sort.Slice(entries, func(left, right int) bool {
		a, b := entries[left].envelope, entries[right].envelope
		if a.SequenceScope != b.SequenceScope {
			return a.SequenceScope < b.SequenceScope
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.MessageID < b.MessageID
	})
	result := make([]session.Envelope, 0, len(entries))
	for _, item := range entries {
		result = append(result, cloneEnvelope(item.envelope))
	}
	return result, nil
}

func (spool *Spool) Acknowledge(receipt session.Receipt) error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.closed {
		return errors.New("Agent spool is closed")
	}
	item, ok := spool.entries[receipt.MessageID]
	if !ok {
		return ErrEmpty
	}
	if err := receipt.ValidateFor(item.envelope); err != nil {
		return err
	}
	if receipt.Disposition != "ACCEPTED" {
		return fmt.Errorf("receipt disposition %s does not release durable message", receipt.Disposition)
	}
	if err := os.Remove(item.path); err != nil {
		return err
	}
	if err := syncDirectory(spool.queueDirectory); err != nil {
		return err
	}
	delete(spool.entries, receipt.MessageID)
	return nil
}

func (spool *Spool) Digest() (string, error) {
	pending, err := spool.Pending()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, envelope := range pending {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%d\n", envelope.MessageID, envelope.PayloadDigest, envelope.SequenceScope, envelope.Sequence)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (spool *Spool) Stats() Stats {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	return spool.statsLocked()
}

func (spool *Spool) Close() error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.closed {
		return nil
	}
	spool.closed = true
	unlockErr := unix.Flock(int(spool.lock.Fd()), unix.LOCK_UN)
	closeErr := spool.lock.Close()
	return errors.Join(unlockErr, closeErr)
}

func (spool *Spool) load() error {
	entries, err := os.ReadDir(spool.queueDirectory)
	if err != nil {
		return err
	}
	for _, directoryEntry := range entries {
		if strings.HasPrefix(directoryEntry.Name(), ".tmp-") {
			if err := os.Remove(filepath.Join(spool.queueDirectory, directoryEntry.Name())); err != nil {
				return err
			}
			continue
		}
		if directoryEntry.IsDir() || !strings.HasSuffix(directoryEntry.Name(), ".json") {
			return errors.New("unexpected Agent spool entry")
		}
		path := filepath.Join(spool.queueDirectory, directoryEntry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("Agent spool entry must be a private regular file")
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var decoded record
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return err
		}
		if decoded.Version != recordVersion || decoded.Envelope.HostIdentity != spool.hostIdentity {
			return errors.New("Agent spool record version or Host identity mismatch")
		}
		if err := decoded.Envelope.Validate(spool.maxMessageBytes); err != nil {
			return err
		}
		if messageFilename(decoded.Envelope.MessageID) != directoryEntry.Name() {
			return errors.New("Agent spool record filename mismatch")
		}
		if _, exists := spool.entries[decoded.Envelope.MessageID]; exists {
			return ErrMessageConflict
		}
		spool.entries[decoded.Envelope.MessageID] = entry{path: path, envelope: cloneEnvelope(decoded.Envelope), size: info.Size()}
	}
	stats := spool.statsLocked()
	if stats.QueuedEntries > spool.maxEntries || stats.QueuedBytes > spool.maxBytes {
		return ErrFull
	}
	return syncDirectory(spool.queueDirectory)
}

func (spool *Spool) statsLocked() Stats {
	stats := Stats{QueuedEntries: len(spool.entries), MaxEntries: spool.maxEntries, MaxBytes: spool.maxBytes}
	for _, item := range spool.entries {
		stats.QueuedBytes += item.size
	}
	return stats
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("Agent spool directory must be private")
	}
	return nil
}

func writeNewAtomic(directory, target string, payload []byte) error {
	temporary, err := os.CreateTemp(directory, ".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func messageFilename(messageID string) string {
	digest := sha256.Sum256([]byte(messageID))
	return hex.EncodeToString(digest[:]) + ".json"
}

func sameMessage(left, right session.Envelope) bool {
	return left.HostIdentity == right.HostIdentity && left.Stream == right.Stream && left.MessageID == right.MessageID && left.SchemaVersion == right.SchemaVersion && left.SequenceScope == right.SequenceScope && left.Sequence == right.Sequence && left.ResourceGeneration == right.ResourceGeneration && left.PayloadDigest == right.PayloadDigest && left.CorrelationKey == right.CorrelationKey
}

func cloneEnvelope(envelope session.Envelope) session.Envelope {
	envelope.Payload = append([]byte(nil), envelope.Payload...)
	return envelope
}
