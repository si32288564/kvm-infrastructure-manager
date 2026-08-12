//go:build libvirt && cgo

// kim-real-kvm-recovery-helper is an explicitly opted-in, lab-only adapter
// used by the two-Host Recovery qualification. It exposes only the existing
// closed typed Agent backends and never accepts XML, a device path, an LVM
// command, a libvirt method, flags, or argv.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/localimage"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/qualification/recoveryauthority"
)

const optIn = "KIM_REAL_KVM_RECOVERY_QUALIFICATION"

type request struct {
	ExpectedHostname string          `json:"expected_hostname"`
	HostID           string          `json:"host_id"`
	CommandID        string          `json:"command_id"`
	CommandType      string          `json:"command_type"`
	SchemaVersion    string          `json:"schema_version"`
	TargetResourceID string          `json:"target_resource_id"`
	Payload          json.RawMessage `json:"payload"`
	VGName           string          `json:"vg_name"`
	VGUUID           string          `json:"vg_uuid"`
	CacheRoot        string          `json:"cache_root"`
	StateRoot        string          `json:"state_root"`
	AttemptIndex     int             `json:"attempt_index"`
	AuthorityGen     int64           `json:"host_authority_generation"`
	SessionGen       int64           `json:"session_generation"`
	LeaseGeneration  int64           `json:"lease_generation"`
	LeaseToken       string          `json:"lease_token"`
}

type response struct {
	HostID, Hostname, CommandID, CommandType string
	Result                                   recoveryauthority.ResultEvidence
	Observation                              contract.Observation
}

type capturePublisher struct{ result contract.CommandResult }

func (publisher *capturePublisher) Publish(envelope session.Envelope) error {
	if envelope.Stream != session.StreamResult {
		return nil
	}
	var result contract.CommandResult
	if err := json.Unmarshal(envelope.Payload, &result); err != nil {
		return err
	}
	publisher.result = result
	return nil
}

func main() {
	flag.Parse()
	if os.Getenv(optIn) != "1" {
		log.Fatal("explicit real KVM Recovery qualification opt-in is required")
	}
	var desired request
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&desired); err != nil {
		log.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		log.Fatal("trailing request data")
	}
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	if err := validate(desired, hostname); err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	lvmClient, err := locallvm.NewCLIClient()
	if err != nil {
		log.Fatal(err)
	}
	if err := lvmClient.VerifyVolumeGroup(ctx, desired.VGName, desired.VGUUID); err != nil {
		log.Fatal(err)
	}
	volumeGroups := map[string]string{desired.VGUUID: desired.VGName}
	resolver := libvirtvolume.LocalLVMResolver{Client: lvmClient, VolumeGroups: volumeGroups}
	volumeBackend := locallvm.Backend{Client: lvmClient, VolumeGroups: volumeGroups}
	imageBackend := localimage.Backend{CacheRoot: desired.CacheRoot, Volumes: resolver}
	vmBackend, closeVM, err := libvirtvm.New("qemu:///system", resolver)
	if err != nil {
		log.Fatal(err)
	}
	defer closeVM()
	attachmentBackend, closeAttachment, err := libvirtvolume.New("qemu:///system", lvmClient, volumeGroups)
	if err != nil {
		log.Fatal(err)
	}
	defer closeAttachment()
	rootSafetyBackend := libvirtvolume.SourceRootSafetyBackend{Attachment: attachmentBackend}
	powerBackend, closePower, err := libvirtdomain.New("qemu:///system")
	if err != nil {
		log.Fatal(err)
	}
	defer closePower()

	journal, err := executionjournal.Open(filepath.Join(desired.StateRoot, "journal"), desired.HostID)
	if err != nil {
		log.Fatal(err)
	}
	defer journal.Close()
	publisher := &capturePublisher{}
	module, err := agentexecution.NewModule(desired.HostID, journal, publisher, digest("real-kvm-recovery-verifier/v1"))
	if err != nil {
		log.Fatal(err)
	}
	for _, backend := range []agentexecution.Backend{volumeBackend, imageBackend, *vmBackend, *attachmentBackend, rootSafetyBackend, *powerBackend} {
		if err := module.RegisterBackend(backend); err != nil {
			log.Fatal(err)
		}
	}

	payloadDigest := digestBytes(desired.Payload)
	lease := contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: desired.CommandID,
		LeaseGeneration: desired.LeaseGeneration, AttemptIndex: desired.AttemptIndex, HostID: desired.HostID,
		HostAuthorityGeneration: desired.AuthorityGen, SessionGeneration: desired.SessionGen,
		LeaseToken: desired.LeaseToken, CommandType: desired.CommandType,
		CommandSchemaVersion: desired.SchemaVersion, TargetResourceID: desired.TargetResourceID,
		CommandPayload: desired.Payload, CommandPayloadDigest: payloadDigest, ExecutionTimeoutMillis: 120_000}
	encoded, _ := json.Marshal(lease)
	envelope := session.NewEnvelope(desired.HostID, uint64(desired.SessionGen), session.StreamCommand,
		"real-kvm-recovery/"+desired.CommandID, contract.CommandLeaseSchema, desired.CommandID, 1, encoded)
	envelope.CorrelationKey = desired.CommandID
	if err := module.Handle(ctx, envelope); err != nil {
		log.Fatal(err)
	}
	if publisher.result.CommandID != desired.CommandID || publisher.result.Outcome != "SUCCEEDED" {
		log.Fatalf("typed backend result is not successful: %+v", publisher.result)
	}
	verification := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema,
		CommandID: desired.CommandID, AttemptIndex: desired.AttemptIndex, HostID: desired.HostID,
		SessionGeneration: desired.SessionGen, CommandType: desired.CommandType,
		CommandSchemaVersion: desired.SchemaVersion, TargetResourceID: desired.TargetResourceID,
		CommandPayload: desired.Payload, CommandPayloadDigest: payloadDigest}
	observation, err := observe(ctx, desired.CommandType, volumeBackend, imageBackend, *vmBackend, *attachmentBackend, rootSafetyBackend, *powerBackend, verification)
	if err != nil {
		log.Fatal(err)
	}
	if observation.State != "MATCHED" {
		log.Fatalf("typed backend read-back is not MATCHED: %+v", observation)
	}
	if err := json.NewEncoder(os.Stdout).Encode(response{HostID: desired.HostID, Hostname: hostname,
		CommandID: desired.CommandID, CommandType: desired.CommandType, Result: recoveryauthority.Redact(publisher.result),
		Observation: observation}); err != nil {
		log.Fatal(err)
	}
}

func validate(desired request, hostname string) error {
	if desired.ExpectedHostname == "" || desired.ExpectedHostname != hostname || desired.HostID != hostname {
		return errors.New("exact Host allow-list identity mismatch")
	}
	if !strings.HasPrefix(desired.CommandID, "real-recovery-") || desired.TargetResourceID == "" || len(desired.Payload) == 0 {
		return errors.New("dedicated qualification command identity is required")
	}
	if desired.VGName == "" || !strings.HasPrefix(desired.VGName, "kimrr_") || desired.VGUUID == "" {
		return errors.New("dedicated qualification VG identity is required")
	}
	if desired.CacheRoot == "" || filepath.Clean(desired.CacheRoot) != desired.CacheRoot || !strings.HasPrefix(desired.CacheRoot, "/var/tmp/kim-real-recovery-") {
		return errors.New("dedicated qualification cache root is required")
	}
	if desired.StateRoot == "" || filepath.Clean(desired.StateRoot) != desired.StateRoot || !strings.HasPrefix(desired.StateRoot, "/var/tmp/kim-real-recovery-") {
		return errors.New("dedicated qualification state root is required")
	}
	if desired.AttemptIndex < 1 || desired.AttemptIndex > 32 || desired.AuthorityGen < 1 || desired.SessionGen < 1 || desired.LeaseGeneration < 1 || desired.LeaseToken == "" {
		return errors.New("current qualification authority generations are required")
	}
	wantSchema := map[string]string{
		locallvm.CommandType:                              locallvm.SchemaVersion,
		localimage.CommandType:                            localimage.SchemaVersion,
		libvirtvm.CommandType:                             libvirtvm.SchemaVersion,
		libvirtvolume.CommandType:                         libvirtvolume.SchemaVersion,
		libvirtvolume.SourceRootSafetyReadBackCommandType: libvirtvolume.SourceRootSafetyReadBackSchema,
		libvirtdomain.CommandType:                         libvirtdomain.SchemaVersion,
	}[desired.CommandType]
	if wantSchema == "" || desired.SchemaVersion != wantSchema {
		return fmt.Errorf("unsupported typed command %q/%q", desired.CommandType, desired.SchemaVersion)
	}
	return nil
}

func observe(ctx context.Context, commandType string, volume locallvm.Backend, image localimage.Backend, vm libvirtvm.Backend, attachment libvirtvolume.Backend, root libvirtvolume.SourceRootSafetyBackend, power libvirtdomain.Backend, request contract.VerificationRequest) (contract.Observation, error) {
	switch commandType {
	case locallvm.CommandType:
		return volume.Observe(ctx, request)
	case localimage.CommandType:
		return image.Observe(ctx, request)
	case libvirtvm.CommandType:
		return vm.Observe(ctx, request)
	case libvirtvolume.CommandType:
		return attachment.Observe(ctx, request)
	case libvirtvolume.SourceRootSafetyReadBackCommandType:
		return root.Observe(ctx, request)
	case libvirtdomain.CommandType:
		return power.Observe(ctx, request)
	default:
		return contract.Observation{}, errors.New("unsupported typed command")
	}
}

func digest(value string) string { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
