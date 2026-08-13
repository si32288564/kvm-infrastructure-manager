package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

type fixedAuthenticator struct {
	principal project.Principal
	err       error
}

type panicAuthenticator struct{}

func (panicAuthenticator) Authenticate(*http.Request) (project.Principal, error) {
	panic("credential must not leak")
}

func (a fixedAuthenticator) Authenticate(*http.Request) (project.Principal, error) {
	return a.principal, a.err
}

type memoryStore struct {
	mu     sync.Mutex
	items  map[string]project.Resource
	idem   map[string]project.Resource
	forced error
	audits []project.AuditEvent
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: map[string]project.Resource{}, idem: map[string]project.Resource{}}
}
func (s *memoryStore) Ready(context.Context) error { return s.forced }
func (s *memoryStore) RecordAudit(_ context.Context, event project.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, event)
	return nil
}
func (s *memoryStore) Create(_ context.Context, _ project.Principal, request project.CreateRequest, id, digest string) (project.Resource, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forced != nil {
		return project.Resource{}, false, s.forced
	}
	if existing, ok := s.idem[request.IdempotencyKey]; ok {
		existingDigest, _ := project.DesiredDigest(existing.Name, existing.DeleteProtection)
		if existingDigest != digest {
			return project.Resource{}, false, project.ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	resource := project.Resource{ID: id, Name: request.Name, DeleteProtection: request.DeleteProtection, Revision: 1, CreatedAt: now, UpdatedAt: now}
	s.items[id], s.idem[request.IdempotencyKey] = resource, resource
	return resource, false, nil
}
func (s *memoryStore) Get(_ context.Context, _ project.Principal, id, _ string) (project.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forced != nil {
		return project.Resource{}, s.forced
	}
	value, ok := s.items[id]
	if !ok {
		return value, project.ErrNotFound
	}
	return value, nil
}
func (s *memoryStore) List(_ context.Context, _ project.Principal, request project.ListRequest, _ string) (project.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := project.Page{}
	for id, item := range s.items {
		if id > request.AfterID {
			page.Items = append(page.Items, item)
		}
	}
	sort.Slice(page.Items, func(i, j int) bool { return page.Items[i].ID < page.Items[j].ID })
	if len(page.Items) > request.Limit {
		page.NextAfter = page.Items[request.Limit-1].ID
		page.Items = page.Items[:request.Limit]
	}
	return page, s.forced
}
func (s *memoryStore) Patch(_ context.Context, _ project.Principal, id string, revision uint64, patch project.Patch, _ string) (project.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return item, project.ErrNotFound
	}
	if revision != item.Revision {
		return item, project.ErrStaleRevision
	}
	if patch.NamePresent {
		item.Name = patch.Name
	}
	if patch.DeleteProtectionPresent {
		item.DeleteProtection = patch.DeleteProtection
	}
	item.Revision++
	s.items[id] = item
	return item, nil
}
func (s *memoryStore) Delete(_ context.Context, _ project.Principal, id string, revision uint64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return project.ErrNotFound
	}
	if revision != item.Revision {
		return project.ErrStaleRevision
	}
	if item.DeleteProtection {
		return project.ErrDeleteProtected
	}
	delete(s.items, id)
	return nil
}

func TestProjectHTTPContract(t *testing.T) {
	store := newMemoryStore()
	principal := project.Principal{Issuer: "issuer", Subject: "human", Type: "HUMAN"}
	server := httptest.NewServer(Server{Projects: project.Service{Store: store}, Authenticator: fixedAuthenticator{principal: principal}}.Handler())
	defer server.Close()
	client := server.Client()

	response := do(t, client, "GET", server.URL+"/healthz", "", nil, nil)
	if response.StatusCode != 200 || response.Header.Get(RequestIDHeader) == "" {
		t.Fatalf("health status/header=%d/%q", response.StatusCode, response.Header.Get(RequestIDHeader))
	}
	response.Body.Close()
	response = do(t, client, "GET", server.URL+"/readyz", "", nil, nil)
	if response.StatusCode != 200 {
		t.Fatalf("ready status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = do(t, client, "POST", server.URL+"/api/v1/projects", `{"name":"alpha","deleteProtection":false}`, map[string]string{"Idempotency-Key": "create-1"}, nil)
	if response.StatusCode != 201 || response.Header.Get("ETag") != `"1"` || response.Header.Get("Location") == "" {
		t.Fatalf("create status/etag/location=%d/%q/%q", response.StatusCode, response.Header.Get("ETag"), response.Header.Get("Location"))
	}
	var created project.Resource
	if json.NewDecoder(response.Body).Decode(&created) != nil || created.ID == "" || created.Name != "alpha" {
		t.Fatalf("created=%+v", created)
	}
	response.Body.Close()

	response = do(t, client, "POST", server.URL+"/api/v1/projects", `{"name":"alpha","deleteProtection":false}`, map[string]string{"Idempotency-Key": "create-1"}, nil)
	var replay project.Resource
	_ = json.NewDecoder(response.Body).Decode(&replay)
	response.Body.Close()
	if response.StatusCode != 201 || replay.ID != created.ID {
		t.Fatalf("replay=%d %+v", response.StatusCode, replay)
	}
	response = do(t, client, "POST", server.URL+"/api/v1/projects", `{"name":"different"}`, map[string]string{"Idempotency-Key": "create-1"}, nil)
	assertProblem(t, response, 409, "IDEMPOTENCY_CONFLICT")
	response = do(t, client, "POST", server.URL+"/api/v1/projects", `{"name":"second"}`, map[string]string{"Idempotency-Key": "create-2"}, nil)
	response.Body.Close()
	response = do(t, client, "GET", server.URL+"/api/v1/projects?limit=1", "", nil, nil)
	var firstPage struct {
		Items      []project.Resource `json:"items"`
		NextCursor string             `json:"nextCursor"`
	}
	_ = json.NewDecoder(response.Body).Decode(&firstPage)
	response.Body.Close()
	if response.StatusCode != 200 || len(firstPage.Items) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first page=%d %+v", response.StatusCode, firstPage)
	}
	response = do(t, client, "GET", server.URL+"/api/v1/projects?limit=1&cursor="+firstPage.NextCursor, "", nil, nil)
	var secondPage struct {
		Items []project.Resource `json:"items"`
	}
	_ = json.NewDecoder(response.Body).Decode(&secondPage)
	response.Body.Close()
	if len(secondPage.Items) != 1 || secondPage.Items[0].ID == firstPage.Items[0].ID {
		t.Fatalf("second page=%+v", secondPage)
	}

	response = do(t, client, "GET", server.URL+"/api/v1/projects/"+created.ID, "", nil, nil)
	if response.StatusCode != 200 || response.Header.Get("ETag") != `"1"` {
		t.Fatalf("get=%d %q", response.StatusCode, response.Header.Get("ETag"))
	}
	response.Body.Close()
	response = do(t, client, "PATCH", server.URL+"/api/v1/projects/"+created.ID, `{"name":"beta"}`, map[string]string{"If-Match": `"1"`}, nil)
	if response.StatusCode != 200 || response.Header.Get("ETag") != `"2"` {
		t.Fatalf("patch=%d %q", response.StatusCode, response.Header.Get("ETag"))
	}
	response.Body.Close()
	response = do(t, client, "PATCH", server.URL+"/api/v1/projects/"+created.ID, `{"name":"stale"}`, map[string]string{"If-Match": `"1"`}, nil)
	assertProblem(t, response, 412, "STALE_REVISION")
	response = do(t, client, "PATCH", server.URL+"/api/v1/projects/"+created.ID, `{"name":null}`, map[string]string{"If-Match": `"2"`}, nil)
	assertProblem(t, response, 400, "VALIDATION_FAILED")
	response = do(t, client, "PATCH", server.URL+"/api/v1/projects/"+created.ID, `{"id":"other"}`, map[string]string{"If-Match": `"2"`}, nil)
	assertProblem(t, response, 400, "VALIDATION_FAILED")
	response = do(t, client, "DELETE", server.URL+"/api/v1/projects/"+created.ID, "", map[string]string{"If-Match": `"1"`}, nil)
	assertProblem(t, response, 412, "STALE_REVISION")
	response = do(t, client, "DELETE", server.URL+"/api/v1/projects/"+created.ID, "", map[string]string{"If-Match": `"2"`}, nil)
	if response.StatusCode != 204 {
		t.Fatalf("delete=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestProjectHTTPFailuresBoundariesAndProblemDetails(t *testing.T) {
	store := newMemoryStore()
	valid := fixedAuthenticator{principal: project.Principal{Issuer: "issuer", Subject: "machine", Type: "AUTOMATION"}}
	tests := []struct {
		name, method, path, body string
		headers                  map[string]string
		authenticator            fixedAuthenticator
		status                   int
		code                     string
	}{
		{name: "no token", method: "GET", path: "/api/v1/projects", authenticator: fixedAuthenticator{err: errors.New("invalid")}, status: 401, code: "UNAUTHENTICATED"},
		{name: "malformed", method: "POST", path: "/api/v1/projects", body: `{`, headers: map[string]string{"Idempotency-Key": "x"}, authenticator: valid, status: 400, code: "VALIDATION_FAILED"},
		{name: "unknown field", method: "POST", path: "/api/v1/projects", body: `{"name":"x","host_id":"forbidden"}`, headers: map[string]string{"Idempotency-Key": "x"}, authenticator: valid, status: 400, code: "VALIDATION_FAILED"},
		{name: "missing if match", method: "PATCH", path: "/api/v1/projects/00000000-0000-4000-8000-000000000000", body: `{"name":"x"}`, authenticator: valid, status: 400, code: "VALIDATION_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			for k, v := range test.headers {
				request.Header.Set(k, v)
			}
			Server{Projects: project.Service{Store: store}, Authenticator: test.authenticator}.Handler().ServeHTTP(recorder, request)
			assertRecorderProblem(t, recorder, test.status, test.code)
		})
	}

	large := `{"name":"` + strings.Repeat("a", maximumBody) + `"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(large))
	request.Header.Set("Idempotency-Key", "large")
	Server{Projects: project.Service{Store: store}, Authenticator: valid}.Handler().ServeHTTP(recorder, request)
	assertRecorderProblem(t, recorder, 413, "REQUEST_TOO_LARGE")

	store.forced = errors.New("SQL SELECT secret_password")
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest("GET", "/api/v1/projects", nil)
	Server{Projects: project.Service{Store: store}, Authenticator: valid}.Handler().ServeHTTP(recorder, request)
	assertRecorderProblem(t, recorder, 500, "INTERNAL_ERROR")
	if strings.Contains(recorder.Body.String(), "SQL") || strings.Contains(recorder.Body.String(), "secret_password") {
		t.Fatal("internal error leaked")
	}
}

func TestHTTPServerGracefulShutdown(t *testing.T) {
	store := newMemoryStore()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: Server{Projects: project.Service{Store: store}, Authenticator: fixedAuthenticator{principal: project.Principal{Issuer: "issuer", Subject: "human", Type: "HUMAN"}}}.Handler()}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	response, err := http.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve=%v", err)
	}
}

func TestHTTPServerPanicRecoveryHasCorrelationID(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/projects", nil)
	Server{Projects: project.Service{Store: newMemoryStore()}, Authenticator: panicAuthenticator{}, Logger: io.Discard}.Handler().ServeHTTP(recorder, request)
	assertRecorderProblem(t, recorder, 500, "INTERNAL_ERROR")
	if strings.Contains(recorder.Body.String(), "credential") {
		t.Fatal("panic detail leaked")
	}
}

func do(t *testing.T, client *http.Client, method, url, body string, headers map[string]string, raw io.Reader) *http.Response {
	t.Helper()
	if raw == nil {
		raw = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, url, raw)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func assertProblem(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != status || !bytes.Contains(raw, []byte(`"code":"`+code+`"`)) || response.Header.Get(RequestIDHeader) == "" {
		t.Fatalf("problem=%d %s headers=%v", response.StatusCode, raw, response.Header)
	}
}
func assertRecorderProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	response := recorder.Result()
	assertProblem(t, response, status, code)
}
