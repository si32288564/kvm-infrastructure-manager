package locallvm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

const lvmQualificationHostID = "host-local-lvm-qualification"

type blockingPublisher struct{ readyPath string }

func (publisher blockingPublisher) Publish(session.Envelope) error {
	if err := os.WriteFile(publisher.readyPath, []byte("result_constructed_before_transport\n"), 0o600); err != nil {
		return err
	}
	select {}
}

type capturePublisher struct{ envelope session.Envelope }

func (publisher *capturePublisher) Publish(envelope session.Envelope) error {
	publisher.envelope = envelope
	return nil
}

func TestLocalLVMHelperProcess(t *testing.T) {
	if os.Getenv("KIM_LOCAL_LVM_HELPER") != "1" {
		t.Skip("qualification child only")
	}
	runLVMCommand(t, blockingPublisher{readyPath: os.Getenv("KIM_LOCAL_LVM_READY")})
}

func TestLocalLVMProcessKillUnknownReadBack(t *testing.T) {
	vgUUID, vgName := os.Getenv("KIM_LOCAL_LVM_VG_UUID"), os.Getenv("KIM_LOCAL_LVM_VG_NAME")
	if vgUUID == "" || vgName == "" {
		t.Skip("KIM_LOCAL_LVM_VG_UUID and KIM_LOCAL_LVM_VG_NAME are not set")
	}
	volumeID := "qualification-volume-20260810"
	journalDirectory := filepath.Join(t.TempDir(), "journal")
	readyPath := filepath.Join(t.TempDir(), "result-blocked")
	command := exec.Command(os.Args[0], "-test.run=^TestLocalLVMHelperProcess$")
	command.Env = append(os.Environ(), "KIM_LOCAL_LVM_HELPER=1", "KIM_LOCAL_LVM_JOURNAL="+journalDirectory, "KIM_LOCAL_LVM_READY="+readyPath)
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
	client := mustClient(t)
	eventually(t, 20*time.Second, func() bool {
		if _, err := os.Stat(readyPath); err != nil {
			return false
		}
		lv, found, err := client.LogicalVolume(context.Background(), vgName, locallvm.ResourceKey(volumeID))
		return err == nil && found && lv.VGUUID == vgUUID && lv.LVUUID != "" && lv.SizeBytes == 64*locallvm.MiB
	}, "typed Local LVM mutation did not reach read-back before Result transport")
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	_ = logFile.Close()

	lvBefore, found, err := client.LogicalVolume(t.Context(), vgName, locallvm.ResourceKey(volumeID))
	if err != nil || !found || lvBefore.LVUUID == "" {
		t.Fatalf("LV did not survive Agent process kill: found=%v lv=%#v err=%v", found, lvBefore, err)
	}

	journal, err := executionjournal.Open(journalDirectory, lvmQualificationHostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	publisher := &capturePublisher{}
	module, err := agentexecution.NewModule(lvmQualificationHostID, journal, publisher, digest("local-lvm-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(locallvm.Backend{Client: client, VolumeGroups: map[string]string{vgUUID: vgName}}); err != nil {
		t.Fatal(err)
	}
	payload := commandPayload(vgUUID)
	request := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema, CommandID: "local-lvm-create", AttemptIndex: 1, HostID: lvmQualificationHostID, SessionGeneration: 2, CommandType: locallvm.CommandType, CommandSchemaVersion: locallvm.SchemaVersion, TargetResourceID: "volume:" + volumeID, CommandPayload: payload, CommandPayloadDigest: digestBytes(payload)}
	requestPayload, _ := json.Marshal(request)
	envelope := session.NewEnvelope(lvmQualificationHostID, 2, session.StreamCommand, "verification-request/local-lvm-create/1", contract.VerificationRequestSchema, "command/local-lvm-create", 1, requestPayload)
	envelope.CorrelationKey = request.CommandID
	if err := module.Handle(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	var observed contract.VerificationObservation
	if err := json.Unmarshal(publisher.envelope.Payload, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Observation.State != "MATCHED" || observed.Observation.Evidence["observed_lv_uuid"] != lvBefore.LVUUID || observed.Observation.Evidence["backend_resource_key"] != locallvm.ResourceKey(volumeID) {
		t.Fatalf("Local LVM read-back observation = %#v", observed.Observation)
	}
}

func runLVMCommand(t *testing.T, publisher agentexecution.Publisher) {
	t.Helper()
	vgUUID, vgName := os.Getenv("KIM_LOCAL_LVM_VG_UUID"), os.Getenv("KIM_LOCAL_LVM_VG_NAME")
	journal, err := executionjournal.Open(os.Getenv("KIM_LOCAL_LVM_JOURNAL"), lvmQualificationHostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	module, err := agentexecution.NewModule(lvmQualificationHostID, journal, publisher, digest("local-lvm-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(locallvm.Backend{Client: mustClient(t), VolumeGroups: map[string]string{vgUUID: vgName}}); err != nil {
		t.Fatal(err)
	}
	payload := commandPayload(vgUUID)
	lease := contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: "local-lvm-create", LeaseGeneration: 1, AttemptIndex: 1, HostID: lvmQualificationHostID, HostAuthorityGeneration: 1, SessionGeneration: 1, LeaseToken: "qualification-token", CommandType: locallvm.CommandType, CommandSchemaVersion: locallvm.SchemaVersion, TargetResourceID: "volume:qualification-volume-20260810", CommandPayload: payload, CommandPayloadDigest: digestBytes(payload), ExecutionTimeoutMillis: 30_000}
	envelopePayload, _ := json.Marshal(lease)
	envelope := session.NewEnvelope(lvmQualificationHostID, 1, session.StreamCommand, "command-lease/local-lvm-create/1", contract.CommandLeaseSchema, "command/local-lvm-create", 1, envelopePayload)
	envelope.CorrelationKey = lease.CommandID
	if err := module.Handle(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
}

func mustClient(t *testing.T) *locallvm.CLIClient {
	t.Helper()
	client, err := locallvm.NewCLIClient()
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func commandPayload(vgUUID string) []byte {
	payload, _ := json.Marshal(map[string]any{"vg_uuid": vgUUID, "size_mib": 64, "desired_state": "PRESENT"})
	return payload
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

func digest(value string) string { return digestBytes([]byte(value)) }
func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
