package locallvm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CLIClient is a narrow standard-LVM adapter. Every executable and argument
// shape is fixed by KIM; callers cannot supply paths, flags, or selectors.
type CLIClient struct {
	VGSPath      string
	LVSPath      string
	LVCreatePath string
	LVRemovePath string
}

func NewCLIClient() (*CLIClient, error) {
	vgs, err := exec.LookPath("vgs")
	if err != nil {
		return nil, err
	}
	lvs, err := exec.LookPath("lvs")
	if err != nil {
		return nil, err
	}
	lvcreate, err := exec.LookPath("lvcreate")
	if err != nil {
		return nil, err
	}
	lvremove, err := exec.LookPath("lvremove")
	if err != nil {
		return nil, err
	}
	return &CLIClient{VGSPath: vgs, LVSPath: lvs, LVCreatePath: lvcreate, LVRemovePath: lvremove}, nil
}

func (client *CLIClient) RemoveLogicalVolume(ctx context.Context, vgName, lvName string) error {
	if client == nil || !validLVMName(vgName) || !validLVMName(lvName) {
		return errors.New("invalid Local LVM delete request")
	}
	output, err := exec.CommandContext(ctx, client.LVRemovePath, "--yes", vgName+"/"+lvName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete Local LVM LV: %w: %s", err, boundedOutput(output))
	}
	return nil
}

func (client *CLIClient) VerifyVolumeGroup(ctx context.Context, vgName, expectedUUID string) error {
	if client == nil || !validLVMName(vgName) || expectedUUID == "" {
		return errors.New("invalid Local LVM VG identity")
	}
	output, err := exec.CommandContext(ctx, client.VGSPath, "--noheadings", "--readonly", "--separator", "|", "-o", "vg_uuid,vg_name", vgName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("read Local LVM VG identity: %w: %s", err, boundedOutput(output))
	}
	fields := splitRecord(string(output), 2)
	if len(fields) != 2 || fields[0] != expectedUUID || fields[1] != vgName {
		return errors.New("Local LVM VG identity mismatch")
	}
	return nil
}

func (client *CLIClient) LogicalVolume(ctx context.Context, vgName, lvName string) (LogicalVolume, bool, error) {
	if client == nil || !validLVMName(vgName) || !validLVMName(lvName) {
		return LogicalVolume{}, false, errors.New("invalid Local LVM resource identity")
	}
	selector := "vg_name=" + vgName + " && lv_name=" + lvName
	// Do not use LVM's --readonly reporting mode here: lv_device_open is then
	// reported as "unknown" and cannot prove holder presence or absence. lvs is
	// still a read-only query; it owns no activation or mutation argument.
	output, err := exec.CommandContext(ctx, client.LVSPath, "--noheadings", "--units", "b", "--nosuffix", "--separator", "|", "-o", "vg_uuid,lv_uuid,lv_name,lv_size,lv_device_open", "--select", selector).CombinedOutput()
	if err != nil {
		return LogicalVolume{}, false, fmt.Errorf("read Local LVM LV identity: %w: %s", err, boundedOutput(output))
	}
	if strings.TrimSpace(string(output)) == "" {
		return LogicalVolume{}, false, nil
	}
	fields := splitRecord(string(output), 5)
	if len(fields) != 5 {
		return LogicalVolume{}, false, errors.New("unexpected Local LVM read-back shape")
	}
	size, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return LogicalVolume{}, false, errors.New("invalid Local LVM read-back size")
	}
	if fields[4] != "" && fields[4] != "open" {
		return LogicalVolume{}, false, errors.New("invalid Local LVM open-holder read-back")
	}
	return LogicalVolume{VGUUID: fields[0], LVUUID: fields[1], Name: fields[2], SizeBytes: size, DeviceOpen: fields[4] == "open"}, true, nil
}

func (client *CLIClient) CreateLogicalVolume(ctx context.Context, vgName, lvName string, sizeMiB uint64) error {
	if client == nil || !validLVMName(vgName) || !validLVMName(lvName) || sizeMiB == 0 {
		return errors.New("invalid Local LVM create request")
	}
	output, err := exec.CommandContext(ctx, client.LVCreatePath, "--yes", "--size", strconv.FormatUint(sizeMiB, 10)+"M", "--name", lvName, vgName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create Local LVM LV: %w: %s", err, boundedOutput(output))
	}
	return nil
}

func splitRecord(output string, expected int) []string {
	scanner := bufio.NewScanner(strings.NewReader(output))
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
	parts := strings.Split(record, "|")
	if len(parts) != expected {
		return nil
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func boundedOutput(output []byte) string {
	const limit = 512
	if len(output) > limit {
		output = output[:limit]
	}
	return strings.TrimSpace(string(output))
}
