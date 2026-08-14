package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: server.URL, Token: "secret-token", Timeout: 2 * time.Second, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProblemDetailsUsesStableCodeAndRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatal("missing bearer token")
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_ = json.NewEncoder(w).Encode(Problem{Status: 412, Code: "STALE_REVISION", Detail: "refresh required", RequestID: "request-42"})
	}))
	defer server.Close()
	_, err := testClient(t, server).Do(context.Background(), http.MethodPatch, "/projects/id", map[string]any{"name": "new"}, nil, map[string]string{"If-Match": `"1"`})
	p, ok := err.(*Problem)
	if !ok || p.Code != "STALE_REVISION" || p.RequestID != "request-42" || p.Status != 412 {
		t.Fatalf("problem=%+v err=%v", p, err)
	}
}

func TestIdempotentPostRetriesResponseLossWithSameKey(t *testing.T) {
	var requests atomic.Int64
	var firstKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if requests.Add(1) == 1 {
			firstKey = key
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server cannot simulate response loss")
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			return
		}
		if key != firstKey || key == "" {
			t.Fatalf("idempotency key changed %q/%q", firstKey, key)
		}
		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"one"}`))
	}))
	defer server.Close()
	var out struct {
		ID string `json:"id"`
	}
	response, err := testClient(t, server).Do(context.Background(), http.MethodPost, "/projects", map[string]any{"name": "one"}, &out, map[string]string{"Idempotency-Key": "stable-attempt"})
	if err != nil || requests.Load() != 2 || out.ID != "one" || response.ETag != `"1"` {
		t.Fatalf("response=%+v out=%+v requests=%d err=%v", response, out, requests.Load(), err)
	}
}

func TestOperationUnknownIsNonTerminal(t *testing.T) {
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		phase := "UNKNOWN"
		if reads.Add(1) >= 2 {
			phase = "SUCCEEDED"
		}
		_ = json.NewEncoder(w).Encode(Operation{ID: "operation-1", Phase: phase})
	}))
	defer server.Close()
	op, err := testClient(t, server).WaitOperation(context.Background(), "operation-1")
	if err != nil || op.Phase != "SUCCEEDED" || reads.Load() != 2 {
		t.Fatalf("operation=%+v reads=%d err=%v", op, reads.Load(), err)
	}
}

func TestTypedPaginationEncodesCursor(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "opaque+/cursor" || r.URL.Query().Get("limit") != "25" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"one"}],"nextCursor":"next"}`))
	}))
	defer server.Close()
	page, err := ListPage[item](context.Background(), testClient(t, server), "/projects", "opaque+/cursor", 25)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "one" || page.NextCursor != "next" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestIdempotencyKeyIncludesDesiredPayload(t *testing.T) {
	a, err := IdempotencyKey("project", map[string]any{"name": "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := IdempotencyKey("project", map[string]any{"name": "b"})
	aReplay, _ := IdempotencyKey("project", map[string]any{"name": "a"})
	if a == b || a != aReplay {
		t.Fatalf("keys a=%s b=%s replay=%s", a, b, aReplay)
	}
}

func TestCreateIdempotencyKeyIsInvocationScoped(t *testing.T) {
	a, err := CreateIdempotencyKey("project", map[string]any{"name": "same"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateIdempotencyKey("project", map[string]any{"name": "same"})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("separate Create invocations reused a permanent idempotency key")
	}
}
