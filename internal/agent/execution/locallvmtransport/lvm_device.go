package locallvmtransport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

var lvmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,126}$`)

type lvsRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type processLVSRunner struct{}

func (processLVSRunner) Output(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, arguments...).CombinedOutput()
}

type blockDevice interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Close() error
}

// LVMDeviceResolver is the real Host adapter behind a transport peer. The
// only administrator input is an immutable VG UUID -> VG name map. LV names
// are derived from Volume IDs and device paths are read back from lvs; no
// caller-controlled path, selector, executable, flag, shell, or argv exists.
type LVMDeviceResolver struct {
	HostID       string
	VolumeGroups map[string]string
	LVSPath      string
	runner       lvsRunner
	openDevice   func(string, int, os.FileMode) (blockDevice, error)
}

func NewLVMDeviceResolver(hostID string, volumeGroups map[string]string) (*LVMDeviceResolver, error) {
	lvs, err := exec.LookPath("lvs")
	if err != nil {
		return nil, err
	}
	return &LVMDeviceResolver{HostID: hostID, VolumeGroups: volumeGroups, LVSPath: lvs, runner: processLVSRunner{}, openDevice: func(path string, flag int, mode os.FileMode) (blockDevice, error) {
		return os.OpenFile(path, flag, mode)
	}}, nil
}

func (r *LVMDeviceResolver) Inspect(ctx context.Context, identity VolumeIdentity) (VolumeState, error) {
	volume, err := r.resolve(ctx, identity)
	if err != nil {
		return VolumeState{}, err
	}
	return VolumeState{SizeBytes: volume.size, HolderOpen: volume.holder}, nil
}

func (r *LVMDeviceResolver) ReadAt(ctx context.Context, identity VolumeIdentity, buffer []byte, offset int64) (int, error) {
	volume, err := r.resolve(ctx, identity)
	if err != nil {
		return 0, err
	}
	device, err := r.openDevice(volume.path, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer device.Close()
	return device.ReadAt(buffer, offset)
}

func (r *LVMDeviceResolver) WriteAt(ctx context.Context, identity VolumeIdentity, buffer []byte, offset int64) (int, error) {
	volume, err := r.resolve(ctx, identity)
	if err != nil {
		return 0, err
	}
	device, err := r.openDevice(volume.path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer device.Close()
	return device.WriteAt(buffer, offset)
}

func (r *LVMDeviceResolver) Flush(ctx context.Context, identity VolumeIdentity) error {
	volume, err := r.resolve(ctx, identity)
	if err != nil {
		return err
	}
	device, err := r.openDevice(volume.path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer device.Close()
	return device.Sync()
}

type resolvedLVMDevice struct {
	path   string
	size   uint64
	holder bool
}

func (r *LVMDeviceResolver) resolve(ctx context.Context, identity VolumeIdentity) (resolvedLVMDevice, error) {
	if r == nil || r.HostID == "" || identity.HostID != r.HostID || identity.VolumeID == "" || identity.BindingID == "" || identity.BindingGeneration == 0 || identity.VGUUID == "" || identity.LVUUID == "" || r.LVSPath == "" || r.runner == nil || r.openDevice == nil {
		return resolvedLVMDevice{}, ErrAuthorityConflict
	}
	vgName, ok := r.VolumeGroups[identity.VGUUID]
	if !ok || !lvmNamePattern.MatchString(vgName) || vgName == "." || vgName == ".." || strings.Contains(vgName, "--") {
		return resolvedLVMDevice{}, ErrAuthorityConflict
	}
	lvName := locallvm.ResourceKey(identity.VolumeID)
	selector := "vg_name=" + vgName + " && lv_name=" + lvName
	output, err := r.runner.Output(ctx, r.LVSPath, "--noheadings", "--units", "b", "--nosuffix", "--separator", "|", "-o", "vg_uuid,lv_uuid,lv_name,lv_size,lv_device_open,lv_dm_path", "--select", selector)
	if err != nil {
		return resolvedLVMDevice{}, fmt.Errorf("read exact Local LVM transport device: %w", err)
	}
	fields := oneLVSRecord(output)
	if len(fields) != 6 || fields[0] != identity.VGUUID || fields[1] != identity.LVUUID || fields[2] != lvName {
		return resolvedLVMDevice{}, ErrAuthorityConflict
	}
	size, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil || size == 0 || fields[4] != "" && fields[4] != "open" {
		return resolvedLVMDevice{}, ErrAuthorityConflict
	}
	devicePath := filepath.Clean(fields[5])
	if !strings.HasPrefix(devicePath, "/dev/mapper/") || devicePath == "/dev/mapper" || strings.Contains(devicePath, "..") {
		return resolvedLVMDevice{}, errors.New("LVM did not return a bounded device-mapper path")
	}
	return resolvedLVMDevice{path: devicePath, size: size, holder: fields[4] == "open"}, nil
}

func oneLVSRecord(output []byte) []string {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var record string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if record != "" {
			return nil
		}
		record = line
	}
	if record == "" {
		return nil
	}
	fields := strings.Split(record, "|")
	for index := range fields {
		fields[index] = strings.TrimSpace(fields[index])
	}
	return fields
}

var _ SourceReader = (*LVMDeviceResolver)(nil)
var _ DestinationWriter = (*LVMDeviceResolver)(nil)
