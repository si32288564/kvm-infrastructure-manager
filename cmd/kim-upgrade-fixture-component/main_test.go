package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHealth(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("Linux procfs is required")
	}
	path := filepath.Join(t.TempDir(), "run", "health.json")
	if err := writeHealth(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"ready":true`) {
		t.Fatalf("health=%s", raw)
	}
}
