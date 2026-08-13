package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

type integrationPrincipalAuthenticator struct {
	subjects map[string]project.Principal
}

func (a integrationPrincipalAuthenticator) Authenticate(request *http.Request) (project.Principal, error) {
	if principal, ok := a.subjects[request.Header.Get("X-Test-Principal")]; ok {
		return principal, nil
	}
	return project.Principal{}, fmt.Errorf("invalid fixture token")
}

func TestNorthboundProjectHTTPPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('northbound-project',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	principals := map[string]project.Principal{
		"human":    {Issuer: "qualification-issuer", Subject: "human-admin-" + suffix, Type: "HUMAN"},
		"machine":  {Issuer: "qualification-issuer", Subject: "terraform-machine-" + suffix, Type: "AUTOMATION"},
		"outsider": {Issuer: "qualification-issuer", Subject: "outsider-" + suffix, Type: "HUMAN"},
		"reader":   {Issuer: "qualification-issuer", Subject: "reader-" + suffix, Type: "HUMAN"},
		"writer":   {Issuer: "qualification-issuer", Subject: "writer-" + suffix, Type: "AUTOMATION"},
	}
	for name, principal := range principals {
		if name != "human" && name != "machine" {
			continue
		}
		bindingID := name + "-system-" + suffix
		if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,'qualification-issuer',$2,$3,'SYSTEM','','ADMIN','ACTIVE',1)
			ON CONFLICT(principal_issuer,principal_subject,scope_type,scope_id) DO UPDATE SET principal_type=EXCLUDED.principal_type,role='ADMIN',lifecycle_state='ACTIVE',binding_revision=kim.northbound_role_bindings_current.binding_revision+1,updated_at=statement_timestamp()`, bindingID, principal.Subject, principal.Type); err != nil {
			t.Fatal(err)
		}
	}
	store := NorthboundProjectStore{DB: pool}
	server := httptest.NewServer(httpapi.Server{Projects: project.Service{Store: store}, Authenticator: integrationPrincipalAuthenticator{subjects: principals}}.Handler())
	defer server.Close()
	client := server.Client()

	// Concurrent create replay and response-loss retry converge on one Project.
	const workers = 12
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := integrationRequest(client, "POST", server.URL+"/api/v1/projects", `{"name":"concurrent-`+suffix+`"}`, map[string]string{"X-Test-Principal": "human", "Idempotency-Key": "concurrent-" + suffix})
			if err != nil {
				errs <- err
				return
			}
			defer response.Body.Close()
			var value project.Resource
			decodeErr := json.NewDecoder(response.Body).Decode(&value)
			if response.StatusCode != 201 || decodeErr != nil {
				errs <- fmt.Errorf("create status=%d decode=%v", response.StatusCode, decodeErr)
				return
			}
			ids <- value.ID
			errs <- nil
		}()
	}
	wait.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	createdID := ""
	for id := range ids {
		if createdID == "" {
			createdID = id
		}
		if id != createdID {
			t.Fatalf("duplicate IDs %s/%s", createdID, id)
		}
	}
	var currentCount, idempotencyCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.projects_current WHERE project_id=$1`, createdID).Scan(&currentCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.northbound_idempotency_evidence WHERE idempotency_key=$1`, `concurrent-`+suffix).Scan(&idempotencyCount); err != nil {
		t.Fatal(err)
	}
	if currentCount != 1 || idempotencyCount != 1 {
		t.Fatalf("counts=%d/%d", currentCount, idempotencyCount)
	}

	response, err := integrationRequest(client, "POST", server.URL+"/api/v1/projects", `{"name":"different"}`, map[string]string{"X-Test-Principal": "human", "Idempotency-Key": "concurrent-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	assertIntegrationProblem(t, response, 409, "IDEMPOTENCY_CONFLICT")

	// One concurrent If-Match update wins and one fails closed.
	statuses := make(chan int, 2)
	wait = sync.WaitGroup{}
	for _, name := range []string{"winner-a", "winner-b"} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			response, err := integrationRequest(client, "PATCH", server.URL+"/api/v1/projects/"+createdID, `{"name":"`+name+`"}`, map[string]string{"X-Test-Principal": "human", "If-Match": `"1"`})
			if err != nil {
				statuses <- 0
				return
			}
			response.Body.Close()
			statuses <- response.StatusCode
		}(name)
	}
	wait.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatalf("update statuses=%v", counts)
	}

	// A machine principal can create and then be reduced to its auto-created Project scope.
	machine := createIntegrationProject(t, client, server.URL, "machine", "machine-"+suffix, "machine-key-"+suffix, false)
	if _, err := pool.Exec(ctx, `UPDATE kim.northbound_role_bindings_current SET lifecycle_state='REVOKED',binding_revision=binding_revision+1,updated_at=statement_timestamp() WHERE principal_subject=$1 AND scope_type='SYSTEM'`, principals["machine"].Subject); err != nil {
		t.Fatal(err)
	}
	response, err = integrationRequest(client, "GET", server.URL+"/api/v1/projects/"+createdID, "", map[string]string{"X-Test-Principal": "machine"})
	if err != nil {
		t.Fatal(err)
	}
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, err = integrationRequest(client, "GET", server.URL+"/api/v1/projects?limit=100", "", map[string]string{"X-Test-Principal": "machine"})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []project.Resource `json:"items"`
	}
	_ = json.NewDecoder(response.Body).Decode(&page)
	response.Body.Close()
	if len(page.Items) != 1 || page.Items[0].ID != machine.ID {
		t.Fatalf("scope list=%+v", page.Items)
	}
	response, err = integrationRequest(client, "GET", server.URL+"/api/v1/projects/"+machine.ID, "", map[string]string{"X-Test-Principal": "outsider"})
	if err != nil {
		t.Fatal(err)
	}
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")

	// Project-scoped READER and WRITER roles are deliberately distinct.
	for name, role := range map[string]string{"reader": "READER", "writer": "WRITER"} {
		principal := principals[name]
		if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'PROJECT',$5,$6,'ACTIVE',1)`, name+"-binding-"+suffix, principal.Issuer, principal.Subject, principal.Type, createdID, role); err != nil {
			t.Fatal(err)
		}
	}
	response, err = integrationRequest(client, "GET", server.URL+"/api/v1/projects/"+createdID, "", map[string]string{"X-Test-Principal": "reader"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("reader GET=%d err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/projects/"+createdID, `{"name":"reader-denied"}`, map[string]string{"X-Test-Principal": "reader", "If-Match": `"2"`})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, err = integrationRequest(client, "PATCH", server.URL+"/api/v1/projects/"+createdID, `{"name":"writer-allowed"}`, map[string]string{"X-Test-Principal": "writer", "If-Match": `"2"`})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("writer PATCH=%d err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/projects/"+createdID, "", map[string]string{"X-Test-Principal": "writer", "If-Match": `"3"`})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")

	// Protection, dependency, delete replay, and tombstone behavior.
	protected := createIntegrationProject(t, client, server.URL, "human", "protected-"+suffix, "protected-key-"+suffix, true)
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/projects/"+protected.ID, "", map[string]string{"X-Test-Principal": "human", "If-Match": `"1"`})
	assertIntegrationProblem(t, response, 409, "DELETE_PROTECTED")
	dependent := createIntegrationProject(t, client, server.URL, "human", "dependent-"+suffix, "dependent-key-"+suffix, false)
	if _, err := pool.Exec(ctx, `INSERT INTO kim.networks_current(network_id,project_id,network_generation,lifecycle_state,mtu) VALUES($1,$2,1,'ACTIVE',1500)`, `dependent-network-`+suffix, dependent.ID); err != nil {
		t.Fatal(err)
	}
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/projects/"+dependent.ID, "", map[string]string{"X-Test-Principal": "human", "If-Match": `"1"`})
	assertIntegrationProblem(t, response, 409, "DEPENDENCY_CONFLICT")
	deletable := createIntegrationProject(t, client, server.URL, "human", "deletable-"+suffix, "deletable-key-"+suffix, false)
	for range 2 {
		response, err = integrationRequest(client, "DELETE", server.URL+"/api/v1/projects/"+deletable.ID, "", map[string]string{"X-Test-Principal": "human", "If-Match": `"1"`})
		if err != nil || response.StatusCode != 204 {
			t.Fatalf("delete/replay=%d err=%v", response.StatusCode, err)
		}
		response.Body.Close()
	}
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/projects/"+deletable.ID, "", map[string]string{"X-Test-Principal": "human"})
	assertIntegrationProblem(t, response, 404, "RESOURCE_NOT_FOUND")

	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.northbound_audit_evidence WHERE principal_issuer='qualification-issuer' AND principal_subject=$1 AND request_id<>''`, principals["human"].Subject).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount < 8 {
		t.Fatalf("audit count=%d", auditCount)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.project_revision_evidence SET project_name='forged' WHERE project_id=$1`, createdID); err == nil {
		t.Fatal("Project evidence update succeeded")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.northbound_idempotency_evidence SET response_status=202 WHERE idempotency_key=$1`, "concurrent-"+suffix); err == nil {
		t.Fatal("idempotency evidence update succeeded")
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.northbound_audit_evidence SET reason_code='FORGED' WHERE principal_subject=$1`, principals["human"].Subject); err == nil {
		t.Fatal("audit evidence update succeeded")
	}
}

func createIntegrationProject(t *testing.T, client *http.Client, base, principalName, name, key string, protection bool) project.Resource {
	t.Helper()
	response, err := integrationRequest(client, "POST", base+"/api/v1/projects", fmt.Sprintf(`{"name":%q,"deleteProtection":%t}`, name, protection), map[string]string{"X-Test-Principal": principalName, "Idempotency-Key": key})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value project.Resource
	if response.StatusCode != 201 || json.NewDecoder(response.Body).Decode(&value) != nil {
		t.Fatalf("create %s status=%d", name, response.StatusCode)
	}
	return value
}
func integrationRequest(client *http.Client, method, url, body string, headers map[string]string) (*http.Response, error) {
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	return client.Do(request)
}
func assertIntegrationProblem(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	defer response.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(response.Body).Decode(&body)
	if response.StatusCode != status || body["code"] != code || body["requestId"] == "" {
		t.Fatalf("problem status/body=%d/%v", response.StatusCode, body)
	}
}
