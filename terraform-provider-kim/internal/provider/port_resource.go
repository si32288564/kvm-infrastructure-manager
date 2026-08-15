package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type portResource struct{ phase2Resource }
type portModel struct {
	ID               types.String `tfsdk:"id"`
	ClientReference  types.String `tfsdk:"client_reference"`
	ProjectID        types.String `tfsdk:"project_id"`
	NetworkID        types.String `tfsdk:"network_id"`
	Name             types.String `tfsdk:"name"`
	MACPolicy        types.String `tfsdk:"mac_policy"`
	RequestedMAC     types.String `tfsdk:"requested_mac"`
	SubnetID         types.String `tfsdk:"subnet_id"`
	IPAllocationMode types.String `tfsdk:"ip_allocation_mode"`
	RequestedIP      types.String `tfsdk:"requested_ip"`
	AttachmentPolicy types.String `tfsdk:"attachment_policy"`
	DatapathProfile  types.String `tfsdk:"datapath_profile"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	Revision         types.Int64  `tfsdk:"revision"`
	RealizationState types.String `tfsdk:"realization_state"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}

func NewPortResource() resource.Resource {
	return &portResource{phase2Resource: phase2Resource{kind: "port", plural: "ports"}}
}
func (r *portResource) Metadata(_ context.Context, _ resource.MetadataRequest, x *resource.MetadataResponse) {
	x.TypeName = typeName + "_port"
}
func (r *portResource) Configure(_ context.Context, q resource.ConfigureRequest, x *resource.ConfigureResponse) {
	r.configure(q, x)
}
func (r *portResource) Schema(_ context.Context, _ resource.SchemaRequest, x *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	x.Schema = schema.Schema{Description: "KIM unattached logical Port; allocation and Host binding identities are not Terraform state.", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}, "client_reference": schema.StringAttribute{Required: true, WriteOnly: true}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: replace}, "network_id": schema.StringAttribute{Required: true, PlanModifiers: replace}, "name": schema.StringAttribute{Required: true}, "mac_policy": schema.StringAttribute{Required: true, PlanModifiers: replace}, "requested_mac": schema.StringAttribute{Optional: true, PlanModifiers: replace}, "subnet_id": schema.StringAttribute{Optional: true, PlanModifiers: replace}, "ip_allocation_mode": schema.StringAttribute{Required: true, PlanModifiers: replace}, "requested_ip": schema.StringAttribute{Optional: true, PlanModifiers: replace}, "attachment_policy": schema.StringAttribute{Required: true, PlanModifiers: replace}, "datapath_profile": schema.StringAttribute{Required: true, PlanModifiers: replace}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "realization_state": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
}
func (r *portResource) Create(ctx context.Context, q resource.CreateRequest, x *resource.CreateResponse) {
	var m portModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	if x.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"projectId": m.ProjectID.ValueString(), "networkId": m.NetworkID.ValueString(), "name": m.Name.ValueString(), "macPolicy": m.MACPolicy.ValueString(), "requestedMac": m.RequestedMAC.ValueString(), "subnetId": m.SubnetID.ValueString(), "ipAllocationMode": m.IPAllocationMode.ValueString(), "requestedIp": m.RequestedIP.ValueString(), "attachmentPolicy": m.AttachmentPolicy.ValueString(), "datapathProfile": m.DatapathProfile.ValueString(), "deleteProtection": boolValue(m.DeleteProtection)}
	var out phase2API
	if r.create(ctx, createClientReference(ctx, q.Config, &x.Diagnostics), body, &out, &x.Diagnostics) {
		x.Diagnostics.Append(x.State.Set(ctx, portState(out))...)
	}
}
func (r *portResource) Read(ctx context.Context, q resource.ReadRequest, x *resource.ReadResponse) {
	var m portModel
	x.Diagnostics.Append(q.State.Get(ctx, &m)...)
	var out phase2API
	ok, gone := r.read(ctx, m.ID.ValueString(), &out, &x.Diagnostics)
	if gone {
		x.State.RemoveResource(ctx)
	} else if ok {
		x.Diagnostics.Append(x.State.Set(ctx, portState(out))...)
	}
}
func (r *portResource) Update(ctx context.Context, q resource.UpdateRequest, x *resource.UpdateResponse) {
	var p, s portModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &p)...)
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	var out phase2API
	if r.update(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), map[string]any{"name": p.Name.ValueString(), "deleteProtection": boolValue(p.DeleteProtection)}, &out, &x.Diagnostics) {
		x.Diagnostics.Append(x.State.Set(ctx, portState(out))...)
	}
}
func (r *portResource) Delete(ctx context.Context, q resource.DeleteRequest, x *resource.DeleteResponse) {
	var s portModel
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	r.delete(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), &x.Diagnostics)
}
func (r *portResource) ImportState(ctx context.Context, q resource.ImportStateRequest, x *resource.ImportStateResponse) {
	importID(ctx, "port", q, x)
}
func portState(v phase2API) portModel {
	requestedMAC, subnetID, requestedIP := types.StringNull(), types.StringNull(), types.StringNull()
	if v.RequestedMAC != "" {
		requestedMAC = types.StringValue(v.RequestedMAC)
	}
	if v.SubnetID != "" {
		subnetID = types.StringValue(v.SubnetID)
	}
	if v.RequestedIP != "" {
		requestedIP = types.StringValue(v.RequestedIP)
	}
	return portModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), NetworkID: types.StringValue(v.NetworkID), Name: types.StringValue(v.Name), MACPolicy: types.StringValue(v.MACPolicy), RequestedMAC: requestedMAC, SubnetID: subnetID, IPAllocationMode: types.StringValue(v.IPAllocationMode), RequestedIP: requestedIP, AttachmentPolicy: types.StringValue(v.AttachmentPolicy), DatapathProfile: types.StringValue(v.DatapathProfile), DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), RealizationState: types.StringValue(v.RealizationState), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}
}

var _ resource.ResourceWithImportState = (*portResource)(nil)
var _ resource.ResourceWithConfigure = (*portResource)(nil)
