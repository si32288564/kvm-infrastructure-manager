// Package sessiongeneration persists the last PostgreSQL-accepted Agent
// transport generation. It proposes generations but never grants authority.
package sessiongeneration

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

const schema = "kim.agent.session-generation/v1"

type record struct {
	Schema             string `json:"schema"`
	HostID             string `json:"host_id"`
	AcceptedGeneration uint64 `json:"accepted_generation"`
}

type Ledger struct {
	mu       sync.Mutex
	dir      string
	hostID   string
	accepted uint64
	lock     *os.File
	closed   bool
}

func Open(directory, hostID string) (*Ledger, error) {
	if directory == "" || hostID == "" {
		return nil, errors.New("session generation directory and Host identity are required")
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
	ledger := &Ledger{dir: directory, hostID: hostID, lock: lock}
	payload, err := os.ReadFile(filepath.Join(directory, "current.json"))
	if errors.Is(err, os.ErrNotExist) {
		return ledger, nil
	}
	if err != nil {
		_ = ledger.Close()
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(directory, "current.json"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = ledger.Close()
		return nil, errors.New("session generation ledger must be a private regular file")
	}
	var current record
	if err := json.Unmarshal(payload, &current); err != nil || current.Schema != schema || current.HostID != hostID {
		_ = ledger.Close()
		return nil, errors.New("session generation ledger identity conflict")
	}
	ledger.accepted = current.AcceptedGeneration
	return ledger, nil
}

func (ledger *Ledger) Next() (uint64, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.closed {
		return 0, errors.New("session generation ledger is closed")
	}
	if ledger.accepted == math.MaxUint64 {
		return 0, errors.New("session generation exhausted")
	}
	return ledger.accepted + 1, nil
}

// CommitAccepted records a generation only after SessionAccepted. A rejected
// or failed connection attempt never consumes a generation.
func (ledger *Ledger) CommitAccepted(generation uint64) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.closed {
		return errors.New("session generation ledger is closed")
	}
	if generation != ledger.accepted+1 {
		return errors.New("accepted session generation is not the next proposal")
	}
	payload, err := json.Marshal(record{Schema: schema, HostID: ledger.hostID, AcceptedGeneration: generation})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(ledger.dir, ".tmp-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
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
	if err := os.Rename(name, filepath.Join(ledger.dir, "current.json")); err != nil {
		return err
	}
	directory, err := os.Open(ledger.dir)
	if err != nil {
		return err
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return err
	}
	ledger.accepted = generation
	return nil
}

func (ledger *Ledger) Close() error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.closed {
		return nil
	}
	ledger.closed = true
	return errors.Join(unix.Flock(int(ledger.lock.Fd()), unix.LOCK_UN), ledger.lock.Close())
}
