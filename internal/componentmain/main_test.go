package componentmain

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run("kim-api", []string{"-version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("Run returned %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "kim-api development") {
		t.Fatalf("unexpected version output %q", stdout.String())
	}
}
