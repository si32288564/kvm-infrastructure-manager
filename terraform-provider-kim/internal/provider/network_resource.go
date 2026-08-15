package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type networkResource struct{ phase2Resource }
type networkModel struct {
	ID                 types.String `tfsdk:"id"`
	ClientReference    types.String `tfsdk:"client_reference"`
	ProjectID          types.String `tfsdk:"project_id"`
	Name               types.String `tfsdk:"name"`
	Profile            types.String `tfsdk:"profile"`
	MTU                types.Int64  `tfsdk:"mtu"`
	SegmentPolicy      types.String `tfsdk:"segment_policy"`
	RequestedSegmentID types.Int64  `tfsdk:"requested_segment_id"`
	DeleteProtection   types.Bool   `tfsdk:"delete_protection"`
	Revision           types.Int64  `tfsdk:"revision"`
	RealizationState   types.String `tfsdk:"realization_state"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

func NewNetworkResource() resource.Resource {
	return &networkResource{phase2Resource: phase2Resource{kind: "network", plural: "networks"}}
}
func (r *networkResource) Metadata(_ context.Context, _ resource.MetadataRequest, x *resource.MetadataResponse) {
	x.TypeName = typeName + "_network"
}
func (r *networkResource) Configure(_ context.Context, q resource.ConfigureRequest, x *resource.ConfigureResponse) {
	r.configure(q, x)
}
func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, x *resource.SchemaResponse) {
	replaceS := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	replaceI := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	x.Schema = schema.Schema{Description: "KIM logical Network; physical segment/backend incarnation is not Terraform state.", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}, "client_reference": schema.StringAttribute{Required: true, WriteOnly: true}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: replaceS}, "name": schema.StringAttribute{Required: true}, "profile": schema.StringAttribute{Required: true, PlanModifiers: replaceS}, "mtu": schema.Int64Attribute{Required: true}, "segment_policy": schema.StringAttribute{Required: true, PlanModifiers: replaceS}, "requested_segment_id": schema.Int64Attribute{Optional: true, PlanModifiers: replaceI}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "realization_state": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
}
func (r *networkResource) Create(ctx context.Context, q resource.CreateRequest, x *resource.CreateResponse) {
	var m networkModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	if x.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"projectId": m.ProjectID.ValueString(), "name": m.Name.ValueString(), "profile": m.Profile.ValueString(), "mtu": m.MTU.ValueInt64(), "segmentPolicy": m.SegmentPolicy.ValueString(), "requestedSegmentId": m.RequestedSegmentID.ValueInt64(), "deleteProtection": boolValue(m.DeleteProtection)}
	var out phase2API
	if r.create(ctx, createClientReference(ctx, q.Config, &x.Diagnostics), body, &out, &x.Diagnostics) {
		x.Diagnostics.Append(x.State.Set(ctx, networkState(out))...)
	}
}
func (r *networkResource) Read(ctx context.Context, q resource.ReadRequest, x *resource.ReadResponse) {
	var m networkModel
	x.Diagnostics.Append(q.State.Get(ctx, &m)...)
	var out phase2API
	ok, gone := r.read(ctx, m.ID.ValueString(), &out, &x.Diagnostics)
	if gone {
		x.State.RemoveResource(ctx)
	} else if ok {
		x.Diagnostics.Append(x.State.Set(ctx, networkState(out))...)
	}
}
func (r *networkResource) Update(ctx context.Context, q resource.UpdateRequest, x *resource.UpdateResponse) {
	var p, s networkModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &p)...)
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	var out phase2API
	if r.update(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), map[string]any{"name": p.Name.ValueString(), "mtu": p.MTU.ValueInt64(), "deleteProtection": boolValue(p.DeleteProtection)}, &out, &x.Diagnostics) {
		x.Diagnostics.Append(x.State.Set(ctx, networkState(out))...)
	}
}
func (r *networkResource) Delete(ctx context.Context, q resource.DeleteRequest, x *resource.DeleteResponse) {
	var s networkModel
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	r.delete(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), &x.Diagnostics)
}
func (r *networkResource) ImportState(ctx context.Context, q resource.ImportStateRequest, x *resource.ImportStateResponse) {
	importID(ctx, "network", q, x)
}
func networkState(v phase2API) networkModel {
	requested := types.Int64Null()
	if v.RequestedSegmentID != 0 {
		requested = types.Int64Value(v.RequestedSegmentID)
	}
	return networkModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), Name: types.StringValue(v.Name), Profile: types.StringValue(v.Profile), MTU: types.Int64Value(v.MTU), SegmentPolicy: types.StringValue(v.SegmentPolicy), RequestedSegmentID: requested, DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), RealizationState: types.StringValue(v.RealizationState), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}
}

var _ resource.ResourceWithImportState = (*networkResource)(nil)
var _ resource.ResourceWithConfigure = (*networkResource)(nil)
