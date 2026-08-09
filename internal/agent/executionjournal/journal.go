package executionjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"golang.org/x/sys/unix"
)

const schemaVersion = "kim.agent.execution-journal/v1"

var (
	ErrConflict = errors.New("execution journal evidence conflict")
	ErrNotFound = errors.New("execution journal record not found")
)

type CommandRecord struct {
	SchemaVersion string `json:"schema_version"`
	HostID        string `json:"host_id"`
	CommandID     string `json:"command_id"`
	AttemptIndex  int    `json:"attempt_index"`
	CommandDigest string `json:"command_digest"`
	TargetID      string `json:"target_id"`
	State         string `json:"state"`
	ResultDigest  string `json:"result_digest,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
}

type Journal struct {
	mu        sync.Mutex
	directory string
	hostID    string
	lock      *os.File
	records   map[string]CommandRecord
	closed    bool
}

func Open(directory, hostID string) (*Journal, error) {
	if directory == "" || hostID == "" {
		return nil, errors.New("execution journal directory and Host identity are required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(directory, "lock")
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, err
	}
	journal := &Journal{directory: directory, hostID: hostID, lock: lock, records: make(map[string]CommandRecord)}
	if err := journal.load(); err != nil {
		_ = journal.Close()
		return nil, err
	}
	return journal, nil
}

// Prepare durably records the Command identity before any backend mutation.
// Lease tokens and credentials are intentionally absent from CommandRecord.
func (journal *Journal) Prepare(commandID string, attemptIndex int, commandDigest, targetID string) (string, error) {
	if commandID == "" || attemptIndex < 1 || len(commandDigest) != 64 || targetID == "" {
		return "", errors.New("complete Command journal identity is required")
	}
	record := CommandRecord{SchemaVersion: schemaVersion, HostID: journal.hostID, CommandID: commandID, AttemptIndex: attemptIndex, CommandDigest: commandDigest, TargetID: targetID, State: "STARTED"}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return "", errors.New("execution journal is closed")
	}
	key := recordKey(commandID, attemptIndex)
	if existing, found := journal.records[key]; found {
		if existing.CommandID != record.CommandID || existing.AttemptIndex != record.AttemptIndex || existing.CommandDigest != record.CommandDigest || existing.TargetID != record.TargetID {
			return "", ErrConflict
		}
		return recordDigest(existing), nil
	}
	if err := journal.persist(record); err != nil {
		return "", err
	}
	journal.records[key] = record
	return recordDigest(record), nil
}

func (journal *Journal) Complete(commandID string, attemptIndex int, resultDigest, outcome string) (string, error) {
	if len(resultDigest) != 64 || (outcome != "SUCCEEDED" && outcome != "FAILED" && outcome != "UNKNOWN") {
		return "", errors.New("complete typed Result evidence is required")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return "", errors.New("execution journal is closed")
	}
	key := recordKey(commandID, attemptIndex)
	record, found := journal.records[key]
	if !found {
		return "", ErrNotFound
	}
	if record.State == "COMPLETED" {
		if record.ResultDigest != resultDigest || record.Outcome != outcome {
			return "", ErrConflict
		}
		return recordDigest(record), nil
	}
	record.State, record.ResultDigest, record.Outcome = "COMPLETED", resultDigest, outcome
	if err := journal.persist(record); err != nil {
		return "", err
	}
	journal.records[key] = record
	return recordDigest(record), nil
}

// Evidence returns existing write-before-execute evidence without creating a
// new STARTED record. Resync cannot manufacture proof that execution began.
func (journal *Journal) Evidence(commandID string, attemptIndex int, commandDigest, targetID string) (CommandRecord, string, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return CommandRecord{}, "", errors.New("execution journal is closed")
	}
	record, found := journal.records[recordKey(commandID, attemptIndex)]
	if !found {
		return CommandRecord{}, "", ErrNotFound
	}
	if record.CommandDigest != commandDigest || record.TargetID != targetID {
		return CommandRecord{}, "", ErrConflict
	}
	return record, recordDigest(record), nil
}

func (journal *Journal) Records() ([]CommandRecord, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil, errors.New("execution journal is closed")
	}
	records := make([]CommandRecord, 0, len(journal.records))
	for _, record := range journal.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CommandID != records[j].CommandID {
			return records[i].CommandID < records[j].CommandID
		}
		return records[i].AttemptIndex < records[j].AttemptIndex
	})
	return records, nil
}

func (journal *Journal) Close() error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil
	}
	journal.closed = true
	return errors.Join(unix.Flock(int(journal.lock.Fd()), unix.LOCK_UN), journal.lock.Close())
}

func (journal *Journal) load() error {
	entries, err := os.ReadDir(journal.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "lock" || len(entry.Name()) > 5 && entry.Name()[:5] == ".tmp-" {
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return errors.New("unexpected execution journal entry")
		}
		path := filepath.Join(journal.directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("execution journal entry must be a private regular file")
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record CommandRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return err
		}
		if record.SchemaVersion != schemaVersion || record.HostID != journal.hostID || filepath.Base(path) != recordFilename(record.CommandID, record.AttemptIndex) {
			return errors.New("execution journal record identity mismatch")
		}
		journal.records[recordKey(record.CommandID, record.AttemptIndex)] = record
	}
	return nil
}

func (journal *Journal) persist(record CommandRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(journal.directory, ".tmp-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryName) }
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return err
	}
	destination := filepath.Join(journal.directory, recordFilename(record.CommandID, record.AttemptIndex))
	if err := os.Rename(temporaryName, destination); err != nil {
		cleanup()
		return err
	}
	directory, err := os.Open(journal.directory)
	if err != nil {
		return err
	}
	err = directory.Sync()
	return errors.Join(err, directory.Close())
}

func recordKey(commandID string, attemptIndex int) string {
	return fmt.Sprintf("%s\x00%d", commandID, attemptIndex)
}

func recordFilename(commandID string, attemptIndex int) string {
	digest := sha256.Sum256([]byte(recordKey(commandID, attemptIndex)))
	return hex.EncodeToString(digest[:]) + ".json"
}

func recordDigest(record CommandRecord) string {
	payload, _ := json.Marshal(record)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
