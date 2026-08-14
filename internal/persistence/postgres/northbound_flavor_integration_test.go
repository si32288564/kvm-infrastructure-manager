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

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/flavor"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

func TestNorthboundFlavorHTTPPostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('northbound-flavor',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	principals := map[string]project.Principal{"admin": {Issuer: "flavor-issuer", Subject: "admin-" + suffix, Type: "HUMAN"}, "machine": {Issuer: "flavor-issuer", Subject: "machine-" + suffix, Type: "AUTOMATION"}, "reader": {Issuer: "flavor-issuer", Subject: "reader-" + suffix, Type: "HUMAN"}, "outsider": {Issuer: "flavor-issuer", Subject: "outsider-" + suffix, Type: "HUMAN"}}
	for _, name := range []string{"admin", "machine"} {
		p := principals[name]
		if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','','ADMIN','ACTIVE',1)`, name+"-"+suffix, p.Issuer, p.Subject, p.Type); err != nil {
			t.Fatal(err)
		}
	}
	pstore := NorthboundProjectStore{DB: pool}
	fstore := NorthboundFlavorStore{DB: pool}
	server := httptest.NewServer(httpapi.Server{Projects: project.Service{Store: pstore}, Flavors: flavor.Service{Store: fstore}, Authenticator: integrationPrincipalAuthenticator{subjects: principals}}.Handler())
	defer server.Close()
	client := server.Client()
	projectA := createIntegrationProject(t, client, server.URL, "admin", "flavor-project-a-"+suffix, "fp-a-"+suffix, false)
	projectB := createIntegrationProject(t, client, server.URL, "admin", "flavor-project-b-"+suffix, "fp-b-"+suffix, false)
	body := fmt.Sprintf(`{"projectId":%q,"name":"general.small","vcpus":2,"memoryMiB":2048,"rootDiskGiB":20,"numaPolicy":"NONE","cpuAllocation":"SHARED","cpuPinning":false}`, projectA.ID)
	const workers = 8
	ids := make(chan string, workers)
	statuses := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := integrationRequest(client, "POST", server.URL+"/api/v1/flavors", body, map[string]string{"X-Test-Principal": "machine", "Idempotency-Key": "flavor-create-" + suffix})
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			var x flavor.Resource
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
			t.Fatalf("create status=%d", status)
		}
	}
	for candidate := range ids {
		if id == "" {
			id = candidate
		}
		if id != candidate {
			t.Fatalf("duplicate Flavor %s/%s", id, candidate)
		}
	}
	var current, idem int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.flavors_current WHERE flavor_id=$1`, id).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.northbound_flavor_idempotency_evidence WHERE idempotency_key=$1`, `flavor-create-`+suffix).Scan(&idem); err != nil {
		t.Fatal(err)
	}
	if current != 1 || idem != 1 {
		t.Fatalf("authority counts=%d/%d", current, idem)
	}
	response, err := integrationRequest(client, "POST", server.URL+"/api/v1/flavors", body, map[string]string{"X-Test-Principal": "machine", "Idempotency-Key": "flavor-create-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	var replayed flavor.Resource
	_ = json.NewDecoder(response.Body).Decode(&replayed)
	response.Body.Close()
	if response.StatusCode != 201 || response.Header.Get("ETag") != `"1"` || replayed.ID != id || replayed.ProjectID != projectA.ID || replayed.Name != "general.small" || replayed.VCPUs != 2 || replayed.MemoryMiB != 2048 || replayed.RootDiskGiB != 20 || replayed.Revision != 1 {
		t.Fatalf("Flavor replay projection status=%d etag=%q resource=%+v", response.StatusCode, response.Header.Get("ETag"), replayed)
	}
	response, _ = integrationRequest(client, "POST", server.URL+"/api/v1/flavors", fmt.Sprintf(`{"projectId":%q,"name":"different","vcpus":2,"memoryMiB":2048,"rootDiskGiB":20,"numaPolicy":"NONE","cpuAllocation":"SHARED","cpuPinning":false}`, projectA.ID), map[string]string{"X-Test-Principal": "machine", "Idempotency-Key": "flavor-create-" + suffix})
	assertIntegrationProblem(t, response, 409, "IDEMPOTENCY_CONFLICT")
	if _, err := pool.Exec(ctx, `UPDATE kim.northbound_role_bindings_current SET lifecycle_state='REVOKED',binding_revision=binding_revision+1 WHERE principal_subject=$1 AND scope_type='SYSTEM'`, principals["machine"].Subject); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'PROJECT',$5,'WRITER','ACTIVE',1)`, `machine-project-`+suffix, principals["machine"].Issuer, principals["machine"].Subject, principals["machine"].Type, projectA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'PROJECT',$5,'READER','ACTIVE',1)`, `reader-project-`+suffix, principals["reader"].Issuer, principals["reader"].Subject, principals["reader"].Type, projectA.ID); err != nil {
		t.Fatal(err)
	}
	response, err = integrationRequest(client, "GET", server.URL+"/api/v1/flavors/"+id, "", map[string]string{"X-Test-Principal": "reader"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("reader GET=%d err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/flavors/"+id, `{"name":"reader-denied"}`, map[string]string{"X-Test-Principal": "reader", "If-Match": `"1"`})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/flavors/"+id, "", map[string]string{"X-Test-Principal": "machine", "If-Match": `"1"`})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/flavors/"+id, "", map[string]string{"X-Test-Principal": "outsider"})
	assertIntegrationProblem(t, response, 403, "FORBIDDEN")
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/flavors?projectId="+projectB.ID, "", map[string]string{"X-Test-Principal": "machine"})
	var page struct {
		Items []flavor.Resource `json:"items"`
	}
	_ = json.NewDecoder(response.Body).Decode(&page)
	response.Body.Close()
	if len(page.Items) != 0 {
		t.Fatalf("cross-project list leaked=%+v", page.Items)
	}
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/flavors?limit=101", "", map[string]string{"X-Test-Principal": "machine"})
	assertIntegrationProblem(t, response, 400, "VALIDATION_FAILED")
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/flavors/"+id, `{"name":"missing-precondition"}`, map[string]string{"X-Test-Principal": "machine"})
	assertIntegrationProblem(t, response, 400, "VALIDATION_FAILED")
	statuses = make(chan int, 2)
	wg = sync.WaitGroup{}
	for _, name := range []string{"small-a", "small-b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			response, err := integrationRequest(client, "PATCH", server.URL+"/api/v1/flavors/"+id, fmt.Sprintf(`{"name":%q}`, name), map[string]string{"X-Test-Principal": "machine", "If-Match": `"1"`})
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
	var historicalName string
	var revisionCount int
	if err := pool.QueryRow(ctx, `SELECT name FROM kim.flavor_revision_evidence WHERE flavor_id=$1 AND flavor_revision=1`, id).Scan(&historicalName); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.flavor_revision_evidence WHERE flavor_id=$1`, id).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if historicalName != "general.small" || revisionCount != 2 {
		t.Fatalf("historical Flavor mutated name=%q revisions=%d", historicalName, revisionCount)
	}
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/flavors/"+id, `{"projectId":"00000000-0000-4000-8000-000000000000"}`, map[string]string{"X-Test-Principal": "machine", "If-Match": `"2"`})
	assertIntegrationProblem(t, response, 400, "VALIDATION_FAILED")
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/flavors/"+id, `{"name":null}`, map[string]string{"X-Test-Principal": "machine", "If-Match": `"2"`})
	assertIntegrationProblem(t, response, 400, "VALIDATION_FAILED")
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/flavors/00000000-0000-4000-8000-000000000000", "", map[string]string{"X-Test-Principal": "admin"})
	assertIntegrationProblem(t, response, 404, "RESOURCE_NOT_FOUND")
	response, _ = integrationRequest(client, "PATCH", server.URL+"/api/v1/flavors/"+id, `{"deleteProtection":true}`, map[string]string{"X-Test-Principal": "machine", "If-Match": `"2"`})
	if response.StatusCode != 200 {
		t.Fatalf("protect=%d", response.StatusCode)
	}
	response.Body.Close()
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/flavors/"+id, "", map[string]string{"X-Test-Principal": "admin", "If-Match": `"2"`})
	assertIntegrationProblem(t, response, 412, "STALE_REVISION")
	response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/flavors/"+id, "", map[string]string{"X-Test-Principal": "admin", "If-Match": `"3"`})
	assertIntegrationProblem(t, response, 409, "DELETE_PROTECTED")
	deletableBody := fmt.Sprintf(`{"projectId":%q,"name":"delete-me","vcpus":1,"memoryMiB":512,"rootDiskGiB":1,"numaPolicy":"NONE","cpuAllocation":"SHARED","cpuPinning":false}`, projectA.ID)
	response, err = integrationRequest(client, "POST", server.URL+"/api/v1/flavors", deletableBody, map[string]string{"X-Test-Principal": "admin", "Idempotency-Key": "delete-flavor-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	var deletable flavor.Resource
	_ = json.NewDecoder(response.Body).Decode(&deletable)
	response.Body.Close()
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/flavors?projectId="+projectA.ID+"&limit=1", "", map[string]string{"X-Test-Principal": "admin"})
	var first struct {
		Items      []flavor.Resource `json:"items"`
		NextCursor string            `json:"nextCursor"`
	}
	_ = json.NewDecoder(response.Body).Decode(&first)
	response.Body.Close()
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("Flavor first page=%+v", first)
	}
	response, _ = integrationRequest(client, "GET", server.URL+"/api/v1/flavors?projectId="+projectA.ID+"&limit=1&cursor="+first.NextCursor, "", map[string]string{"X-Test-Principal": "admin"})
	var second struct {
		Items []flavor.Resource `json:"items"`
	}
	_ = json.NewDecoder(response.Body).Decode(&second)
	response.Body.Close()
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("Flavor second page=%+v", second)
	}
	for range 2 {
		response, _ = integrationRequest(client, "DELETE", server.URL+"/api/v1/flavors/"+deletable.ID, "", map[string]string{"X-Test-Principal": "admin", "If-Match": `"1"`})
		if response.StatusCode != 204 {
			t.Fatalf("delete replay=%d", response.StatusCode)
		}
		response.Body.Close()
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.northbound_flavor_idempotency_evidence SET response_status=202 WHERE flavor_id=$1`, id); err == nil {
		t.Fatal("Flavor idempotency evidence update succeeded")
	}
}
