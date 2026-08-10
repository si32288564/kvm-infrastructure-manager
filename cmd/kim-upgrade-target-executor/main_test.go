package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "kim-upgrade-target-executor") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestConfigurationRequired(t *testing.T) {
	t.Setenv("KIM_DATABASE_URL", "")
	t.Setenv("KIM_UPGRADE_CAMPAIGN_ID", "")
	t.Setenv("KIM_UPGRADE_TARGET_ID", "")
	t.Setenv("KIM_UPGRADE_TARGET_EXECUTOR_ID", "")
	t.Setenv("KIM_UPGRADE_TARGET_STATE_DIRECTORY", "")
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
