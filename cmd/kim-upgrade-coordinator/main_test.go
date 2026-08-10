package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "kim-upgrade-coordinator") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunRejectsIncompleteOrUnboundedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "missing authority identity"},
		{name: "renewal not below lease", args: []string{
			"-database-url", "postgres://fixture", "-campaign-id", "campaign", "-coordinator-id", "coordinator",
			"-canary-evaluator-artifact-digest", strings.Repeat("a", 64), "-claim-lease", "1s",
			"-claim-maximum-lifetime", "2s", "-claim-renew-interval", "1s",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}
