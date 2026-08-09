//go:build libvirt && cgo

package libvirtdomain_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	libvirt "libvirt.org/go/libvirt"
)

const qualificationHostID = "host-kvm-libvirt-qualification"

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

func TestKVMHelperProcess(t *testing.T) {
	if os.Getenv("KIM_KVM_HELPER") != "1" {
		t.Skip("qualification child only")
	}
	uri, journalDirectory, uuid, readyPath := os.Getenv("KIM_LIBVIRT_SYSTEM_URI"), os.Getenv("KIM_KVM_JOURNAL"), os.Getenv("KIM_KVM_DOMAIN_UUID"), os.Getenv("KIM_KVM_READY")
	journal, err := executionjournal.Open(journalDirectory, qualificationHostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	backend, closeBackend, err := libvirtdomain.New(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	module, err := agentexecution.NewModule(qualificationHostID, journal, blockingPublisher{readyPath: readyPath}, digest("kvm-libvirt-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"desired_state":"RUNNING"}`)
	lease := commandLease(uuid, payload)
	envelopePayload, _ := json.Marshal(lease)
	envelope := session.NewEnvelope(qualificationHostID, 1, session.StreamCommand, "command-lease/kvm-libvirt/1", contract.CommandLeaseSchema, "command/kvm-libvirt", 1, envelopePayload)
	envelope.CorrelationKey = lease.CommandID
	if err := module.Handle(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
}

func TestKVMProcessKillUnknownReadBack(t *testing.T) {
	uri := os.Getenv("KIM_LIBVIRT_SYSTEM_URI")
	if uri == "" {
		t.Skip("KIM_LIBVIRT_SYSTEM_URI is not set")
	}
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	uuid := randomUUID(t)
	name := "kim-qualification-" + uuid[:8]
	domain, err := connection.DomainDefineXML(fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name><uuid>%s</uuid>
  <memory unit='MiB'>64</memory><currentMemory unit='MiB'>64</currentMemory><vcpu>1</vcpu>
  <os><type arch='x86_64' machine='pc'>hvm</type><boot dev='hd'/></os>
  <features><acpi/></features>
  <clock offset='utc'/><on_poweroff>destroy</on_poweroff><on_reboot>restart</on_reboot><on_crash>destroy</on_crash>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator><controller type='pci' model='pci-root'/><memballoon model='none'/></devices>
</domain>`, name, uuid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		active, _ := domain.IsActive()
		if active {
			_ = domain.Destroy()
		}
		_ = domain.Undefine()
		_ = domain.Free()
	}()

	journalDirectory := filepath.Join(t.TempDir(), "journal")
	readyPath := filepath.Join(t.TempDir(), "result-blocked")
	command := exec.Command(os.Args[0], "-test.run=^TestKVMHelperProcess$")
	command.Env = append(os.Environ(), "KIM_KVM_HELPER=1", "KIM_LIBVIRT_SYSTEM_URI="+uri, "KIM_KVM_JOURNAL="+journalDirectory, "KIM_KVM_DOMAIN_UUID="+uuid, "KIM_KVM_READY="+readyPath)
	output := filepath.Join(t.TempDir(), "child.log")
	logFile, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	eventually(t, 20*time.Second, func() bool {
		active, stateErr := domain.IsActive()
		_, readyErr := os.Stat(readyPath)
		return stateErr == nil && active && readyErr == nil
	}, "typed libvirt mutation did not reach running KVM state before Result transport")
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	_ = logFile.Close()
	active, err := domain.IsActive()
	if err != nil || !active {
		t.Fatalf("KVM Domain did not survive Agent process kill: active=%v err=%v", active, err)
	}

	journal, err := executionjournal.Open(journalDirectory, qualificationHostID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	backend, closeBackend, err := libvirtdomain.New(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	publisher := &capturePublisher{}
	module, err := agentexecution.NewModule(qualificationHostID, journal, publisher, digest("kvm-libvirt-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := module.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"desired_state":"RUNNING"}`)
	request := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema, CommandID: "kvm-libvirt", AttemptIndex: 1, HostID: qualificationHostID, SessionGeneration: 2, CommandType: libvirtdomain.CommandType, CommandSchemaVersion: libvirtdomain.SchemaVersion, TargetResourceID: "vm:" + uuid, CommandPayload: payload, CommandPayloadDigest: digestBytes(payload)}
	requestPayload, _ := json.Marshal(request)
	envelope := session.NewEnvelope(qualificationHostID, 2, session.StreamCommand, "verification-request/kvm-libvirt/1", contract.VerificationRequestSchema, "command/kvm-libvirt", 1, requestPayload)
	envelope.CorrelationKey = request.CommandID
	if err := module.Handle(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	var observed contract.VerificationObservation
	if err := json.Unmarshal(publisher.envelope.Payload, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Observation.State != "MATCHED" || observed.Observation.Evidence["observed_state"] != libvirtdomain.StateRunning {
		t.Fatalf("KVM read-back observation = %#v", observed.Observation)
	}
}

func commandLease(uuid string, payload []byte) contract.CommandLease {
	return contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: "kvm-libvirt", LeaseGeneration: 1, AttemptIndex: 1, HostID: qualificationHostID, HostAuthorityGeneration: 1, SessionGeneration: 1, LeaseToken: "qualification-token", CommandType: libvirtdomain.CommandType, CommandSchemaVersion: libvirtdomain.SchemaVersion, TargetResourceID: "vm:" + uuid, CommandPayload: payload, CommandPayloadDigest: digestBytes(payload), ExecutionTimeoutMillis: 30_000}
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

func randomUUID(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

func digest(value string) string { return digestBytes([]byte(value)) }

func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
