package locallvmtransport

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

type fixedLVS struct {
	output []byte
	args   []string
}

func (f *fixedLVS) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.args = append([]string(nil), args...)
	return f.output, nil
}

type memoryDevice struct{ *bytes.Reader }

func (m *memoryDevice) WriteAt([]byte, int64) (int, error) { return 0, io.ErrClosedPipe }
func (m *memoryDevice) Sync() error                        { return nil }
func (m *memoryDevice) Close() error                       { return nil }

func TestLVMDeviceResolverDerivesClosedDeviceFromStableIdentity(t *testing.T) {
	identity := VolumeIdentity{HostID: "host-a", VolumeID: "root-volume", BindingID: "binding-1", BindingGeneration: 3, VGUUID: "vg-uuid", LVUUID: "lv-uuid"}
	lvName := locallvm.ResourceKey(identity.VolumeID)
	runner := &fixedLVS{output: []byte("vg-uuid|lv-uuid|" + lvName + "|8192||/dev/mapper/vg--kim-" + lvName + "\n")}
	opened := ""
	resolver := &LVMDeviceResolver{HostID: "host-a", VolumeGroups: map[string]string{"vg-uuid": "vg-kim"}, LVSPath: "/sbin/lvs", runner: runner, openDevice: func(path string, flag int, _ os.FileMode) (blockDevice, error) {
		opened = path
		if flag != os.O_RDONLY {
			t.Fatalf("source opened with flag %d", flag)
		}
		return &memoryDevice{bytes.NewReader(make([]byte, 8192))}, nil
	}}
	state, err := resolver.Inspect(t.Context(), identity)
	if err != nil || state.SizeBytes != 8192 || state.HolderOpen {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if _, err := resolver.ReadAt(t.Context(), identity, make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	if opened == "" || !strings.HasPrefix(opened, "/dev/mapper/") || strings.Contains(strings.Join(runner.args, " "), identity.BindingID) {
		t.Fatalf("opened=%q args=%q", opened, runner.args)
	}
}

func TestLVMDeviceResolverRejectsWrongHostLVAndUnboundedPath(t *testing.T) {
	identity := VolumeIdentity{HostID: "host-a", VolumeID: "root-volume", BindingID: "binding-1", BindingGeneration: 3, VGUUID: "vg-uuid", LVUUID: "lv-uuid"}
	lvName := locallvm.ResourceKey(identity.VolumeID)
	for name, mutate := range map[string]func(*VolumeIdentity, *fixedLVS){
		"wrong Host": func(i *VolumeIdentity, _ *fixedLVS) { i.HostID = "host-b" },
		"wrong LV": func(_ *VolumeIdentity, r *fixedLVS) {
			r.output = []byte("vg-uuid|other|" + lvName + "|8192||/dev/mapper/good\n")
		},
		"path": func(_ *VolumeIdentity, r *fixedLVS) {
			r.output = []byte("vg-uuid|lv-uuid|" + lvName + "|8192||/tmp/caller-device\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := identity
			runner := &fixedLVS{output: []byte("vg-uuid|lv-uuid|" + lvName + "|8192||/dev/mapper/good\n")}
			mutate(&candidate, runner)
			resolver := &LVMDeviceResolver{HostID: "host-a", VolumeGroups: map[string]string{"vg-uuid": "vg-kim"}, LVSPath: "/sbin/lvs", runner: runner, openDevice: func(string, int, os.FileMode) (blockDevice, error) { return nil, io.ErrClosedPipe }}
			if _, err := resolver.Inspect(t.Context(), candidate); err == nil {
				t.Fatal("unsafe device identity accepted")
			}
		})
	}
}
