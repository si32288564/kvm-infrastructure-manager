//go:build libvirt && cgo

package libvirtvolume_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	libvirt "libvirt.org/go/libvirt"
)

const attachmentQualificationHostID = "host-local-lvm-attachment-qualification"

type blockingPublisher struct{ readyPath string }

func (publisher blockingPublisher) Publish(envelope session.Envelope) error {
	if err := os.WriteFile(publisher.readyPath, envelope.Payload, 0o600); err != nil {
		return err
	}
	select {}
}

type capturePublisher struct{ envelope session.Envelope }

func (publisher *capturePublisher) Publish(envelope session.Envelope) error {
	publisher.envelope = envelope
	return nil
}

type diagnosticBackend struct {
	inner     *libvirtvolume.Backend
	errorPath string
}

func (backend diagnosticBackend) CommandType() string   { return backend.inner.CommandType() }
func (backend diagnosticBackend) SchemaVersion() string { return backend.inner.SchemaVersion() }
func (backend diagnosticBackend) Execute(ctx context.Context, lease contract.CommandLease) (agentexecution.BackendResult, error) {
	result, err := backend.inner.Execute(ctx, lease)
	if err != nil {
		_ = os.WriteFile(backend.errorPath, []byte(err.Error()), 0o600)
	}
	return result, err
}

func TestLibvirtVolumeHelperProcess(t *testing.T) {
	if os.Getenv("KIM_LIBVIRT_VOLUME_HELPER") != "1" {
		t.Skip("qualification child only")
	}
	backend, closeBackend := qualificationBackend(t)
	defer closeBackend()
	journal, err := executionjournal.Open(os.Getenv("KIM_LIBVIRT_VOLUME_JOURNAL"), attachmentQualificationHostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	module, err := agentexecution.NewModule(attachmentQualificationHostID, journal, blockingPublisher{readyPath: os.Getenv("KIM_LIBVIRT_VOLUME_READY")}, digest("libvirt-volume-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(diagnosticBackend{inner: backend, errorPath: os.Getenv("KIM_LIBVIRT_VOLUME_BACKEND_ERROR")}); err != nil {
		t.Fatal(err)
	}
	desired, commandID := os.Getenv("KIM_LIBVIRT_VOLUME_DESIRED"), os.Getenv("KIM_LIBVIRT_VOLUME_COMMAND_ID")
	sessionGeneration, err := strconv.ParseUint(os.Getenv("KIM_LIBVIRT_VOLUME_SESSION_GENERATION"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	payload := qualificationPayload(t, desired)
	lease := contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: commandID, LeaseGeneration: 1, AttemptIndex: 1, HostID: attachmentQualificationHostID, HostAuthorityGeneration: 1, SessionGeneration: int64(sessionGeneration), LeaseToken: "qualification-token", CommandType: libvirtvolume.CommandType, CommandSchemaVersion: libvirtvolume.SchemaVersion, TargetResourceID: "attachment:qualification-attachment", CommandPayload: payload, CommandPayloadDigest: digestBytes(payload), ExecutionTimeoutMillis: 30_000}
	envelopePayload, _ := json.Marshal(lease)
	envelope := session.NewEnvelope(attachmentQualificationHostID, sessionGeneration, session.StreamCommand, "command-lease/"+commandID+"/1", contract.CommandLeaseSchema, "command/"+commandID, 1, envelopePayload)
	envelope.CorrelationKey = commandID
	if err := module.Handle(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
}

func TestLibvirtVolumeAttachDetachProcessKillReadBack(t *testing.T) {
	if os.Getenv("KIM_LIBVIRT_SYSTEM_URI") == "" || os.Getenv("KIM_LOCAL_LVM_VG_UUID") == "" || os.Getenv("KIM_LOCAL_LVM_VG_NAME") == "" || os.Getenv("KIM_LOCAL_LVM_LV_UUID") == "" || os.Getenv("KIM_KVM_DOMAIN_UUID") == "" {
		t.Skip("complete KVM Local LVM qualification environment is not set")
	}
	journalDirectory := filepath.Join(t.TempDir(), "journal")
	runKilledMutation(t, journalDirectory, "ATTACHED", "attachment-attach", 1)
	t.Log("attach mutation reached Result and Agent subprocess was killed")
	verifyEventually(t, journalDirectory, "ATTACHED", "attachment-attach", 2)
	t.Log("attach device and holder read-back matched")
	stopQualificationDomain(t)
	t.Log("Domain entered maintenance shutoff before cold detach")
	runKilledMutation(t, journalDirectory, "DETACHED", "attachment-detach", 2)
	t.Log("detach mutation reached Result and Agent subprocess was killed")
	verifyEventually(t, journalDirectory, "DETACHED", "attachment-detach", 3)
	t.Log("detach absence and holder release read-back matched")
}

func stopQualificationDomain(t *testing.T) {
	t.Helper()
	connection, err := libvirt.NewConnect(os.Getenv("KIM_LIBVIRT_SYSTEM_URI"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	domain, err := connection.LookupDomainByUUIDString(os.Getenv("KIM_KVM_DOMAIN_UUID"))
	if err != nil {
		t.Fatal(err)
	}
	defer domain.Free()
	active, err := domain.IsActive()
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if err := domain.Destroy(); err != nil {
			t.Fatal(err)
		}
	}
}

func runKilledMutation(t *testing.T, journalDirectory, desired, commandID string, sessionGeneration uint64) {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "result-blocked")
	backendErrorPath := filepath.Join(t.TempDir(), "backend-error")
	command := exec.Command(os.Args[0], "-test.run=^TestLibvirtVolumeHelperProcess$")
	command.Env = append(os.Environ(), "KIM_LIBVIRT_VOLUME_HELPER=1", "KIM_LIBVIRT_VOLUME_JOURNAL="+journalDirectory, "KIM_LIBVIRT_VOLUME_READY="+readyPath, "KIM_LIBVIRT_VOLUME_BACKEND_ERROR="+backendErrorPath, "KIM_LIBVIRT_VOLUME_DESIRED="+desired, "KIM_LIBVIRT_VOLUME_COMMAND_ID="+commandID, "KIM_LIBVIRT_VOLUME_SESSION_GENERATION="+strconv.FormatUint(sessionGeneration, 10))
	logPath := filepath.Join(t.TempDir(), "child.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	eventually(t, 30*time.Second, func() bool { _, err := os.Stat(readyPath); return err == nil }, "typed libvirt Volume mutation did not construct Result before timeout")
	resultPayload, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	var result contract.CommandResult
	if err := json.Unmarshal(resultPayload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		backendError, _ := os.ReadFile(backendErrorPath)
		t.Fatalf("typed %s mutation did not converge before kill: backend=%q result=%#v", desired, backendError, result)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	t.Logf("killed helper for %s", desired)
	_ = logFile.Close()
}

func verifyEventually(t *testing.T, journalDirectory, desired, commandID string, sessionGeneration uint64) {
	t.Helper()
	backend, closeBackend := qualificationBackend(t)
	defer closeBackend()
	payload := qualificationPayload(t, desired)
	request := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema, CommandID: commandID, AttemptIndex: 1, HostID: attachmentQualificationHostID, SessionGeneration: int64(sessionGeneration), CommandType: libvirtvolume.CommandType, CommandSchemaVersion: libvirtvolume.SchemaVersion, TargetResourceID: "attachment:qualification-attachment", CommandPayload: payload, CommandPayloadDigest: digestBytes(payload)}
	eventually(t, 30*time.Second, func() bool {
		observation, err := backend.Observe(context.Background(), request)
		return err == nil && observation.State == "MATCHED"
	}, "typed libvirt device/LVM holder read-back did not converge")
	journal, err := executionjournal.Open(journalDirectory, attachmentQualificationHostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	publisher := &capturePublisher{}
	module, err := agentexecution.NewModule(attachmentQualificationHostID, journal, publisher, digest("libvirt-volume-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	requestPayload, _ := json.Marshal(request)
	envelope := session.NewEnvelope(attachmentQualificationHostID, sessionGeneration, session.StreamCommand, "verification-request/"+commandID+"/1", contract.VerificationRequestSchema, "command/"+commandID, 1, requestPayload)
	envelope.CorrelationKey = commandID
	if err := module.Handle(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	var observed contract.VerificationObservation
	if err := json.Unmarshal(publisher.envelope.Payload, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Observation.State != "MATCHED" || observed.Observation.Evidence["desired_state"] != desired || observed.Observation.Evidence["observed_lv_uuid"] != os.Getenv("KIM_LOCAL_LVM_LV_UUID") {
		t.Fatalf("typed attachment read-back = %#v", observed.Observation)
	}
}

func qualificationBackend(t *testing.T) (*libvirtvolume.Backend, func() error) {
	t.Helper()
	client, err := locallvm.NewCLIClient()
	if err != nil {
		t.Fatal(err)
	}
	backend, closeBackend, err := libvirtvolume.New(os.Getenv("KIM_LIBVIRT_SYSTEM_URI"), client, map[string]string{os.Getenv("KIM_LOCAL_LVM_VG_UUID"): os.Getenv("KIM_LOCAL_LVM_VG_NAME")})
	if err != nil {
		t.Fatal(err)
	}
	return backend, closeBackend
}

func qualificationPayload(t *testing.T, desired string) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"domain_uuid": os.Getenv("KIM_KVM_DOMAIN_UUID"), "volume_id": "qualification-volume-attachment-20260810", "vg_uuid": os.Getenv("KIM_LOCAL_LVM_VG_UUID"), "lv_uuid": os.Getenv("KIM_LOCAL_LVM_LV_UUID"), "backend_resource_key": locallvm.ResourceKey("qualification-volume-attachment-20260810"), "disk_slot": 1, "desired_state": desired, "access_mode": "SINGLE_WRITER"})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(message)
}

func digest(value string) string { return digestBytes([]byte(value)) }
func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
