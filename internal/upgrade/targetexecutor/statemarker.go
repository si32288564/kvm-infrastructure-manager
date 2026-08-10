package targetexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Target struct {
	TargetID, ComponentType, ComponentID, TargetArtifactDigest, TargetDigest string
}

type Observation struct {
	State, Digest string
}

type StateMarkerBackend struct {
	directory string
}

type marker struct {
	SchemaVersion        string `json:"schema_version"`
	TargetID             string `json:"target_id"`
	ComponentType        string `json:"component_type"`
	ComponentID          string `json:"component_id"`
	TargetArtifactDigest string `json:"target_artifact_digest"`
	TargetDigest         string `json:"target_digest"`
}

func NewStateMarkerBackend(directory string) (*StateMarkerBackend, error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, errors.New("absolute KIM-owned state directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	return &StateMarkerBackend{directory: directory}, nil
}

func MarkerPath(directory, targetID string) string {
	sum := sha256.Sum256([]byte(targetID))
	return filepath.Join(directory, hex.EncodeToString(sum[:])+".json")
}

func (backend *StateMarkerBackend) Observe(target Target) (Observation, error) {
	expected, err := markerBytes(target)
	if err != nil {
		return Observation{}, err
	}
	path := MarkerPath(backend.directory, target.TargetID)
	observed, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Observation{State: "ABSENT", Digest: digest([]byte("absent:" + target.TargetID))}, nil
	}
	if err != nil {
		return Observation{State: "UNKNOWN", Digest: digest([]byte("unknown:" + target.TargetID))}, err
	}
	if string(observed) != string(expected) {
		return Observation{State: "CONFLICTING", Digest: digest(observed)}, nil
	}
	return Observation{State: "MATCHED", Digest: digest(observed)}, nil
}

func (backend *StateMarkerBackend) Apply(target Target) error {
	payload, err := markerBytes(target)
	if err != nil {
		return err
	}
	finalPath := MarkerPath(backend.directory, target.TargetID)
	temporary, err := os.CreateTemp(backend.directory, ".kim-upgrade-target-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			observation, observeErr := backend.Observe(target)
			if observeErr == nil && observation.State == "MATCHED" {
				return nil
			}
			return errors.New("existing upgrade target marker conflicts with desired evidence")
		}
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	temporaryPath = ""
	directory, err := os.Open(backend.directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func markerBytes(target Target) ([]byte, error) {
	if target.TargetID == "" || target.ComponentID == "" || !allowedComponentType(target.ComponentType) ||
		!validDigest(target.TargetArtifactDigest) || !validDigest(target.TargetDigest) {
		return nil, errors.New("closed typed upgrade target identity is required")
	}
	payload, err := json.Marshal(marker{SchemaVersion: "kim.upgrade.target-state-marker/v1", TargetID: target.TargetID,
		ComponentType: target.ComponentType, ComponentID: target.ComponentID,
		TargetArtifactDigest: target.TargetArtifactDigest, TargetDigest: target.TargetDigest})
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func allowedComponentType(value string) bool {
	switch value {
	case "API", "AGENT_GATEWAY", "CONTROL_WORKER", "OVN_RUNTIME_WORKER", "HOST_AGENT":
		return true
	default:
		return false
	}
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}
