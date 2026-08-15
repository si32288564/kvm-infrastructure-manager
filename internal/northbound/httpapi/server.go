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
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/availabilitypolicy"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/flavor"
	imageapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/image"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/phase2"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
	vmapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/vm"
)

const (
	RequestIDHeader = "X-Request-ID"
	maximumBody     = 64 << 10
)

var errBodyTooLarge = errors.New("request body exceeds the maximum size")

type Server struct {
	Projects             project.Service
	Flavors              flavor.Service
	AvailabilityPolicies availabilitypolicy.Service
	Images               imageapi.Service
	Phase2               phase2.Service
	VMs                  vmapi.Service
	Authenticator        auth.Authenticator
	Logger               io.Writer
	RequestTimeout       time.Duration
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
	mux.HandleFunc("POST /api/v1/flavors", s.createFlavor)
	mux.HandleFunc("GET /api/v1/flavors", s.listFlavors)
	mux.HandleFunc("GET /api/v1/flavors/{flavor_id}", s.getFlavor)
	mux.HandleFunc("PATCH /api/v1/flavors/{flavor_id}", s.patchFlavor)
	mux.HandleFunc("DELETE /api/v1/flavors/{flavor_id}", s.deleteFlavor)
	mux.HandleFunc("POST /api/v1/availability-policies", s.createAvailabilityPolicy)
	mux.HandleFunc("GET /api/v1/availability-policies", s.listAvailabilityPolicies)
	mux.HandleFunc("GET /api/v1/availability-policies/{policy_id}", s.getAvailabilityPolicy)
	mux.HandleFunc("PATCH /api/v1/availability-policies/{policy_id}", s.patchAvailabilityPolicy)
	mux.HandleFunc("DELETE /api/v1/availability-policies/{policy_id}", s.deleteAvailabilityPolicy)
	mux.HandleFunc("POST /api/v1/images", s.createImage)
	mux.HandleFunc("GET /api/v1/images", s.listImages)
	mux.HandleFunc("GET /api/v1/images/{image_id}", s.getImage)
	mux.HandleFunc("PATCH /api/v1/images/{image_id}", s.patchImage)
	mux.HandleFunc("DELETE /api/v1/images/{image_id}", s.deleteImage)
	mux.HandleFunc("POST /api/v1/images/{image_id}/ingestions", s.ingestImage)
	mux.HandleFunc("GET /api/v1/operations/{operation_id}", s.getOperation)
	mux.HandleFunc("POST /api/v1/vms", s.createVM)
	mux.HandleFunc("GET /api/v1/vms", s.listVMs)
	mux.HandleFunc("GET /api/v1/vms/{vm_id}", s.getVM)
	mux.HandleFunc("PATCH /api/v1/vms/{vm_id}", s.patchVM)
	mux.HandleFunc("DELETE /api/v1/vms/{vm_id}", s.deleteVM)
	for _, binding := range []struct {
		plural string
		kind   phase2.Kind
	}{{"networks", phase2.Network}, {"subnets", phase2.Subnet}, {"ports", phase2.Port}, {"volumes", phase2.Volume}} {
		plural, kind := binding.plural, binding.kind
		mux.HandleFunc("POST /api/v1/"+plural, func(w http.ResponseWriter, r *http.Request) { s.createPhase2(w, r, kind) })
		mux.HandleFunc("GET /api/v1/"+plural, func(w http.ResponseWriter, r *http.Request) { s.listPhase2(w, r, kind) })
		mux.HandleFunc("GET /api/v1/"+plural+"/{resource_id}", func(w http.ResponseWriter, r *http.Request) { s.getPhase2(w, r, kind) })
		mux.HandleFunc("PATCH /api/v1/"+plural+"/{resource_id}", func(w http.ResponseWriter, r *http.Request) { s.patchPhase2(w, r, kind) })
		mux.HandleFunc("DELETE /api/v1/"+plural+"/{resource_id}", func(w http.ResponseWriter, r *http.Request) { s.deletePhase2(w, r, kind) })
	}
	return s.requestContext(s.recover(mux))
}

func (s Server) createVM(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "VM_CREATE")
	if !ok {
		return
	}
	var desired vmapi.Desired
	if err := decodeJSON(r, &desired); err != nil {
		s.writeProblem(w, r, resource.ErrValidation)
		return
	}
	out, _, err := s.VMs.Create(r.Context(), p, vmapi.CreateRequest{Desired: desired, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestID: requestID(r), CanonicalPath: "/api/v1/vms"})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	w.Header().Set("Location", "/api/v1/vms/"+out.ID)
	writeJSON(w, 201, out)
}
func (s Server) getVM(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "VM_READ")
	if !ok {
		return
	}
	out, err := s.VMs.Get(r.Context(), p, r.PathValue("vm_id"), requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) listVMs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "VM_LIST")
	if !ok {
		return
	}
	limit, after, err := parsePage(r, map[string]bool{"limit": true, "cursor": true, "projectId": true})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	page, err := s.VMs.List(r.Context(), p, vmapi.ListRequest{ProjectID: r.URL.Query().Get("projectId"), AfterID: after, Limit: limit}, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	response := struct {
		Items      []vmapi.Resource `json:"items"`
		NextCursor string           `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextAfter != "" {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeJSON(w, 200, response)
}
func (s Server) patchVM(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "VM_UPDATE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	patch, err := decodeVMPatch(r)
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	out, err := s.VMs.Patch(r.Context(), p, r.PathValue("vm_id"), revision, patch, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	if out.OperationID != "" && out.ConvergenceState != "CONVERGED" {
		w.Header().Set("Location", "/api/v1/operations/"+out.OperationID)
		writeJSON(w, 202, out)
		return
	}
	writeJSON(w, 200, out)
}
func (s Server) deleteVM(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "VM_DELETE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	op, err := s.VMs.Delete(r.Context(), p, r.PathValue("vm_id"), revision, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+op.ID)
	writeJSON(w, 202, op)
}

func (s Server) createPhase2(w http.ResponseWriter, r *http.Request, k phase2.Kind) {
	p, ok := s.authenticate(w, r, string(k)+"_CREATE")
	if !ok {
		return
	}
	var d phase2.Desired
	if err := decodeJSON(r, &d); err != nil {
		s.writeProblem(w, r, resource.ErrValidation)
		return
	}
	out, _, err := s.Phase2.Create(r.Context(), p, k, phase2.CreateRequest{Desired: d, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestID: requestID(r), CanonicalPath: "/api/v1/" + k.Plural()})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	w.Header().Set("Location", "/api/v1/"+k.Plural()+"/"+out.ID)
	writeJSON(w, 201, out)
}
func (s Server) getPhase2(w http.ResponseWriter, r *http.Request, k phase2.Kind) {
	p, ok := s.authenticate(w, r, string(k)+"_READ")
	if !ok {
		return
	}
	out, err := s.Phase2.Get(r.Context(), p, k, r.PathValue("resource_id"), requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) listPhase2(w http.ResponseWriter, r *http.Request, k phase2.Kind) {
	p, ok := s.authenticate(w, r, string(k)+"_LIST")
	if !ok {
		return
	}
	limit, after, err := parsePage(r, map[string]bool{"limit": true, "cursor": true, "projectId": true})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	page, err := s.Phase2.List(r.Context(), p, k, phase2.ListRequest{ProjectID: r.URL.Query().Get("projectId"), AfterID: after, Limit: limit}, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	response := struct {
		Items      []phase2.Resource `json:"items"`
		NextCursor string            `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextAfter != "" {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeJSON(w, 200, response)
}
func (s Server) patchPhase2(w http.ResponseWriter, r *http.Request, k phase2.Kind) {
	p, ok := s.authenticate(w, r, string(k)+"_UPDATE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	patch, err := decodePhase2Patch(r)
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	out, err := s.Phase2.Patch(r.Context(), p, k, r.PathValue("resource_id"), revision, patch, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) deletePhase2(w http.ResponseWriter, r *http.Request, k phase2.Kind) {
	p, ok := s.authenticate(w, r, string(k)+"_DELETE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	op, err := s.Phase2.Delete(r.Context(), p, k, r.PathValue("resource_id"), revision, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+op.ID)
	writeJSON(w, 202, op)
}

func (s Server) createImage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "IMAGE_CREATE")
	if !ok {
		return
	}
	var body imageapi.Desired
	if err := decodeJSON(r, &body); err != nil {
		s.writeProblem(w, r, resource.ErrValidation)
		return
	}
	out, _, err := s.Images.Create(r.Context(), p, imageapi.CreateRequest{Desired: body, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestID: requestID(r), CanonicalPath: "/api/v1/images"})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	w.Header().Set("Location", "/api/v1/images/"+out.ID)
	writeJSON(w, 201, out)
}
func (s Server) getImage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "IMAGE_READ")
	if !ok {
		return
	}
	out, err := s.Images.Get(r.Context(), p, r.PathValue("image_id"), requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) listImages(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "IMAGE_LIST")
	if !ok {
		return
	}
	limit, after, err := parsePage(r, map[string]bool{"limit": true, "cursor": true, "projectId": true})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	page, err := s.Images.List(r.Context(), p, imageapi.ListRequest{ProjectID: r.URL.Query().Get("projectId"), AfterID: after, Limit: limit}, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	response := struct {
		Items      []imageapi.Resource `json:"items"`
		NextCursor string              `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextAfter != "" {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeJSON(w, 200, response)
}
func (s Server) patchImage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "IMAGE_UPDATE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	patch, err := decodeImagePatch(r)
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	out, err := s.Images.Patch(r.Context(), p, r.PathValue("image_id"), revision, patch, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "IMAGE_DELETE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	if err := s.Images.Delete(r.Context(), p, r.PathValue("image_id"), revision, requestID(r)); err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s Server) ingestImage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "IMAGE_INGEST")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	if r.Body != nil {
		var empty struct{}
		if err := decodeJSON(r, &empty); err != nil {
			s.writeProblem(w, r, resource.ErrValidation)
			return
		}
	}
	out, _, err := s.Images.Ingest(r.Context(), p, r.PathValue("image_id"), revision, imageapi.IngestionRequest{IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestID: requestID(r)})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+out.ID)
	writeJSON(w, 202, out)
}
func (s Server) getOperation(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "OPERATION_READ")
	if !ok {
		return
	}
	if s.VMs.Store != nil {
		out, err := s.VMs.GetOperation(r.Context(), p, r.PathValue("operation_id"), requestID(r))
		if err == nil {
			writeJSON(w, 200, out)
			return
		}
		if !errors.Is(err, resource.ErrNotFound) {
			s.writeProblem(w, r, err)
			return
		}
	}
	if s.Phase2.Store != nil {
		out, err := s.Phase2.GetOperation(r.Context(), p, r.PathValue("operation_id"), requestID(r))
		if err == nil {
			writeJSON(w, 200, out)
			return
		}
		if !errors.Is(err, resource.ErrNotFound) {
			s.writeProblem(w, r, err)
			return
		}
	}
	out, err := s.Images.GetOperation(r.Context(), p, r.PathValue("operation_id"), requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s Server) createAvailabilityPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "AVAILABILITY_POLICY_CREATE")
	if !ok {
		return
	}
	var body availabilitypolicy.Desired
	if err := decodeJSON(r, &body); err != nil {
		if !errors.Is(err, errBodyTooLarge) {
			err = resource.ErrValidation
		}
		s.writeProblem(w, r, err)
		return
	}
	out, _, err := s.AvailabilityPolicies.Create(r.Context(), p, availabilitypolicy.CreateRequest{Desired: body, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestID: requestID(r), CanonicalPath: "/api/v1/availability-policies"})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	w.Header().Set("Location", "/api/v1/availability-policies/"+out.ID)
	writeJSON(w, 201, out)
}
func (s Server) getAvailabilityPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "AVAILABILITY_POLICY_READ")
	if !ok {
		return
	}
	out, err := s.AvailabilityPolicies.Get(r.Context(), p, r.PathValue("policy_id"), requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) listAvailabilityPolicies(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "AVAILABILITY_POLICY_LIST")
	if !ok {
		return
	}
	limit, after, err := parsePage(r, map[string]bool{"limit": true, "cursor": true})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	page, err := s.AvailabilityPolicies.List(r.Context(), p, availabilitypolicy.ListRequest{AfterID: after, Limit: limit}, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	response := struct {
		Items      []availabilitypolicy.Resource `json:"items"`
		NextCursor string                        `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextAfter != "" {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeJSON(w, 200, response)
}
func (s Server) patchAvailabilityPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "AVAILABILITY_POLICY_UPDATE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	patch, err := decodeAvailabilityPolicyPatch(r)
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	out, err := s.AvailabilityPolicies.Patch(r.Context(), p, r.PathValue("policy_id"), revision, patch, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) deleteAvailabilityPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "AVAILABILITY_POLICY_DELETE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	if err := s.AvailabilityPolicies.Delete(r.Context(), p, r.PathValue("policy_id"), revision, requestID(r)); err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.WriteHeader(204)
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
	limit, after, err := parsePage(request, map[string]bool{"limit": true, "cursor": true})
	if err != nil {
		s.writeProblem(w, request, err)
		return
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

func (s Server) createFlavor(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "FLAVOR_CREATE")
	if !ok {
		return
	}
	var body flavor.Desired
	if err := decodeJSON(r, &body); err != nil {
		if !errors.Is(err, errBodyTooLarge) {
			err = resource.ErrValidation
		}
		s.writeProblem(w, r, err)
		return
	}
	out, _, err := s.Flavors.Create(r.Context(), p, flavor.CreateRequest{Desired: body, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestID: requestID(r), CanonicalPath: "/api/v1/flavors"})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	w.Header().Set("Location", "/api/v1/flavors/"+out.ID)
	writeJSON(w, 201, out)
}
func (s Server) getFlavor(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "FLAVOR_READ")
	if !ok {
		return
	}
	out, err := s.Flavors.Get(r.Context(), p, r.PathValue("flavor_id"), requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) listFlavors(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "FLAVOR_LIST")
	if !ok {
		return
	}
	limit, after, err := parsePage(r, map[string]bool{"limit": true, "cursor": true, "projectId": true})
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	page, err := s.Flavors.List(r.Context(), p, flavor.ListRequest{ProjectID: r.URL.Query().Get("projectId"), AfterID: after, Limit: limit}, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	response := struct {
		Items      []flavor.Resource `json:"items"`
		NextCursor string            `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextAfter != "" {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeJSON(w, 200, response)
}
func (s Server) patchFlavor(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "FLAVOR_UPDATE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	patch, err := decodeFlavorPatch(r)
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	out, err := s.Flavors.Patch(r.Context(), p, r.PathValue("flavor_id"), revision, patch, requestID(r))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(out.Revision))
	writeJSON(w, 200, out)
}
func (s Server) deleteFlavor(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r, "FLAVOR_DELETE")
	if !ok {
		return
	}
	revision, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	if err := s.Flavors.Delete(r.Context(), p, r.PathValue("flavor_id"), revision, requestID(r)); err != nil {
		s.writeProblem(w, r, err)
		return
	}
	w.WriteHeader(204)
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
	case errors.Is(err, resource.ErrValidation):
		status, code, title, detail = 400, "VALIDATION_FAILED", "Validation failed", "The request does not satisfy the resource contract."
	case errors.Is(err, errBodyTooLarge):
		status, code, title, detail = 413, "REQUEST_TOO_LARGE", "Request too large", "The request body exceeds the bounded API limit."
	case errors.Is(err, resource.ErrUnauthenticated):
		status, code, title, detail = 401, "UNAUTHENTICATED", "Authentication required", "A valid Northbound bearer token is required."
	case errors.Is(err, resource.ErrForbidden):
		status, code, title, detail = 403, "FORBIDDEN", "Forbidden", "The principal is not authorized for this action."
	case errors.Is(err, resource.ErrNotFound):
		status, code, title, detail = 404, "RESOURCE_NOT_FOUND", "Resource not found", "The resource does not exist or is no longer active."
	case errors.Is(err, resource.ErrIdempotencyConflict):
		status, code, title, detail = 409, "IDEMPOTENCY_CONFLICT", "Idempotency conflict", "The idempotency key is already bound to another desired payload."
	case errors.Is(err, resource.ErrDependencyConflict):
		status, code, title, detail = 409, "DEPENDENCY_CONFLICT", "Dependency conflict", "Dependent resources prevent deletion."
	case errors.Is(err, resource.ErrDeleteProtected):
		status, code, title, detail = 409, "DELETE_PROTECTED", "Delete protected", "Resource delete protection is enabled."
	case errors.Is(err, resource.ErrConflict):
		status, code, title, detail = 409, "RESOURCE_CONFLICT", "Resource conflict", "The resource authority conflicts with the request."
	case errors.Is(err, resource.ErrStaleRevision):
		status, code, title, detail = 412, "STALE_REVISION", "Stale revision", "If-Match does not identify the current resource revision."
	case errors.Is(err, resource.ErrServiceUnavailable):
		status, code, title, detail, retryable = 503, "SERVICE_UNAVAILABLE", "Service unavailable", "The resource authority is not ready.", true
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

func parsePage(request *http.Request, allowed map[string]bool) (int, string, error) {
	for key := range request.URL.Query() {
		if !allowed[key] {
			return 0, "", resource.ErrValidation
		}
	}
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, "", resource.ErrValidation
		}
		limit = parsed
	}
	after := ""
	if cursor := request.URL.Query().Get("cursor"); cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 64 {
			return 0, "", resource.ErrValidation
		}
		after = string(raw)
	}
	return limit, after, nil
}

func decodeFlavorPatch(request *http.Request) (flavor.Patch, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(request, &fields); err != nil {
		if errors.Is(err, errBodyTooLarge) {
			return flavor.Patch{}, err
		}
		return flavor.Patch{}, resource.ErrValidation
	}
	if len(fields) == 0 {
		return flavor.Patch{}, resource.ErrValidation
	}
	var p flavor.Patch
	for name, raw := range fields {
		null := string(raw) == "null"
		switch name {
		case "name":
			if null {
				return p, resource.ErrValidation
			}
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Name = &v
		case "vcpus":
			if null {
				return p, resource.ErrValidation
			}
			var v uint64
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.VCPUs = &v
		case "memoryMiB":
			if null {
				return p, resource.ErrValidation
			}
			var v uint64
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.MemoryMiB = &v
		case "rootDiskGiB":
			if null {
				return p, resource.ErrValidation
			}
			var v uint64
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.RootDiskGiB = &v
		case "numaPolicy":
			if null {
				return p, resource.ErrValidation
			}
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.NUMAPolicy = &v
		case "numaNodes":
			var v *uint32
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.NUMANodes = &v
		case "hugePageSizeKiB":
			var v *uint64
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.HugePageSizeKiB = &v
		case "cpuAllocation":
			if null {
				return p, resource.ErrValidation
			}
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.CPUAllocation = &v
		case "cpuPinning":
			if null {
				return p, resource.ErrValidation
			}
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.CPUPinning = &v
		case "deleteProtection":
			if null {
				return p, resource.ErrValidation
			}
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.DeleteProtection = &v
		default:
			return p, resource.ErrValidation
		}
	}
	return p, nil
}

func decodeAvailabilityPolicyPatch(request *http.Request) (availabilitypolicy.Patch, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(request, &fields); err != nil {
		if errors.Is(err, errBodyTooLarge) {
			return availabilitypolicy.Patch{}, err
		}
		return availabilitypolicy.Patch{}, resource.ErrValidation
	}
	if len(fields) == 0 {
		return availabilitypolicy.Patch{}, resource.ErrValidation
	}
	var p availabilitypolicy.Patch
	for name, raw := range fields {
		if string(raw) == "null" {
			return p, resource.ErrValidation
		}
		switch name {
		case "name":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Name = &v
		case "availabilityMode":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.AvailabilityMode = &v
		case "maxAttempts":
			var v int
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.MaxAttempts = &v
		case "deleteProtection":
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.DeleteProtection = &v
		default:
			return p, resource.ErrValidation
		}
	}
	return p, nil
}

func decodeImagePatch(request *http.Request) (imageapi.Patch, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(request, &fields); err != nil || len(fields) == 0 {
		return imageapi.Patch{}, resource.ErrValidation
	}
	var p imageapi.Patch
	for name, raw := range fields {
		if string(raw) == "null" {
			return p, resource.ErrValidation
		}
		switch name {
		case "name":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Name = &v
		case "architecture":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Architecture = &v
		case "format":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Format = &v
		case "expectedDigest":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.ExpectedDigest = &v
		case "sourceId":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.SourceID = &v
		case "visibility":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Visibility = &v
		case "lifecycleState":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.LifecycleState = &v
		case "deleteProtection":
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.DeleteProtection = &v
		default:
			return p, resource.ErrValidation
		}
	}
	return p, nil
}

func decodePhase2Patch(request *http.Request) (phase2.Patch, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(request, &fields); err != nil || len(fields) == 0 {
		return phase2.Patch{}, resource.ErrValidation
	}
	var p phase2.Patch
	for name, raw := range fields {
		if string(raw) == "null" {
			return p, resource.ErrValidation
		}
		switch name {
		case "name":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Name = &v
		case "deleteProtection":
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.DeleteProtection = &v
		case "mtu":
			var v uint32
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.MTU = &v
		default:
			return p, resource.ErrValidation
		}
	}
	return p, nil
}

func decodeVMPatch(request *http.Request) (vmapi.Patch, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(request, &fields); err != nil || len(fields) == 0 {
		return vmapi.Patch{}, resource.ErrValidation
	}
	var p vmapi.Patch
	for name, raw := range fields {
		if string(raw) == "null" {
			return p, resource.ErrValidation
		}
		switch name {
		case "name":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.Name = &v
		case "deleteProtection":
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.DeleteProtection = &v
		case "desiredPowerState":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return p, resource.ErrValidation
			}
			p.DesiredPowerState = &v
		default:
			return p, resource.ErrValidation
		}
	}
	return p, nil
}

func parseIfMatch(raw string) (uint64, error) {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' || strings.Contains(raw[1:len(raw)-1], ",") {
		return 0, resource.ErrValidation
	}
	revision, err := strconv.ParseUint(raw[1:len(raw)-1], 10, 64)
	if err != nil || revision == 0 {
		return 0, resource.ErrValidation
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
