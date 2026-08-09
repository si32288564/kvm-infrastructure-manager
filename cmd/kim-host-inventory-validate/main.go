package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory/linuxhost"
)

func main() {
	root := flag.String("root", "/", "Linux procfs/sysfs root")
	hostID := flag.String("host-id", "linux-validation-host", "validation Host identity")
	full := flag.Bool("full", false, "emit the complete canonical snapshot")
	flag.Parse()
	if runtime.GOOS != "linux" {
		fatal(fmt.Errorf("Linux Host validation requires a Linux runtime, got %s", runtime.GOOS))
	}
	adapter := linuxhost.Adapter{FS: os.DirFS(*root), Architecture: runtime.GOARCH}
	artifactDigest, err := executableDigest()
	if err != nil {
		fatal(err)
	}
	registry := inventory.NewRegistry()
	for _, module := range []inventory.Module{linuxhost.ComputeModule{Adapter: adapter, ArtifactDigest: artifactDigest}, linuxhost.MemoryModule{Adapter: adapter, ArtifactDigest: artifactDigest}, linuxhost.PCIModule{Adapter: adapter, ArtifactDigest: artifactDigest}} {
		if err := registry.Register(module); err != nil {
			fatal(err)
		}
	}
	snapshot, err := registry.Collect(context.Background(), *hostID, 1)
	if err != nil {
		fatal(err)
	}
	if *full {
		payload, err := snapshot.MarshalCanonical()
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(payload)
		os.Stdout.Write([]byte("\n"))
		return
	}
	summary := struct {
		HostIdentity     string                 `json:"host_identity"`
		CollectionStatus string                 `json:"collection_status"`
		Capabilities     []inventory.Capability `json:"capabilities"`
		CPUThreads       int                    `json:"cpu_threads"`
		NUMANodes        int                    `json:"numa_nodes"`
		HugePagePools    int                    `json:"hugepage_pools"`
		PCIDevices       int                    `json:"pci_devices"`
		SRIOVPFs         int                    `json:"sriov_pfs"`
		SRIOVVFs         int                    `json:"sriov_vfs"`
		TotalMemoryBytes uint64                 `json:"total_memory_bytes"`
	}{HostIdentity: snapshot.HostIdentity, CollectionStatus: snapshot.CollectionStatus, Capabilities: snapshot.Capabilities}
	for _, fragment := range snapshot.Fragments {
		if fragment.Compute != nil {
			summary.CPUThreads = len(fragment.Compute.Threads)
		}
		if fragment.Memory != nil {
			summary.NUMANodes = len(fragment.Memory.NUMANodes)
			summary.HugePagePools = len(fragment.Memory.HugePagePools)
			summary.TotalMemoryBytes = fragment.Memory.TotalBytes
		}
		if fragment.PCI != nil {
			summary.PCIDevices = len(fragment.PCI.Devices)
			for _, device := range fragment.PCI.Devices {
				if device.SRIOVTotalVFs > 0 {
					summary.SRIOVPFs++
				}
				if device.PFAddress != "" {
					summary.SRIOVVFs++
				}
			}
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func executableDigest() (string, error) {
	name, err := os.Executable()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
