package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsDatabasePoolBelowWorkerAdmissionBound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := run([]string{
		"-database-url", "postgres://fixture.invalid/kim",
		"-worker-id", "worker-fixture",
		"-adapter-artifact-digest", strings.Repeat("a", 64),
		"-ovn-nb-db", "unix:/fixture/nb.sock",
		"-ovn-sb-db", "unix:/fixture/sb.sock",
		"-batch-limit", "16",
		"-database-max-connections", "16",
	}, &stdout, &stderr)
	if status != 2 || !strings.Contains(stderr.String(), "at least twice batch-limit") {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
}
