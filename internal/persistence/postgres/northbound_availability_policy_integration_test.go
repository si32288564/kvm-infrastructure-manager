package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/availabilitypolicy"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

func TestNorthboundAvailabilityPolicyHTTPPostgreSQLIntegration(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, url, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('northbound-availability',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	principals := map[string]project.Principal{"admin": {Issuer: "availability-issuer", Subject: "admin-" + suffix, Type: "HUMAN"}, "machine": {Issuer: "availability-issuer", Subject: "machine-" + suffix, Type: "AUTOMATION"}, "reader": {Issuer: "availability-issuer", Subject: "reader-" + suffix, Type: "HUMAN"}, "outsider": {Issuer: "availability-issuer", Subject: "outsider-" + suffix, Type: "HUMAN"}}
	for name, role := range map[string]string{"admin": "ADMIN", "machine": "WRITER", "reader": "READER"} {
		p := principals[name]
		if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','',$5,'ACTIVE',1)`, name+"-"+suffix, p.Issuer, p.Subject, p.Type, role); err != nil {
			t.Fatal(err)
		}
	}
	pstore := NorthboundProjectStore{DB: pool}
	astore := NorthboundAvailabilityPolicyStore{DB: pool}
	server := httptest.NewServer(httpapi.Server{Projects: project.Service{Store: pstore}, AvailabilityPolicies: availabilitypolicy.Service{Store: astore}, Authenticator: integrationPrincipalAuthenticator{subjects: principals}}.Handler())
	defer server.Close()
	client := server.Client()
	body := `{"name":"manual-safe","availabilityMode":"MANUAL","maxAttempts":3}`
	const workers = 6
	ids := make(chan string, workers)
	statuses := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := integrationRequest(client, "POST", server.URL+"/api/v1/availability-policies", body, map[string]string{"X-Test-Principal": "admin", "Idempotency-Key": "availability-" + suffix})
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			var x availabilitypolicy.Resource
			_ = json.NewDecoder(response.Body).Decode(&x)
			statuses <- response.StatusCode
			ids <- x.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(statuses)
	id := ""
	for status := range statuses {
		if status != 201 {
			t.Fatalf("create=%d", status)
		}
	}
	for candidate := range ids {
		if id == "" {
			id = candidate
		}
		if id != candidate {
			t.Fatalf("duplicate policy %s/%s", id, candidate)
		}
	}
	var revisions, idempotency int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.availability_policy_revision_evidence WHERE policy_id=$1`, id).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.northbound_availability_policy_idempotency_evidence WHERE policy_id=$1`, id).Scan(&idempotency); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 || idempotency != 1 {
		t.Fatalf("authority=%d/%d", revisions, idempotency)
	}
	response, _ := integrationRequest(client, "POST", server.URL+"/api/v1/availability-policies", `{"name":"different","availabilityMode":"MANUAL","maxAttempts":3}`, map[string]string{"X-Test-Principal": "admin", "Idempotency-Key": "availability-" + suffix})
	assertIntegrationProblem(t, response, 409, "IDEMPOTENCY_CONFLICT")
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/availability-policies/"+id, "", map[string]string{"X-Test-Principal": "reader"})
	if response.StatusCode != 200 || response.Header.Get("ETag") != `"1"` {
		t.Fatalf("reader get=%d etag=%q", response.StatusCode, response.Header.Get("ETag"))
	}
	var got availabilitypolicy.Resource
	_ = json.NewDecoder(response.Body).Decode(&got)
	response.Body.Close()
	if got.Name != "manual-safe" || got.AvailabilityMode != "MANUAL" || got.MaxAttempts != 3 {
		t.Fatalf("projection=%+v", got)
	}
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/availability-policies/"+id, `{"name":"denied"}`, map[string]string{"X-Test-Principal": "reader", "If-Match": `"1"`})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/availability-policies/"+id, "", map[string]string{"X-Test-Principal": "machine", "If-Match": `"1"`})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/availability-policies/"+id, "", map[string]string{"X-Test-Principal": "outsider"})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	statuses = make(chan int, 2)
	wg = sync.WaitGroup{}
	for _, name := range []string{"policy-a", "policy-b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			response, err := integrationRequest(client, "PATCH", server.URL+"/api/v1/availability-policies/"+id, fmt.Sprintf(`{"name":%q}`, name), map[string]string{"X-Test-Principal": "machine", "If-Match": `"1"`})
			if err != nil {
				statuses <- 0
				return
			}
			response.Body.Close()
			statuses <- response.StatusCode
		}(name)
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatalf("patch race=%v", counts)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.availability_policy_revision_evidence WHERE policy_id=$1`, id).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("revisions=%d err=%v", revisions, err)
	}
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/availability-policies?limit=101", "", map[string]string{"X-Test-Principal": "reader"})
	assertIntegrationProblem(t, response, 400, "VALIDATION_FAILED")
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/availability-policies/"+id, `{"failureEpochId":"forged"}`, map[string]string{"X-Test-Principal": "machine", "If-Match": `"2"`})
	assertIntegrationProblem(t, response, 400, "VALIDATION_FAILED")
	var runtimeBefore, runtimeAfter int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence)+(SELECT count(*) FROM kim.recovery_operation_evidence)`).Scan(&runtimeBefore); err != nil {
		t.Fatal(err)
	}
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/availability-policies/"+id, `{"deleteProtection":true}`, map[string]string{"X-Test-Principal": "machine", "If-Match": `"2"`})
	if response.StatusCode != 200 {
		t.Fatalf("protect=%d", response.StatusCode)
	}
	response.Body.Close()
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence)+(SELECT count(*) FROM kim.recovery_operation_evidence)`).Scan(&runtimeAfter); err != nil || runtimeAfter != runtimeBefore {
		t.Fatalf("runtime changed %d/%d err=%v", runtimeBefore, runtimeAfter, err)
	}
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/availability-policies/"+id, "", map[string]string{"X-Test-Principal": "admin", "If-Match": `"3"`})
	assertIntegrationProblem(t, response, 409, "DELETE_PROTECTED")
	deleteBody := `{"name":"delete-safe","availabilityMode":"WORKLOAD_MANAGED","maxAttempts":1}`
	response, err = integrationRequest(client, "POST", server.URL+"/api/v1/availability-policies", deleteBody, map[string]string{"X-Test-Principal": "admin", "Idempotency-Key": "availability-delete-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	var deleted availabilitypolicy.Resource
	_ = json.NewDecoder(response.Body).Decode(&deleted)
	response.Body.Close()
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/availability-policies?limit=1", "", map[string]string{"X-Test-Principal": "reader"})
	var first struct {
		Items      []availabilitypolicy.Resource `json:"items"`
		NextCursor string                        `json:"nextCursor"`
	}
	_ = json.NewDecoder(response.Body).Decode(&first)
	response.Body.Close()
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("Availability first page=%+v", first)
	}
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/availability-policies?limit=1&cursor="+first.NextCursor, "", map[string]string{"X-Test-Principal": "reader"})
	var second struct {
		Items []availabilitypolicy.Resource `json:"items"`
	}
	_ = json.NewDecoder(response.Body).Decode(&second)
	response.Body.Close()
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("Availability second page=%+v", second)
	}
	for range 2 {
		response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/availability-policies/"+deleted.ID, "", map[string]string{"X-Test-Principal": "admin", "If-Match": `"1"`})
		if response.StatusCode != 204 {
			t.Fatalf("delete replay=%d", response.StatusCode)
		}
		response.Body.Close()
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.northbound_availability_policy_revision_metadata SET policy_name='mutated' WHERE policy_id=$1`, id); err == nil {
		t.Fatal("immutable metadata update succeeded")
	}
}
