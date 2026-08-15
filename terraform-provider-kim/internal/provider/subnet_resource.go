package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type subnetResource struct{ phase2Resource }
type subnetModel struct {
	ID                types.String `tfsdk:"id"`
	ClientReference   types.String `tfsdk:"client_reference"`
	ProjectID         types.String `tfsdk:"project_id"`
	NetworkID         types.String `tfsdk:"network_id"`
	Name              types.String `tfsdk:"name"`
	IPFamily          types.String `tfsdk:"ip_family"`
	CIDR              types.String `tfsdk:"cidr"`
	GatewayPolicy     types.String `tfsdk:"gateway_policy"`
	GatewayAddress    types.String `tfsdk:"gateway_address"`
	AllocationPolicy  types.String `tfsdk:"allocation_policy"`
	AllocationStart   types.String `tfsdk:"allocation_start"`
	AllocationEnd     types.String `tfsdk:"allocation_end"`
	ReservedAddresses types.List   `tfsdk:"reserved_addresses"`
	DHCPEnabled       types.Bool   `tfsdk:"dhcp_enabled"`
	DNSServers        types.List   `tfsdk:"dns_servers"`
	DeleteProtection  types.Bool   `tfsdk:"delete_protection"`
	Revision          types.Int64  `tfsdk:"revision"`
	RealizationState  types.String `tfsdk:"realization_state"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

func NewSubnetResource() resource.Resource {
	return &subnetResource{phase2Resource: phase2Resource{kind: "subnet", plural: "subnets"}}
}
func (r *subnetResource) Metadata(_ context.Context, _ resource.MetadataRequest, x *resource.MetadataResponse) {
	x.TypeName = typeName + "_subnet"
}
func (r *subnetResource) Configure(_ context.Context, q resource.ConfigureRequest, x *resource.ConfigureResponse) {
	r.configure(q, x)
}
func (r *subnetResource) Schema(_ context.Context, _ resource.SchemaRequest, x *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	x.Schema = schema.Schema{Description: "KIM logical IPv4 Subnet and IPAM policy.", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}, "client_reference": schema.StringAttribute{Required: true, WriteOnly: true}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: replace}, "network_id": schema.StringAttribute{Required: true, PlanModifiers: replace}, "name": schema.StringAttribute{Required: true}, "ip_family": schema.StringAttribute{Required: true, PlanModifiers: replace}, "cidr": schema.StringAttribute{Required: true, PlanModifiers: replace}, "gateway_policy": schema.StringAttribute{Required: true, PlanModifiers: replace}, "gateway_address": schema.StringAttribute{Optional: true, PlanModifiers: replace}, "allocation_policy": schema.StringAttribute{Required: true, PlanModifiers: replace}, "allocation_start": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replace}, "allocation_end": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replace}, "reserved_addresses": schema.ListAttribute{Optional: true, ElementType: types.StringType}, "dhcp_enabled": schema.BoolAttribute{Required: true}, "dns_servers": schema.ListAttribute{Optional: true, ElementType: types.StringType}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "realization_state": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
}
func (r *subnetResource) Create(ctx context.Context, q resource.CreateRequest, x *resource.CreateResponse) {
	var m subnetModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	var reserved, dns []string
	x.Diagnostics.Append(m.ReservedAddresses.ElementsAs(ctx, &reserved, false)...)
	x.Diagnostics.Append(m.DNSServers.ElementsAs(ctx, &dns, false)...)
	if x.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"projectId": m.ProjectID.ValueString(), "networkId": m.NetworkID.ValueString(), "name": m.Name.ValueString(), "ipFamily": m.IPFamily.ValueString(), "cidr": m.CIDR.ValueString(), "gatewayPolicy": m.GatewayPolicy.ValueString(), "gatewayAddress": m.GatewayAddress.ValueString(), "allocationPolicy": m.AllocationPolicy.ValueString(), "allocationStart": m.AllocationStart.ValueString(), "allocationEnd": m.AllocationEnd.ValueString(), "reservedAddresses": reserved, "dhcpEnabled": m.DHCPEnabled.ValueBool(), "dnsServers": dns, "deleteProtection": boolValue(m.DeleteProtection)}
	var out phase2API
	if r.create(ctx, createClientReference(ctx, q.Config, &x.Diagnostics), body, &out, &x.Diagnostics) {
		state, d := subnetState(ctx, out)
		x.Diagnostics.Append(d...)
		x.Diagnostics.Append(x.State.Set(ctx, state)...)
	}
}
func (r *subnetResource) Read(ctx context.Context, q resource.ReadRequest, x *resource.ReadResponse) {
	var m subnetModel
	x.Diagnostics.Append(q.State.Get(ctx, &m)...)
	var out phase2API
	ok, gone := r.read(ctx, m.ID.ValueString(), &out, &x.Diagnostics)
	if gone {
		x.State.RemoveResource(ctx)
	} else if ok {
		state, d := subnetState(ctx, out)
		x.Diagnostics.Append(d...)
		x.Diagnostics.Append(x.State.Set(ctx, state)...)
	}
}
func (r *subnetResource) Update(ctx context.Context, q resource.UpdateRequest, x *resource.UpdateResponse) {
	var p, s subnetModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &p)...)
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	var out phase2API
	if r.update(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), map[string]any{"name": p.Name.ValueString(), "deleteProtection": boolValue(p.DeleteProtection)}, &out, &x.Diagnostics) {
		state, d := subnetState(ctx, out)
		x.Diagnostics.Append(d...)
		x.Diagnostics.Append(x.State.Set(ctx, state)...)
	}
}
func (r *subnetResource) Delete(ctx context.Context, q resource.DeleteRequest, x *resource.DeleteResponse) {
	var s subnetModel
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	r.delete(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), &x.Diagnostics)
}
func (r *subnetResource) ImportState(ctx context.Context, q resource.ImportStateRequest, x *resource.ImportStateResponse) {
	importID(ctx, "subnet", q, x)
}
func subnetState(ctx context.Context, v phase2API) (subnetModel, diag.Diagnostics) {
	reserved, d1 := types.ListValueFrom(ctx, types.StringType, v.ReservedAddresses)
	dns, d2 := types.ListValueFrom(ctx, types.StringType, v.DNSServers)
	d1.Append(d2...)
	return subnetModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), NetworkID: types.StringValue(v.NetworkID), Name: types.StringValue(v.Name), IPFamily: types.StringValue(v.IPFamily), CIDR: types.StringValue(v.CIDR), GatewayPolicy: types.StringValue(v.GatewayPolicy), GatewayAddress: types.StringValue(v.GatewayAddress), AllocationPolicy: types.StringValue(v.AllocationPolicy), AllocationStart: types.StringValue(v.AllocationStart), AllocationEnd: types.StringValue(v.AllocationEnd), ReservedAddresses: reserved, DHCPEnabled: types.BoolValue(v.DHCPEnabled), DNSServers: dns, DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), RealizationState: types.StringValue(v.RealizationState), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}, d1
}

var _ resource.ResourceWithImportState = (*subnetResource)(nil)
var _ resource.ResourceWithConfigure = (*subnetResource)(nil)
