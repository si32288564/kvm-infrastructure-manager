// Package httpapi exposes versioned Northbound resources without exposing
// persistence, Agent, or backend interfaces.
package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/auth"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

const (
	RequestIDHeader = "X-Request-ID"
	maximumBody     = 64 << 10
)

var errBodyTooLarge = errors.New("request body exceeds the maximum size")

type Server struct {
	Projects       project.Service
	Authenticator  auth.Authenticator
	Logger         io.Writer
	RequestTimeout time.Duration
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	RequestID string `json:"requestId"`
	Retryable bool   `json:"retryable"`
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /api/v1/projects", s.createProject)
	mux.HandleFunc("GET /api/v1/projects", s.listProjects)
	mux.HandleFunc("GET /api/v1/projects/{project_id}", s.getProject)
	mux.HandleFunc("PATCH /api/v1/projects/{project_id}", s.patchProject)
	mux.HandleFunc("DELETE /api/v1/projects/{project_id}", s.deleteProject)
	return s.requestContext(s.recover(mux))
}

func (s Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (s Server) ready(w http.ResponseWriter, request *http.Request) {
	if s.Projects.Store == nil {
		s.writeProblem(w, request, project.ErrServiceUnavailable)
		return
	}
	if err := s.Projects.Store.Ready(request.Context()); err != nil {
		s.writeProblem(w, request, project.ErrServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s Server) createProject(w http.ResponseWriter, request *http.Request) {
	principal, ok := s.authenticate(w, request, "PROJECT_CREATE")
	if !ok {
		return
	}
	key := request.Header.Get("Idempotency-Key")
	var body struct {
		Name             string `json:"name"`
		DeleteProtection bool   `json:"deleteProtection"`
	}
	if err := decodeJSON(request, &body); err != nil {
		if !errors.Is(err, errBodyTooLarge) {
			err = project.ErrValidation
		}
		s.writeProblem(w, request, err)
		return
	}
	resource, _, err := s.Projects.Create(request.Context(), principal, project.CreateRequest{Name: body.Name, DeleteProtection: body.DeleteProtection, IdempotencyKey: key, RequestID: requestID(request), CanonicalPath: "/api/v1/projects"})
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	w.Header().Set("ETag", etag(resource.Revision))
	w.Header().Set("Location", "/api/v1/projects/"+resource.ID)
	writeJSON(w, http.StatusCreated, resource)
}

func (s Server) getProject(w http.ResponseWriter, request *http.Request) {
	principal, ok := s.authenticate(w, request, "PROJECT_READ")
	if !ok {
		return
	}
	resource, err := s.Projects.Get(request.Context(), principal, request.PathValue("project_id"), requestID(request))
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	w.Header().Set("ETag", etag(resource.Revision))
	writeJSON(w, http.StatusOK, resource)
}

func (s Server) listProjects(w http.ResponseWriter, request *http.Request) {
	principal, ok := s.authenticate(w, request, "PROJECT_LIST")
	if !ok {
		return
	}
	for key := range request.URL.Query() {
		if key != "limit" && key != "cursor" {
			s.writeProblem(w, request, project.ErrValidation)
			return
		}
	}
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			s.writeProblem(w, request, project.ErrValidation)
			return
		}
		limit = parsed
	}
	after := ""
	if cursor := request.URL.Query().Get("cursor"); cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 64 {
			s.writeProblem(w, request, project.ErrValidation)
			return
		}
		after = string(raw)
	}
	page, err := s.Projects.List(request.Context(), principal, project.ListRequest{AfterID: after, Limit: limit}, requestID(request))
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	response := struct {
		Items      []project.Resource `json:"items"`
		NextCursor string             `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextAfter != "" {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s Server) patchProject(w http.ResponseWriter, request *http.Request) {
	principal, ok := s.authenticate(w, request, "PROJECT_UPDATE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(request.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	patch, err := decodePatch(request)
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	resource, err := s.Projects.Patch(request.Context(), principal, request.PathValue("project_id"), revision, patch, requestID(request))
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	w.Header().Set("ETag", etag(resource.Revision))
	writeJSON(w, http.StatusOK, resource)
}

func (s Server) deleteProject(w http.ResponseWriter, request *http.Request) {
	principal, ok := s.authenticate(w, request, "PROJECT_DELETE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(request.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, request, err)
		return
	}
	if err := s.Projects.Delete(request.Context(), principal, request.PathValue("project_id"), revision, requestID(request)); err != nil {
		s.writeProblem(w, request, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s Server) authenticate(w http.ResponseWriter, request *http.Request, action string) (project.Principal, bool) {
	if s.Authenticator == nil {
		s.writeProblem(w, request, project.ErrUnauthenticated)
		return project.Principal{}, false
	}
	principal, err := s.Authenticator.Authenticate(request)
	if err == nil && principal.Valid() {
		return principal, true
	}
	if s.Projects.Store != nil {
		auditID, _ := project.NewID()
		_ = s.Projects.Store.RecordAudit(request.Context(), project.AuditEvent{AuditID: auditID, RequestID: requestID(request), Action: action, ResourceType: "PROJECT", ResourceID: request.PathValue("project_id"), ScopeType: "UNKNOWN", Principal: project.Principal{Type: "UNKNOWN"}, Result: "DENIED", ReasonCode: "UNAUTHENTICATED"})
	}
	s.writeProblem(w, request, project.ErrUnauthenticated)
	return project.Principal{}, false
}

func (s Server) requestContext(next http.Handler) http.Handler {
	timeout := s.RequestTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		id, err := project.NewID()
		if err != nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithValue(request.Context(), requestIDKey{}, id), timeout)
		defer cancel()
		w.Header().Set(RequestIDHeader, id)
		w.Header().Set("Cache-Control", "no-store")
		request.Body = http.MaxBytesReader(w, request.Body, maximumBody)
		next.ServeHTTP(w, request.WithContext(ctx))
	})
}

func (s Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log(map[string]any{"level": "error", "event": "panic_recovered", "request_id": requestID(request)})
				s.writeProblem(w, request, errors.New("internal"))
			}
		}()
		next.ServeHTTP(w, request)
	})
}

func (s Server) writeProblem(w http.ResponseWriter, request *http.Request, err error) {
	status, code, title, detail, retryable := http.StatusInternalServerError, "INTERNAL_ERROR", "Internal error", "The request could not be completed.", false
	switch {
	case errors.Is(err, project.ErrValidation):
		status, code, title, detail = 400, "VALIDATION_FAILED", "Validation failed", "The request does not satisfy the resource contract."
	case errors.Is(err, errBodyTooLarge):
		status, code, title, detail = 413, "REQUEST_TOO_LARGE", "Request too large", "The request body exceeds the bounded API limit."
	case errors.Is(err, project.ErrUnauthenticated):
		status, code, title, detail = 401, "UNAUTHENTICATED", "Authentication required", "A valid Northbound bearer token is required."
	case errors.Is(err, project.ErrForbidden):
		status, code, title, detail = 403, "FORBIDDEN", "Forbidden", "The principal is not authorized for this action."
	case errors.Is(err, project.ErrNotFound):
		status, code, title, detail = 404, "RESOURCE_NOT_FOUND", "Resource not found", "The Project does not exist or is no longer active."
	case errors.Is(err, project.ErrIdempotencyConflict):
		status, code, title, detail = 409, "IDEMPOTENCY_CONFLICT", "Idempotency conflict", "The idempotency key is already bound to another desired payload."
	case errors.Is(err, project.ErrDependencyConflict):
		status, code, title, detail = 409, "DEPENDENCY_CONFLICT", "Dependency conflict", "Dependent resources prevent Project deletion."
	case errors.Is(err, project.ErrDeleteProtected):
		status, code, title, detail = 409, "DELETE_PROTECTED", "Delete protected", "Project delete protection is enabled."
	case errors.Is(err, project.ErrConflict):
		status, code, title, detail = 409, "RESOURCE_CONFLICT", "Resource conflict", "The resource authority conflicts with the request."
	case errors.Is(err, project.ErrStaleRevision):
		status, code, title, detail = 412, "STALE_REVISION", "Stale revision", "If-Match does not identify the current resource revision."
	case errors.Is(err, project.ErrServiceUnavailable):
		status, code, title, detail, retryable = 503, "SERVICE_UNAVAILABLE", "Service unavailable", "The Project authority is not ready.", true
	}
	if code == "UNAUTHENTICATED" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kim-api"`)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "urn:kim:problem:" + strings.ToLower(code), Title: title, Status: status, Code: code, Detail: detail, RequestID: requestID(request), Retryable: retryable})
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return errBodyTooLarge
		}
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodePatch(request *http.Request) (project.Patch, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(request, &fields); err != nil {
		if errors.Is(err, errBodyTooLarge) {
			return project.Patch{}, err
		}
		return project.Patch{}, project.ErrValidation
	}
	if len(fields) == 0 {
		return project.Patch{}, project.ErrValidation
	}
	var patch project.Patch
	for name, raw := range fields {
		switch name {
		case "name":
			patch.NamePresent = true
			if string(raw) == "null" || json.Unmarshal(raw, &patch.Name) != nil {
				return project.Patch{}, project.ErrValidation
			}
		case "deleteProtection":
			patch.DeleteProtectionPresent = true
			if string(raw) == "null" || json.Unmarshal(raw, &patch.DeleteProtection) != nil {
				return project.Patch{}, project.ErrValidation
			}
		default:
			return project.Patch{}, project.ErrValidation
		}
	}
	return patch, nil
}

func parseIfMatch(raw string) (uint64, error) {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' || strings.Contains(raw[1:len(raw)-1], ",") {
		return 0, project.ErrValidation
	}
	revision, err := strconv.ParseUint(raw[1:len(raw)-1], 10, 64)
	if err != nil || revision == 0 {
		return 0, project.ErrValidation
	}
	return revision, nil
}

func etag(revision uint64) string { return fmt.Sprintf(`"%d"`, revision) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type requestIDKey struct{}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDKey{}).(string)
	return value
}
func (s Server) log(value map[string]any) {
	if s.Logger != nil {
		_ = json.NewEncoder(s.Logger).Encode(value)
	}
}
