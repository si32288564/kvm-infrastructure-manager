package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var version = "development"

type health struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	Ready         bool   `json:"ready"`
	PID           int    `json:"pid"`
	BootID        string `json:"boot_id"`
	StartTicks    uint64 `json:"process_start_ticks"`
}

func main() {
	set := flag.NewFlagSet("kim-upgrade-fixture-component", flag.ExitOnError)
	showVersion := set.Bool("version", false, "print version")
	healthPath := set.String("health-file", "", "administrator-configured health evidence path")
	_ = set.Parse(os.Args[1:])
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *healthPath == "" || !filepath.IsAbs(*healthPath) {
		fmt.Fprintln(os.Stderr, "absolute health evidence path is required")
		os.Exit(2)
	}
	if err := writeHealth(*healthPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

func writeHealth(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	bootIDRaw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	startTicks, err := processStartTicks("/proc/self/stat")
	if err != nil {
		return err
	}
	raw, err := json.Marshal(health{SchemaVersion: "kim.upgrade.fixture-health/v1", Version: version, Ready: true,
		PID: os.Getpid(), BootID: strings.TrimSpace(string(bootIDRaw)), StartTicks: startTicks})
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kim-health-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func processStartTicks(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	closing := strings.LastIndexByte(string(raw), ')')
	if closing < 0 {
		return 0, fmt.Errorf("invalid proc stat")
	}
	fields := strings.Fields(string(raw[closing+1:]))
	if len(fields) <= 19 {
		return 0, fmt.Errorf("incomplete proc stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}
