package provider

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
	"net/http"
)

type flavorResource struct{ client *client.Client }
type flavorModel struct {
	ID               types.String `tfsdk:"id"`
	ClientReference  types.String `tfsdk:"client_reference"`
	ProjectID        types.String `tfsdk:"project_id"`
	Name             types.String `tfsdk:"name"`
	VCPUs            types.Int64  `tfsdk:"vcpus"`
	MemoryMiB        types.Int64  `tfsdk:"memory_mib"`
	RootDiskGiB      types.Int64  `tfsdk:"root_disk_gib"`
	NUMAPolicy       types.String `tfsdk:"numa_policy"`
	NUMANodes        types.Int64  `tfsdk:"numa_nodes"`
	HugePageSizeKiB  types.Int64  `tfsdk:"huge_page_size_kib"`
	CPUAllocation    types.String `tfsdk:"cpu_allocation"`
	CPUPinning       types.Bool   `tfsdk:"cpu_pinning"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	Revision         types.Int64  `tfsdk:"revision"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}
type flavorAPI struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	Name             string `json:"name"`
	VCPUs            int64  `json:"vcpus"`
	MemoryMiB        int64  `json:"memoryMiB"`
	RootDiskGiB      int64  `json:"rootDiskGiB"`
	NUMAPolicy       string `json:"numaPolicy"`
	NUMANodes        *int64 `json:"numaNodes"`
	HugePageSizeKiB  *int64 `json:"hugePageSizeKiB"`
	CPUAllocation    string `json:"cpuAllocation"`
	CPUPinning       bool   `json:"cpuPinning"`
	DeleteProtection bool   `json:"deleteProtection"`
	Revision         int64  `json:"revision"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

func NewFlavorResource() resource.Resource { return &flavorResource{} }
func (r *flavorResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName + "_flavor"
}
func (r *flavorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Experimental immutable-revision KIM Flavor; updates never retrofit existing VMs.", Attributes: map[string]schema.Attribute{
		"client_reference": schema.StringAttribute{Required: true, WriteOnly: true, Description: "Stable client-owned logical resource reference for crash-safe Create; never stored in Terraform state."},
		"id":               schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}, "name": schema.StringAttribute{Required: true}, "vcpus": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.AtLeast(1)}}, "memory_mib": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.AtLeast(1)}}, "root_disk_gib": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.AtLeast(1)}}, "numa_policy": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("NONE", "REQUIRED")}}, "numa_nodes": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.AtLeast(1)}}, "huge_page_size_kib": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.AtLeast(1)}}, "cpu_allocation": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("SHARED", "DEDICATED")}}, "cpu_pinning": schema.BoolAttribute{Required: true}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
}
func (r *flavorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, resp, &r.client)
}
func flavorBody(v flavorModel) map[string]any {
	b := map[string]any{"projectId": v.ProjectID.ValueString(), "name": v.Name.ValueString(), "vcpus": v.VCPUs.ValueInt64(), "memoryMiB": v.MemoryMiB.ValueInt64(), "rootDiskGiB": v.RootDiskGiB.ValueInt64(), "numaPolicy": v.NUMAPolicy.ValueString(), "cpuAllocation": v.CPUAllocation.ValueString(), "cpuPinning": v.CPUPinning.ValueBool(), "deleteProtection": boolValue(v.DeleteProtection)}
	if x := optionalInt(v.NUMANodes); x != nil {
		b["numaNodes"] = x
	}
	if x := optionalInt(v.HugePageSizeKiB); x != nil {
		b["hugePageSizeKiB"] = x
	}
	return b
}
func flavorPatchBody(v flavorModel) map[string]any {
	b := flavorBody(v)
	delete(b, "projectId")
	return b
}
func (r *flavorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var p flavorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &p)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := flavorBody(p)
	key, _ := r.client.CreateIdempotencyKey("flavor", createClientReference(ctx, req.Config, &resp.Diagnostics), body)
	var out flavorAPI
	response, err := r.client.Do(ctx, http.MethodPost, "/flavors", body, &out, map[string]string{"Idempotency-Key": key})
	if err != nil {
		addError(&resp.Diagnostics, "Create Flavor", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, flavorState(out))...)
}
func (r *flavorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var s flavorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	var out flavorAPI
	response, err := r.client.Do(ctx, http.MethodGet, "/flavors/"+s.ID.ValueString(), nil, &out, nil)
	if notFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addError(&resp.Diagnostics, "Read Flavor", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, flavorState(out))...)
}
func (r *flavorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var p, s flavorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &p)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	var out flavorAPI
	response, err := r.client.Do(ctx, http.MethodPatch, "/flavors/"+s.ID.ValueString(), flavorPatchBody(p), &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", s.Revision.ValueInt64())})
	if err != nil {
		addError(&resp.Diagnostics, "Update Flavor", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, flavorState(out))...)
}
func (r *flavorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var s flavorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	_, err := r.client.Do(ctx, http.MethodDelete, "/flavors/"+s.ID.ValueString(), nil, nil, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", s.Revision.ValueInt64())})
	if !notFound(err) && err != nil {
		addError(&resp.Diagnostics, "Delete Flavor", err)
	}
}
func (r *flavorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, "flavor", req, resp)
}
func flavorState(v flavorAPI) flavorModel {
	m := flavorModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), Name: types.StringValue(v.Name), VCPUs: types.Int64Value(v.VCPUs), MemoryMiB: types.Int64Value(v.MemoryMiB), RootDiskGiB: types.Int64Value(v.RootDiskGiB), NUMAPolicy: types.StringValue(v.NUMAPolicy), CPUAllocation: types.StringValue(v.CPUAllocation), CPUPinning: types.BoolValue(v.CPUPinning), DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt), NUMANodes: types.Int64Null(), HugePageSizeKiB: types.Int64Null()}
	if v.NUMANodes != nil {
		m.NUMANodes = types.Int64Value(*v.NUMANodes)
	}
	if v.HugePageSizeKiB != nil {
		m.HugePageSizeKiB = types.Int64Value(*v.HugePageSizeKiB)
	}
	return m
}

var _ resource.ResourceWithImportState = (*flavorResource)(nil)
var _ resource.ResourceWithConfigure = (*flavorResource)(nil)
