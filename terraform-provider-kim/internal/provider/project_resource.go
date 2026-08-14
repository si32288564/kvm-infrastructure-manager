package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
)

type projectResource struct{ client *client.Client }
type projectModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	Revision         types.Int64  `tfsdk:"revision"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}
type projectAPI struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	DeleteProtection bool   `json:"deleteProtection"`
	Revision         int64  `json:"revision"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

func NewProjectResource() resource.Resource { return &projectResource{} }
func (r *projectResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName + "_project"
}
func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Experimental persistent KIM Project.", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}, "name": schema.StringAttribute{Required: true}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
}
func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, resp, &r.client)
}
func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": plan.Name.ValueString(), "deleteProtection": boolValue(plan.DeleteProtection)}
	key, err := client.CreateIdempotencyKey("project", body)
	if err != nil {
		addError(&resp.Diagnostics, "Create Project", err)
		return
	}
	var out projectAPI
	response, err := r.client.Do(ctx, http.MethodPost, "/projects", body, &out, map[string]string{"Idempotency-Key": key})
	if err != nil {
		addError(&resp.Diagnostics, "Create Project", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, projectState(out))...)
}
func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out projectAPI
	response, err := r.client.Do(ctx, http.MethodGet, "/projects/"+state.ID.ValueString(), nil, &out, nil)
	if notFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addError(&resp.Diagnostics, "Read Project", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, projectState(out))...)
}
func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": plan.Name.ValueString(), "deleteProtection": boolValue(plan.DeleteProtection)}
	var out projectAPI
	response, err := r.client.Do(ctx, http.MethodPatch, "/projects/"+state.ID.ValueString(), body, &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", state.Revision.ValueInt64())})
	if err != nil {
		addError(&resp.Diagnostics, "Update Project", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, projectState(out))...)
}
func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.Do(ctx, http.MethodDelete, "/projects/"+state.ID.ValueString(), nil, nil, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", state.Revision.ValueInt64())})
	if notFound(err) {
		return
	}
	if err != nil {
		addError(&resp.Diagnostics, "Delete Project", err)
	}
}
func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, "project", req, resp)
}
func projectState(v projectAPI) projectModel {
	return projectModel{ID: types.StringValue(v.ID), Name: types.StringValue(v.Name), DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}
}

var _ resource.ResourceWithImportState = (*projectResource)(nil)
var _ resource.ResourceWithConfigure = (*projectResource)(nil)
