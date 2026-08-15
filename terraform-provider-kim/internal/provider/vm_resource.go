package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
)

type vmResource struct{ client *client.Client }
type vmReferenceModel struct {
	ID       types.String `tfsdk:"id"`
	Revision types.Int64  `tfsdk:"revision"`
}
type vmModel struct {
	ID                         types.String `tfsdk:"id"`
	ClientReference            types.String `tfsdk:"client_reference"`
	ProjectID                  types.String `tfsdk:"project_id"`
	Name                       types.String `tfsdk:"name"`
	FlavorID                   types.String `tfsdk:"flavor_id"`
	FlavorRevision             types.Int64  `tfsdk:"flavor_revision"`
	ImageID                    types.String `tfsdk:"image_id"`
	ImageRevision              types.Int64  `tfsdk:"image_revision"`
	AvailabilityPolicyID       types.String `tfsdk:"availability_policy_id"`
	AvailabilityPolicyRevision types.Int64  `tfsdk:"availability_policy_revision"`
	PlacementScopeID           types.String `tfsdk:"placement_scope_id"`
	PlacementScopeGeneration   types.Int64  `tfsdk:"placement_scope_generation"`
	RootVolume                 types.Object `tfsdk:"root_volume"`
	Ports                      types.Set    `tfsdk:"ports"`
	DataVolumes                types.Set    `tfsdk:"data_volumes"`
	DesiredPowerState          types.String `tfsdk:"desired_power_state"`
	DeleteProtection           types.Bool   `tfsdk:"delete_protection"`
	Revision                   types.Int64  `tfsdk:"revision"`
	RuntimeIntentGeneration    types.Int64  `tfsdk:"runtime_intent_generation"`
	LifecycleState             types.String `tfsdk:"lifecycle_state"`
	ConvergenceState           types.String `tfsdk:"convergence_state"`
	CreatedAt                  types.String `tfsdk:"created_at"`
	UpdatedAt                  types.String `tfsdk:"updated_at"`
}
type vmReferenceAPI struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type vmAPI struct {
	ID                         string           `json:"id"`
	ProjectID                  string           `json:"projectId"`
	Name                       string           `json:"name"`
	FlavorID                   string           `json:"flavorId"`
	FlavorRevision             int64            `json:"flavorRevision"`
	ImageID                    string           `json:"imageId"`
	ImageRevision              int64            `json:"imageRevision"`
	AvailabilityPolicyID       string           `json:"availabilityPolicyId"`
	AvailabilityPolicyRevision int64            `json:"availabilityPolicyRevision"`
	PlacementScopeID           string           `json:"placementScopeId"`
	PlacementScopeGeneration   int64            `json:"placementScopeGeneration"`
	RootVolume                 vmReferenceAPI   `json:"rootVolume"`
	Ports                      []vmReferenceAPI `json:"ports"`
	DataVolumes                []vmReferenceAPI `json:"dataVolumes"`
	DesiredPowerState          string           `json:"desiredPowerState"`
	DeleteProtection           bool             `json:"deleteProtection"`
	Revision                   int64            `json:"revision"`
	RuntimeIntentGeneration    int64            `json:"runtimeIntentGeneration"`
	LifecycleState             string           `json:"lifecycleState"`
	ConvergenceState           string           `json:"convergenceState"`
	OperationID                string           `json:"operationId"`
	CreatedAt                  string           `json:"createdAt"`
	UpdatedAt                  string           `json:"updatedAt"`
}

var vmReferenceTypes = map[string]attr.Type{"id": types.StringType, "revision": types.Int64Type}

func NewVMResource() resource.Resource { return &vmResource{} }
func (r *vmResource) Metadata(_ context.Context, _ resource.MetadataRequest, x *resource.MetadataResponse) {
	x.TypeName = typeName + "_vm"
}
func (r *vmResource) Configure(_ context.Context, q resource.ConfigureRequest, x *resource.ConfigureResponse) {
	configureClient(q, x, &r.client)
}
func (r *vmResource) Schema(_ context.Context, _ resource.SchemaRequest, x *resource.SchemaResponse) {
	rs := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	ri := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	reference := schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Required: true}, "revision": schema.Int64Attribute{Required: true}}}
	x.Schema = schema.Schema{Description: "KIM logical VM aggregate. Placement, Host, Port binding, backend and mobility incarnations are KIM-owned.", Attributes: map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true}, "client_reference": schema.StringAttribute{Required: true, WriteOnly: true}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "name": schema.StringAttribute{Required: true}, "flavor_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "flavor_revision": schema.Int64Attribute{Required: true, PlanModifiers: ri}, "image_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "image_revision": schema.Int64Attribute{Required: true, PlanModifiers: ri}, "availability_policy_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "availability_policy_revision": schema.Int64Attribute{Required: true, PlanModifiers: ri}, "placement_scope_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "placement_scope_generation": schema.Int64Attribute{Required: true, PlanModifiers: ri}, "root_volume": schema.SingleNestedAttribute{Required: true, Attributes: reference.Attributes, PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()}}, "ports": schema.SetNestedAttribute{Required: true, NestedObject: reference, PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()}}, "data_volumes": schema.SetNestedAttribute{Required: true, NestedObject: reference, PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()}}, "desired_power_state": schema.StringAttribute{Required: true}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "runtime_intent_generation": schema.Int64Attribute{Computed: true}, "lifecycle_state": schema.StringAttribute{Computed: true}, "convergence_state": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true},
	}}
}
func decodeRefs(ctx context.Context, v types.Set, diagnostics interface{ AddError(string, string) }) []vmReferenceAPI {
	var refs []vmReferenceModel
	d := v.ElementsAs(ctx, &refs, false)
	if d.HasError() {
		diagnostics.AddError("Invalid VM dependency set", d.Errors()[0].Detail())
		return nil
	}
	out := make([]vmReferenceAPI, len(refs))
	for i, r := range refs {
		out[i] = vmReferenceAPI{ID: r.ID.ValueString(), Revision: r.Revision.ValueInt64()}
	}
	return out
}
func rootFromObject(ctx context.Context, v types.Object, diagnostics interface{ AddError(string, string) }) vmReferenceAPI {
	var r vmReferenceModel
	d := v.As(ctx, &r, struct{ UnhandledNullAsEmpty, UnhandledUnknownAsEmpty bool }{true, true})
	if d.HasError() {
		diagnostics.AddError("Invalid VM root Volume", d.Errors()[0].Detail())
	}
	return vmReferenceAPI{ID: r.ID.ValueString(), Revision: r.Revision.ValueInt64()}
}
func vmBody(ctx context.Context, m vmModel, diagnostics interface{ AddError(string, string) }) map[string]any {
	return map[string]any{"projectId": m.ProjectID.ValueString(), "name": m.Name.ValueString(), "flavorId": m.FlavorID.ValueString(), "flavorRevision": m.FlavorRevision.ValueInt64(), "imageId": m.ImageID.ValueString(), "imageRevision": m.ImageRevision.ValueInt64(), "availabilityPolicyId": m.AvailabilityPolicyID.ValueString(), "availabilityPolicyRevision": m.AvailabilityPolicyRevision.ValueInt64(), "placementScopeId": m.PlacementScopeID.ValueString(), "placementScopeGeneration": m.PlacementScopeGeneration.ValueInt64(), "rootVolume": rootFromObject(ctx, m.RootVolume, diagnostics), "ports": decodeRefs(ctx, m.Ports, diagnostics), "dataVolumes": decodeRefs(ctx, m.DataVolumes, diagnostics), "desiredPowerState": m.DesiredPowerState.ValueString(), "deleteProtection": boolValue(m.DeleteProtection)}
}
func (r *vmResource) Create(ctx context.Context, q resource.CreateRequest, x *resource.CreateResponse) {
	var m vmModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	if x.Diagnostics.HasError() {
		return
	}
	body := vmBody(ctx, m, &x.Diagnostics)
	key, err := r.client.CreateIdempotencyKey("vm", createClientReference(ctx, q.Config, &x.Diagnostics), body)
	if err != nil {
		addError(&x.Diagnostics, "Create VM", err)
		return
	}
	var out vmAPI
	response, err := r.client.Do(ctx, http.MethodPost, "/vms", body, &out, map[string]string{"Idempotency-Key": key})
	if err != nil {
		addError(&x.Diagnostics, "Create VM", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	if !r.wait(ctx, &out, &x.Diagnostics) {
		return
	}
	x.Diagnostics.Append(x.State.Set(ctx, vmState(ctx, out, &x.Diagnostics))...)
}
func (r *vmResource) Read(ctx context.Context, q resource.ReadRequest, x *resource.ReadResponse) {
	var m vmModel
	x.Diagnostics.Append(q.State.Get(ctx, &m)...)
	var out vmAPI
	response, err := r.client.Do(ctx, http.MethodGet, "/vms/"+m.ID.ValueString(), nil, &out, nil)
	if notFound(err) {
		x.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addError(&x.Diagnostics, "Read VM", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	x.Diagnostics.Append(x.State.Set(ctx, vmState(ctx, out, &x.Diagnostics))...)
}
func (r *vmResource) Update(ctx context.Context, q resource.UpdateRequest, x *resource.UpdateResponse) {
	var p, s vmModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &p)...)
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	if x.Diagnostics.HasError() {
		return
	}
	revision := s.Revision.ValueInt64()
	var out vmAPI
	if p.Name.ValueString() != s.Name.ValueString() || boolValue(p.DeleteProtection) != boolValue(s.DeleteProtection) {
		response, err := r.client.Do(ctx, http.MethodPatch, "/vms/"+s.ID.ValueString(), map[string]any{"name": p.Name.ValueString(), "deleteProtection": boolValue(p.DeleteProtection)}, &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", revision)})
		if err != nil {
			addError(&x.Diagnostics, "Update VM metadata", err)
			return
		}
		revision = client.RevisionFromETag(response.ETag, out.Revision)
	}
	if p.DesiredPowerState.ValueString() != s.DesiredPowerState.ValueString() {
		response, err := r.client.Do(ctx, http.MethodPatch, "/vms/"+s.ID.ValueString(), map[string]any{"desiredPowerState": p.DesiredPowerState.ValueString()}, &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", revision)})
		if err != nil {
			addError(&x.Diagnostics, "Update VM power", err)
			return
		}
		out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
		if !r.wait(ctx, &out, &x.Diagnostics) {
			return
		}
	} else if out.ID == "" {
		response, err := r.client.Do(ctx, http.MethodGet, "/vms/"+s.ID.ValueString(), nil, &out, nil)
		if err != nil {
			addError(&x.Diagnostics, "Refresh VM", err)
			return
		}
		out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	}
	x.Diagnostics.Append(x.State.Set(ctx, vmState(ctx, out, &x.Diagnostics))...)
}
func (r *vmResource) Delete(ctx context.Context, q resource.DeleteRequest, x *resource.DeleteResponse) {
	var s vmModel
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	if x.Diagnostics.HasError() {
		return
	}
	revision := s.Revision.ValueInt64()
	if s.DesiredPowerState.ValueString() != "SHUTOFF" {
		var out vmAPI
		response, err := r.client.Do(ctx, http.MethodPatch, "/vms/"+s.ID.ValueString(), map[string]any{"desiredPowerState": "SHUTOFF"}, &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", revision)})
		if err != nil {
			addError(&x.Diagnostics, "Quiesce VM for delete", err)
			return
		}
		out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
		if !r.wait(ctx, &out, &x.Diagnostics) {
			return
		}
		revision = out.Revision
	}
	var op client.Operation
	_, err := r.client.Do(ctx, http.MethodDelete, "/vms/"+s.ID.ValueString(), nil, &op, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", revision)})
	if notFound(err) {
		return
	}
	if err != nil {
		addError(&x.Diagnostics, "Delete VM", err)
		return
	}
	if _, err = r.client.WaitOperation(ctx, op.ID); err != nil {
		addError(&x.Diagnostics, "Delete VM", err)
	}
}
func (r *vmResource) ImportState(ctx context.Context, q resource.ImportStateRequest, x *resource.ImportStateResponse) {
	importID(ctx, "vm", q, x)
}
func (r *vmResource) wait(ctx context.Context, out *vmAPI, diagnostics interface{ AddError(string, string) }) bool {
	if out.OperationID != "" {
		if _, err := r.client.WaitOperation(ctx, out.OperationID); err != nil {
			diagnostics.AddError("VM convergence", err.Error())
			return false
		}
		response, err := r.client.Do(ctx, http.MethodGet, "/vms/"+out.ID, nil, out, nil)
		if err != nil {
			diagnostics.AddError("Refresh VM", err.Error())
			return false
		}
		out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	}
	return true
}
func vmState(ctx context.Context, v vmAPI, diagnostics interface{ AddError(string, string) }) vmModel {
	root, _ := types.ObjectValue(vmReferenceTypes, map[string]attr.Value{"id": types.StringValue(v.RootVolume.ID), "revision": types.Int64Value(v.RootVolume.Revision)})
	toSet := func(in []vmReferenceAPI) types.Set {
		values := make([]attr.Value, len(in))
		for i, r := range in {
			o, _ := types.ObjectValue(vmReferenceTypes, map[string]attr.Value{"id": types.StringValue(r.ID), "revision": types.Int64Value(r.Revision)})
			values[i] = o
		}
		s, d := types.SetValue(types.ObjectType{AttrTypes: vmReferenceTypes}, values)
		if d.HasError() {
			diagnostics.AddError("Encode VM dependencies", d.Errors()[0].Detail())
		}
		return s
	}
	return vmModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), Name: types.StringValue(v.Name), FlavorID: types.StringValue(v.FlavorID), FlavorRevision: types.Int64Value(v.FlavorRevision), ImageID: types.StringValue(v.ImageID), ImageRevision: types.Int64Value(v.ImageRevision), AvailabilityPolicyID: types.StringValue(v.AvailabilityPolicyID), AvailabilityPolicyRevision: types.Int64Value(v.AvailabilityPolicyRevision), PlacementScopeID: types.StringValue(v.PlacementScopeID), PlacementScopeGeneration: types.Int64Value(v.PlacementScopeGeneration), RootVolume: root, Ports: toSet(v.Ports), DataVolumes: toSet(v.DataVolumes), DesiredPowerState: types.StringValue(v.DesiredPowerState), DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), RuntimeIntentGeneration: types.Int64Value(v.RuntimeIntentGeneration), LifecycleState: types.StringValue(v.LifecycleState), ConvergenceState: types.StringValue(v.ConvergenceState), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}
}

var _ resource.ResourceWithImportState = (*vmResource)(nil)
var _ resource.ResourceWithConfigure = (*vmResource)(nil)
