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

type availabilityPolicyResource struct{ client *client.Client }
type availabilityModel struct {
	ID               types.String `tfsdk:"id"`
	ClientReference  types.String `tfsdk:"client_reference"`
	Name             types.String `tfsdk:"name"`
	AvailabilityMode types.String `tfsdk:"availability_mode"`
	MaxAttempts      types.Int64  `tfsdk:"max_attempts"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	Revision         types.Int64  `tfsdk:"revision"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}
type availabilityAPI struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AvailabilityMode string `json:"availabilityMode"`
	MaxAttempts      int64  `json:"maxAttempts"`
	DeleteProtection bool   `json:"deleteProtection"`
	Revision         int64  `json:"revision"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

func NewAvailabilityPolicyResource() resource.Resource { return &availabilityPolicyResource{} }
func (r *availabilityPolicyResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName + "_availability_policy"
}
func (r *availabilityPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Experimental closed SYSTEM Availability Policy (MANUAL or WORKLOAD_MANAGED).", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}, "name": schema.StringAttribute{Required: true}, "availability_mode": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("MANUAL", "WORKLOAD_MANAGED")}}, "max_attempts": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.Between(1, 100)}}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
	resp.Schema.Attributes["client_reference"] = schema.StringAttribute{Required: true, WriteOnly: true, Description: "Stable client-owned logical resource reference for crash-safe Create; never stored in Terraform state."}
}
func (r *availabilityPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, resp, &r.client)
}
func availabilityBody(v availabilityModel) map[string]any {
	return map[string]any{"name": v.Name.ValueString(), "availabilityMode": v.AvailabilityMode.ValueString(), "maxAttempts": v.MaxAttempts.ValueInt64(), "deleteProtection": boolValue(v.DeleteProtection)}
}
func (r *availabilityPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var p availabilityModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &p)...)
	body := availabilityBody(p)
	key, _ := r.client.CreateIdempotencyKey("availability-policy", createClientReference(ctx, req.Config, &resp.Diagnostics), body)
	var out availabilityAPI
	response, err := r.client.Do(ctx, http.MethodPost, "/availability-policies", body, &out, map[string]string{"Idempotency-Key": key})
	if err != nil {
		addError(&resp.Diagnostics, "Create Availability Policy", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, availabilityState(out))...)
}
func (r *availabilityPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var s availabilityModel
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	var out availabilityAPI
	response, err := r.client.Do(ctx, http.MethodGet, "/availability-policies/"+s.ID.ValueString(), nil, &out, nil)
	if notFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addError(&resp.Diagnostics, "Read Availability Policy", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, availabilityState(out))...)
}
func (r *availabilityPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var p, s availabilityModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &p)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	var out availabilityAPI
	response, err := r.client.Do(ctx, http.MethodPatch, "/availability-policies/"+s.ID.ValueString(), availabilityBody(p), &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", s.Revision.ValueInt64())})
	if err != nil {
		addError(&resp.Diagnostics, "Update Availability Policy", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, availabilityState(out))...)
}
func (r *availabilityPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var s availabilityModel
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	_, err := r.client.Do(ctx, http.MethodDelete, "/availability-policies/"+s.ID.ValueString(), nil, nil, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", s.Revision.ValueInt64())})
	if !notFound(err) && err != nil {
		addError(&resp.Diagnostics, "Delete Availability Policy", err)
	}
}
func (r *availabilityPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, "availability-policy", req, resp)
}
func availabilityState(v availabilityAPI) availabilityModel {
	return availabilityModel{ID: types.StringValue(v.ID), Name: types.StringValue(v.Name), AvailabilityMode: types.StringValue(v.AvailabilityMode), MaxAttempts: types.Int64Value(v.MaxAttempts), DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}
}

var _ resource.ResourceWithImportState = (*availabilityPolicyResource)(nil)
var _ resource.ResourceWithConfigure = (*availabilityPolicyResource)(nil)
