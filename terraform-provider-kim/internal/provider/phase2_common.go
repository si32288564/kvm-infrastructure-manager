package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
)

type phase2Resource struct {
	kind, plural string
	client       *client.Client
}
type phase2API struct {
	ID                   string   `json:"id"`
	ProjectID            string   `json:"projectId"`
	Name                 string   `json:"name"`
	DeleteProtection     bool     `json:"deleteProtection"`
	Profile              string   `json:"profile"`
	MTU                  int64    `json:"mtu"`
	SegmentPolicy        string   `json:"segmentPolicy"`
	RequestedSegmentID   int64    `json:"requestedSegmentId"`
	NetworkID            string   `json:"networkId"`
	IPFamily             string   `json:"ipFamily"`
	CIDR                 string   `json:"cidr"`
	GatewayPolicy        string   `json:"gatewayPolicy"`
	GatewayAddress       string   `json:"gatewayAddress"`
	AllocationPolicy     string   `json:"allocationPolicy"`
	AllocationStart      string   `json:"allocationStart"`
	AllocationEnd        string   `json:"allocationEnd"`
	ReservedAddresses    []string `json:"reservedAddresses"`
	DHCPEnabled          bool     `json:"dhcpEnabled"`
	DNSServers           []string `json:"dnsServers"`
	MACPolicy            string   `json:"macPolicy"`
	RequestedMAC         string   `json:"requestedMac"`
	SubnetID             string   `json:"subnetId"`
	IPAllocationMode     string   `json:"ipAllocationMode"`
	RequestedIP          string   `json:"requestedIp"`
	AttachmentPolicy     string   `json:"attachmentPolicy"`
	DatapathProfile      string   `json:"datapathProfile"`
	SizeBytes            int64    `json:"sizeBytes"`
	StorageClassID       string   `json:"storageClassId"`
	StorageClassRevision int64    `json:"storageClassRevision"`
	Bootable             bool     `json:"bootable"`
	SourceType           string   `json:"sourceType"`
	SourceImageID        string   `json:"sourceImageId"`
	SourceImageRevision  int64    `json:"sourceImageRevision"`
	SourceArtifactDigest string   `json:"sourceArtifactDigest"`
	Revision             int64    `json:"revision"`
	LifecycleState       string   `json:"lifecycleState"`
	RealizationState     string   `json:"realizationState"`
	OperationID          string   `json:"operationId"`
	CreatedAt            string   `json:"createdAt"`
	UpdatedAt            string   `json:"updatedAt"`
}

func (r *phase2Resource) configure(req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, resp, &r.client)
}
func (r *phase2Resource) create(ctx context.Context, clientReference string, body map[string]any, out *phase2API, diagnostics *diag.Diagnostics) bool {
	key, err := r.client.CreateIdempotencyKey(r.kind, clientReference, body)
	if err != nil {
		addError(diagnostics, "Create "+r.kind, err)
		return false
	}
	response, err := r.client.Do(ctx, http.MethodPost, "/"+r.plural, body, out, map[string]string{"Idempotency-Key": key})
	if err != nil {
		addError(diagnostics, "Create "+r.kind, err)
		return false
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	return r.waitAndRefresh(ctx, out, diagnostics)
}
func (r *phase2Resource) read(ctx context.Context, id string, out *phase2API, diagnostics *diag.Diagnostics) (bool, bool) {
	response, err := r.client.Do(ctx, http.MethodGet, "/"+r.plural+"/"+id, nil, out, nil)
	if notFound(err) {
		return false, true
	}
	if err != nil {
		addError(diagnostics, "Read "+r.kind, err)
		return false, false
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	return true, false
}
func (r *phase2Resource) update(ctx context.Context, id string, revision int64, body map[string]any, out *phase2API, diagnostics *diag.Diagnostics) bool {
	response, err := r.client.Do(ctx, http.MethodPatch, "/"+r.plural+"/"+id, body, out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", revision)})
	if err != nil {
		addError(diagnostics, "Update "+r.kind, err)
		return false
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	return r.waitAndRefresh(ctx, out, diagnostics)
}
func (r *phase2Resource) delete(ctx context.Context, id string, revision int64, diagnostics *diag.Diagnostics) {
	var op client.Operation
	_, err := r.client.Do(ctx, http.MethodDelete, "/"+r.plural+"/"+id, nil, &op, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", revision)})
	if notFound(err) {
		return
	}
	if err != nil {
		addError(diagnostics, "Delete "+r.kind, err)
		return
	}
	if _, err = r.client.WaitOperation(ctx, op.ID); err != nil {
		addError(diagnostics, "Delete "+r.kind, err)
	}
}
func (r *phase2Resource) waitAndRefresh(ctx context.Context, out *phase2API, diagnostics *diag.Diagnostics) bool {
	if out.OperationID != "" {
		if _, err := r.client.WaitOperation(ctx, out.OperationID); err != nil {
			addError(diagnostics, r.kind+" convergence", err)
			return false
		}
		response, err := r.client.Do(ctx, http.MethodGet, "/"+r.plural+"/"+out.ID, nil, out, nil)
		if err != nil {
			addError(diagnostics, "Refresh "+r.kind, err)
			return false
		}
		out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	}
	return true
}
