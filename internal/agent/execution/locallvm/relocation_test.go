package locallvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type memoryRelocationVolume struct {
	bytes  []byte
	holder bool
}
type memoryRelocationClient struct {
	volumes                    map[string]*memoryRelocationVolume
	mutateSourceAfterFirstRead bool
	reads                      int
}

func relocationKey(i RelocationVolumeIdentity) string {
	return i.HostID + "/" + i.VolumeID + "/" + i.BindingID + "/" + i.LVUUID
}
func (c *memoryRelocationClient) Inspect(_ context.Context, i RelocationVolumeIdentity) (RelocationVolumeState, error) {
	v, ok := c.volumes[relocationKey(i)]
	if !ok {
		return RelocationVolumeState{}, errors.New("unknown exact Volume identity")
	}
	return RelocationVolumeState{SizeBytes: uint64(len(v.bytes)), HolderOpen: v.holder}, nil
}
func (c *memoryRelocationClient) ReadAt(_ context.Context, i RelocationVolumeIdentity, p []byte, off int64) (int, error) {
	v, ok := c.volumes[relocationKey(i)]
	if !ok {
		return 0, errors.New("unknown exact Volume identity")
	}
	n := copy(p, v.bytes[off:])
	c.reads++
	if c.mutateSourceAfterFirstRead && i.HostID == "host-a" && c.reads == 2 {
		v.bytes[len(v.bytes)-1] ^= 0xff
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (c *memoryRelocationClient) WriteAt(_ context.Context, i RelocationVolumeIdentity, p []byte, off int64) (int, error) {
	v, ok := c.volumes[relocationKey(i)]
	if !ok {
		return 0, errors.New("unknown exact Volume identity")
	}
	return copy(v.bytes[off:], p), nil
}

func relocationLease(t *testing.T, payload map[string]any) contract.CommandLease {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return contract.CommandLease{TargetResourceID: "local-lvm-relocation:copy-1", CommandPayload: raw, CommandPayloadDigest: hex.EncodeToString(sum[:]), AttemptIndex: 1}
}
func relocationPayload(size uint64) map[string]any {
	return map[string]any{"copy_operation_id": "copy-1", "copy_generation": 1, "source_host_id": "host-a", "source_volume_id": "source-volume", "source_binding_id": "source-binding", "source_binding_generation": 1, "source_vg_uuid": "vg-a", "source_lv_uuid": "lv-a", "destination_host_id": "host-b", "destination_volume_id": "destination-volume", "destination_binding_id": "destination-binding", "destination_binding_generation": 1, "destination_vg_uuid": "vg-b", "destination_lv_uuid": "lv-b", "exact_byte_count": size, "digest_algorithm": "SHA-256", "copy_policy_revision": 1, "desired_state": "CONTENT_IDENTICAL"}
}
func relocationFixture(size int) (*memoryRelocationClient, RelocationVolumeIdentity, RelocationVolumeIdentity) {
	source := RelocationVolumeIdentity{"host-a", "source-volume", "source-binding", "vg-a", "lv-a", 1}
	destination := RelocationVolumeIdentity{"host-b", "destination-volume", "destination-binding", "vg-b", "lv-b", 1}
	content := make([]byte, size)
	copy(content, []byte("base-image\x00unique-guest-mutation-marker"))
	copy(content[size-64:], []byte("second-marker-near-end-of-volume"))
	return &memoryRelocationClient{volumes: map[string]*memoryRelocationVolume{relocationKey(source): {bytes: content}, relocationKey(destination): {bytes: make([]byte, size)}}}, source, destination
}

func TestRelocationCopiesMutatedGuestDataAndReadsBackWholeDigest(t *testing.T) {
	client, source, destination := relocationFixture(2*relocationChunkBytes + 137)
	metrics := &RelocationMetrics{}
	backend := RelocationBackend{Client: client, Metrics: metrics}
	result, err := backend.Execute(context.Background(), relocationLease(t, relocationPayload(uint64(len(client.volumes[relocationKey(source)].bytes)))))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "SUCCEEDED" || result.Observation.State != "MATCHED" {
		t.Fatalf("result=%+v", result)
	}
	if string(client.volumes[relocationKey(source)].bytes) != string(client.volumes[relocationKey(destination)].bytes) {
		t.Fatal("destination did not preserve source guest markers")
	}
	snapshot := metrics.Snapshot()
	if snapshot.Attempts != 1 || snapshot.Bytes != uint64(len(client.volumes[relocationKey(source)].bytes)) || snapshot.Active != 0 {
		t.Fatalf("metrics=%+v", snapshot)
	}
	verification := contract.VerificationRequest{TargetResourceID: "local-lvm-relocation:copy-1", CommandPayload: relocationLease(t, relocationPayload(uint64(len(client.volumes[relocationKey(source)].bytes)))).CommandPayload, AttemptIndex: 2}
	observed, err := backend.Observe(context.Background(), verification)
	if err != nil || observed.State != "MATCHED" {
		t.Fatalf("read-back=%+v err=%v", observed, err)
	}
}

func TestRelocationRejectsPartialCorruptHolderDriftAndOpenInput(t *testing.T) {
	t.Run("partial destination", func(t *testing.T) {
		client, source, destination := relocationFixture(4096)
		client.volumes[relocationKey(destination)].bytes = make([]byte, 2048)
		backend := RelocationBackend{Client: client}
		if _, err := backend.Execute(context.Background(), relocationLease(t, relocationPayload(uint64(len(client.volumes[relocationKey(source)].bytes))))); err == nil {
			t.Fatal("partial/smaller destination accepted")
		}
	})
	t.Run("destination corruption", func(t *testing.T) {
		client, source, destination := relocationFixture(4096)
		copy(client.volumes[relocationKey(destination)].bytes, client.volumes[relocationKey(source)].bytes)
		client.volumes[relocationKey(destination)].bytes[2048] ^= 0xff
		backend := RelocationBackend{Client: client}
		request := relocationLease(t, relocationPayload(4096))
		observed, err := backend.Observe(context.Background(), contract.VerificationRequest{TargetResourceID: request.TargetResourceID, CommandPayload: request.CommandPayload, AttemptIndex: 1})
		if err != nil || observed.State != "CONFLICTING" {
			t.Fatalf("corruption=%+v err=%v", observed, err)
		}
	})
	t.Run("partial copy at full size", func(t *testing.T) {
		client, source, destination := relocationFixture(4096)
		copy(client.volumes[relocationKey(destination)].bytes[:2048], client.volumes[relocationKey(source)].bytes[:2048])
		backend := RelocationBackend{Client: client}
		request := relocationLease(t, relocationPayload(4096))
		observed, err := backend.Observe(context.Background(), contract.VerificationRequest{TargetResourceID: request.TargetResourceID, CommandPayload: request.CommandPayload, AttemptIndex: 1})
		if err != nil || observed.State != "CONFLICTING" {
			t.Fatalf("partial copy=%+v err=%v", observed, err)
		}
	})
	t.Run("wrong exact source or destination", func(t *testing.T) {
		client, _, _ := relocationFixture(4096)
		for _, key := range []string{"source_lv_uuid", "source_binding_id", "destination_lv_uuid", "destination_binding_id"} {
			payload := relocationPayload(4096)
			payload[key] = "wrong-exact-identity"
			if _, err := (RelocationBackend{Client: client}).Execute(context.Background(), relocationLease(t, payload)); err == nil {
				t.Fatalf("%s mismatch accepted", key)
			}
		}
	})
	t.Run("source holder", func(t *testing.T) {
		client, source, _ := relocationFixture(4096)
		client.volumes[relocationKey(source)].holder = true
		if _, err := (RelocationBackend{Client: client}).Execute(context.Background(), relocationLease(t, relocationPayload(4096))); err == nil {
			t.Fatal("source holder accepted")
		}
	})
	t.Run("source drift", func(t *testing.T) {
		client, _, _ := relocationFixture(2 * relocationChunkBytes)
		client.mutateSourceAfterFirstRead = true
		if _, err := (RelocationBackend{Client: client}).Execute(context.Background(), relocationLease(t, relocationPayload(2*relocationChunkBytes))); err == nil {
			t.Fatal("source drift accepted")
		}
	})
	t.Run("arbitrary path and argv", func(t *testing.T) {
		client, _, _ := relocationFixture(4096)
		payload := relocationPayload(4096)
		payload["source_path"] = "/dev/mapper/other"
		if _, err := (RelocationBackend{Client: client}).Execute(context.Background(), relocationLease(t, payload)); err == nil {
			t.Fatal("arbitrary path accepted")
		}
		payload = relocationPayload(4096)
		payload["argv"] = []string{"dd", "if=/dev/other"}
		if _, err := (RelocationBackend{Client: client}).Execute(context.Background(), relocationLease(t, payload)); err == nil {
			t.Fatal("argv accepted")
		}
	})
}
