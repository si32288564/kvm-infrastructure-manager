package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
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

func TestMetricsHandlerPublishesBoundedIdentityFreeWorkerMetrics(t *testing.T) {
	recorder := httptest.NewRecorder()
	newMetricsHandler(ovnruntime.NewMetrics(), nil).ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, required := range []string{
		`kim_ovn_worker_state{state="STARTING"} 1`,
		"kim_ovn_worker_claims_total 0",
		"kim_ovn_worker_in_flight 0",
		"kim_ovn_worker_renewals_total 0",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("missing %q in metrics:\n%s", required, body)
		}
	}
	if strings.Contains(body, "worker-fixture") || strings.Contains(body, "work_id") || strings.Contains(body, "port_id") {
		t.Fatalf("high-cardinality identity leaked in metrics:\n%s", body)
	}
}
