package targetexecutor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const systemdPackageProfileSchema = "kim.upgrade.systemd-package-profile/v1"

var packageIdentityPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,126}$`)
var serviceIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,126}\.service$`)

type SystemdPackageArtifact struct {
	PackagePath    string `json:"package_path"`
	PackageVersion string `json:"package_version"`
	BinaryDigest   string `json:"binary_digest"`
}

type SystemdPackageProfile struct {
	SchemaVersion string                            `json:"schema_version"`
	ComponentType string                            `json:"component_type"`
	ComponentID   string                            `json:"component_id"`
	PackageName   string                            `json:"package_name"`
	ServiceName   string                            `json:"service_name"`
	BinaryPath    string                            `json:"binary_path"`
	HealthPath    string                            `json:"health_path"`
	HealthSchema  string                            `json:"health_schema"`
	Artifacts     map[string]SystemdPackageArtifact `json:"artifacts"`
}

type SystemdPackageBackend struct {
	profile SystemdPackageProfile
}

type systemdHealth struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	Ready         bool   `json:"ready"`
	PID           int    `json:"pid"`
	BootID        string `json:"boot_id"`
	StartTicks    uint64 `json:"process_start_ticks"`
}

type systemdObservation struct {
	PackageStatus     string `json:"package_status"`
	PackageVersion    string `json:"package_version"`
	ActiveState       string `json:"active_state"`
	SubState          string `json:"sub_state"`
	MainPID           int    `json:"main_pid"`
	ProcessDigest     string `json:"process_digest"`
	HealthVersion     string `json:"health_version"`
	HealthReady       bool   `json:"health_ready"`
	HealthPID         int    `json:"health_pid"`
	BootID            string `json:"boot_id"`
	ProcessStartTicks uint64 `json:"process_start_ticks"`
}

func NewSystemdPackageBackend(profilePath string) (*SystemdPackageBackend, error) {
	if profilePath == "" || !filepath.IsAbs(profilePath) {
		return nil, errors.New("absolute administrator-owned systemd package profile is required")
	}
	info, err := os.Stat(profilePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("systemd package profile must be a non-writable regular file")
	}
	if os.Geteuid() == 0 {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, errors.New("privileged systemd package profile must be root-owned")
		}
	}
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var profile SystemdPackageProfile
	if err := decoder.Decode(&profile); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("systemd package profile contains trailing data")
	}
	if profile.SchemaVersion != systemdPackageProfileSchema || !allowedComponentType(profile.ComponentType) ||
		profile.ComponentID == "" || !packageIdentityPattern.MatchString(profile.PackageName) ||
		!serviceIdentityPattern.MatchString(profile.ServiceName) || !filepath.IsAbs(profile.BinaryPath) ||
		!filepath.IsAbs(profile.HealthPath) || profile.HealthSchema == "" || len(profile.Artifacts) == 0 {
		return nil, errors.New("closed systemd package profile is incomplete")
	}
	for artifactDigest, artifact := range profile.Artifacts {
		if !validDigest(artifactDigest) || !validDigest(artifact.BinaryDigest) || artifact.PackageVersion == "" ||
			!filepath.IsAbs(artifact.PackagePath) || filepath.Ext(artifact.PackagePath) != ".deb" {
			return nil, errors.New("systemd package artifact mapping is invalid")
		}
	}
	return &SystemdPackageBackend{profile: profile}, nil
}

func (backend *SystemdPackageBackend) Observe(ctx context.Context, target Target) (Observation, error) {
	artifact, err := backend.artifactFor(target)
	if err != nil {
		return Observation{}, err
	}
	observed := systemdObservation{}
	packageOutput, err := runClosed(ctx, "/usr/bin/dpkg-query", "-W", "-f=${Status}\t${Version}", backend.profile.PackageName)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return observationFromSystemd("ABSENT", observed), nil
		}
		return observationFromSystemd("UNKNOWN", observed), err
	}
	fields := strings.Split(strings.TrimSpace(string(packageOutput)), "\t")
	if len(fields) != 2 {
		return observationFromSystemd("UNKNOWN", observed), nil
	}
	observed.PackageStatus = fields[0]
	observed.PackageVersion = fields[1]
	if observed.PackageStatus != "install ok installed" {
		return observationFromSystemd("CONFLICTING", observed), nil
	}
	if observed.PackageVersion != artifact.PackageVersion {
		return observationFromSystemd("ABSENT", observed), nil
	}
	showOutput, err := runClosed(ctx, "/usr/bin/systemctl", "show", backend.profile.ServiceName,
		"--property=ActiveState", "--property=SubState", "--property=MainPID")
	if err != nil {
		return observationFromSystemd("UNKNOWN", observed), err
	}
	properties := parseSystemdProperties(string(showOutput))
	observed.ActiveState = properties["ActiveState"]
	observed.SubState = properties["SubState"]
	observed.MainPID, _ = strconv.Atoi(properties["MainPID"])
	if observed.ActiveState != "active" || observed.SubState != "running" || observed.MainPID <= 0 {
		return observationFromSystemd("ABSENT", observed), nil
	}
	processPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", observed.MainPID))
	if err != nil {
		return observationFromSystemd("UNKNOWN", observed), err
	}
	if processPath != backend.profile.BinaryPath {
		return observationFromSystemd("ABSENT", observed), nil
	}
	processDigest, err := digestFile(fmt.Sprintf("/proc/%d/exe", observed.MainPID))
	if err != nil {
		return observationFromSystemd("UNKNOWN", observed), err
	}
	observed.ProcessDigest = processDigest
	bootIDRaw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return observationFromSystemd("UNKNOWN", observed), err
	}
	observed.BootID = strings.TrimSpace(string(bootIDRaw))
	observed.ProcessStartTicks, err = readProcessStartTicks(fmt.Sprintf("/proc/%d/stat", observed.MainPID))
	if err != nil {
		return observationFromSystemd("UNKNOWN", observed), err
	}
	healthRaw, err := os.ReadFile(backend.profile.HealthPath)
	if errors.Is(err, os.ErrNotExist) {
		return observationFromSystemd("ABSENT", observed), nil
	}
	if err != nil {
		return observationFromSystemd("UNKNOWN", observed), err
	}
	decoder := json.NewDecoder(bytes.NewReader(healthRaw))
	decoder.DisallowUnknownFields()
	var health systemdHealth
	if err := decoder.Decode(&health); err != nil || health.SchemaVersion != backend.profile.HealthSchema {
		return observationFromSystemd("CONFLICTING", observed), nil
	}
	observed.HealthVersion = health.Version
	observed.HealthReady = health.Ready
	observed.HealthPID = health.PID
	if observed.ProcessDigest != artifact.BinaryDigest || observed.HealthVersion != artifact.PackageVersion || !observed.HealthReady {
		return observationFromSystemd("ABSENT", observed), nil
	}
	if health.PID != observed.MainPID || health.BootID != observed.BootID || health.StartTicks != observed.ProcessStartTicks {
		return observationFromSystemd("ABSENT", observed), nil
	}
	return observationFromSystemd("MATCHED", observed), nil
}

func (backend *SystemdPackageBackend) Apply(ctx context.Context, target Target) error {
	artifact, err := backend.artifactFor(target)
	if err != nil {
		return err
	}
	packageFile, err := os.Open(artifact.PackagePath)
	if err != nil {
		return err
	}
	defer packageFile.Close()
	info, err := packageFile.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("Debian package artifact must be a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, packageFile); err != nil {
		return err
	}
	packageDigest := fmt.Sprintf("%x", hash.Sum(nil))
	if packageDigest != target.TargetArtifactDigest {
		return errors.New("Debian package digest conflicts with Target authority")
	}
	if _, err := packageFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/usr/bin/dpkg", "--install", "/proc/self/fd/3")
	command.ExtraFiles = []*os.File{packageFile}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("dpkg failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := runClosed(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := runClosed(ctx, "/usr/bin/systemctl", "restart", backend.profile.ServiceName); err != nil {
		return err
	}
	return nil
}

func (backend *SystemdPackageBackend) artifactFor(target Target) (SystemdPackageArtifact, error) {
	if target.ComponentType != backend.profile.ComponentType || target.ComponentID != backend.profile.ComponentID {
		return SystemdPackageArtifact{}, errors.New("Target component conflicts with the administrator profile")
	}
	artifact, ok := backend.profile.Artifacts[target.TargetArtifactDigest]
	if !ok {
		return SystemdPackageArtifact{}, errors.New("Target artifact is absent from the administrator profile")
	}
	return artifact, nil
}

func readProcessStartTicks(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	closing := strings.LastIndexByte(string(raw), ')')
	if closing < 0 {
		return 0, errors.New("invalid proc stat")
	}
	fields := strings.Fields(string(raw[closing+1:]))
	if len(fields) <= 19 {
		return 0, errors.New("incomplete proc stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

func observationFromSystemd(state string, observed systemdObservation) Observation {
	raw, _ := json.Marshal(observed)
	return Observation{State: state, Digest: digest(raw)}
}

func parseSystemdProperties(raw string) map[string]string {
	properties := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = value
		}
	}
	return properties
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func runClosed(ctx context.Context, path string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed: %w: %s", filepath.Base(path), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
