package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type volumeResource struct{ phase2Resource }
type volumeModel struct {
	ID                   types.String `tfsdk:"id"`
	ClientReference      types.String `tfsdk:"client_reference"`
	ProjectID            types.String `tfsdk:"project_id"`
	Name                 types.String `tfsdk:"name"`
	SizeBytes            types.Int64  `tfsdk:"size_bytes"`
	StorageClassID       types.String `tfsdk:"storage_class_id"`
	StorageClassRevision types.Int64  `tfsdk:"storage_class_revision"`
	Bootable             types.Bool   `tfsdk:"bootable"`
	SourceType           types.String `tfsdk:"source_type"`
	SourceImageID        types.String `tfsdk:"source_image_id"`
	SourceImageRevision  types.Int64  `tfsdk:"source_image_revision"`
	SourceArtifactDigest types.String `tfsdk:"source_artifact_digest"`
	DeleteProtection     types.Bool   `tfsdk:"delete_protection"`
	Revision             types.Int64  `tfsdk:"revision"`
	RealizationState     types.String `tfsdk:"realization_state"`
	CreatedAt            types.String `tfsdk:"created_at"`
	UpdatedAt            types.String `tfsdk:"updated_at"`
}

func NewVolumeResource() resource.Resource {
	return &volumeResource{phase2Resource: phase2Resource{kind: "volume", plural: "volumes"}}
}
func (r *volumeResource) Metadata(_ context.Context, _ resource.MetadataRequest, x *resource.MetadataResponse) {
	x.TypeName = typeName + "_volume"
}
func (r *volumeResource) Configure(_ context.Context, q resource.ConfigureRequest, x *resource.ConfigureResponse) {
	r.configure(q, x)
}
func (r *volumeResource) Schema(_ context.Context, _ resource.SchemaRequest, x *resource.SchemaResponse) {
	rs := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	ri := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	rb := []planmodifier.Bool{boolplanmodifier.RequiresReplace()}
	x.Schema = schema.Schema{Description: "KIM backend-neutral logical Volume; capacity/backend/LV incarnation is KIM-owned.", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}, "client_reference": schema.StringAttribute{Required: true, WriteOnly: true}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "name": schema.StringAttribute{Required: true}, "size_bytes": schema.Int64Attribute{Required: true, PlanModifiers: ri}, "storage_class_id": schema.StringAttribute{Required: true, PlanModifiers: rs}, "storage_class_revision": schema.Int64Attribute{Required: true, PlanModifiers: ri}, "bootable": schema.BoolAttribute{Required: true, PlanModifiers: rb}, "source_type": schema.StringAttribute{Required: true, PlanModifiers: rs}, "source_image_id": schema.StringAttribute{Optional: true, PlanModifiers: rs}, "source_image_revision": schema.Int64Attribute{Optional: true, PlanModifiers: ri}, "source_artifact_digest": schema.StringAttribute{Optional: true, PlanModifiers: rs}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "realization_state": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}}}
}
func (r *volumeResource) Create(ctx context.Context, q resource.CreateRequest, x *resource.CreateResponse) {
	var m volumeModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	if x.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"projectId": m.ProjectID.ValueString(), "name": m.Name.ValueString(), "sizeBytes": m.SizeBytes.ValueInt64(), "storageClassId": m.StorageClassID.ValueString(), "storageClassRevision": m.StorageClassRevision.ValueInt64(), "bootable": m.Bootable.ValueBool(), "sourceType": m.SourceType.ValueString(), "sourceImageId": m.SourceImageID.ValueString(), "sourceImageRevision": m.SourceImageRevision.ValueInt64(), "sourceArtifactDigest": m.SourceArtifactDigest.ValueString(), "deleteProtection": boolValue(m.DeleteProtection)}
	var out phase2API
	if r.create(ctx, createClientReference(ctx, q.Config, &x.Diagnostics), body, &out, &x.Diagnostics) {
		x.Diagnostics.Append(x.State.Set(ctx, volumeState(out))...)
	}
}
func (r *volumeResource) Read(ctx context.Context, q resource.ReadRequest, x *resource.ReadResponse) {
	var m volumeModel
	x.Diagnostics.Append(q.State.Get(ctx, &m)...)
	var out phase2API
	ok, gone := r.read(ctx, m.ID.ValueString(), &out, &x.Diagnostics)
	if gone {
		x.State.RemoveResource(ctx)
	} else if ok {
		x.Diagnostics.Append(x.State.Set(ctx, volumeState(out))...)
	}
}
func (r *volumeResource) Update(ctx context.Context, q resource.UpdateRequest, x *resource.UpdateResponse) {
	var p, s volumeModel
	x.Diagnostics.Append(q.Plan.Get(ctx, &p)...)
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	var out phase2API
	if r.update(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), map[string]any{"name": p.Name.ValueString(), "deleteProtection": boolValue(p.DeleteProtection)}, &out, &x.Diagnostics) {
		x.Diagnostics.Append(x.State.Set(ctx, volumeState(out))...)
	}
}
func (r *volumeResource) Delete(ctx context.Context, q resource.DeleteRequest, x *resource.DeleteResponse) {
	var s volumeModel
	x.Diagnostics.Append(q.State.Get(ctx, &s)...)
	r.delete(ctx, s.ID.ValueString(), s.Revision.ValueInt64(), &x.Diagnostics)
}
func (r *volumeResource) ImportState(ctx context.Context, q resource.ImportStateRequest, x *resource.ImportStateResponse) {
	importID(ctx, "volume", q, x)
}
func volumeState(v phase2API) volumeModel {
	imageID, artifactDigest := types.StringNull(), types.StringNull()
	imageRevision := types.Int64Null()
	if v.SourceImageID != "" {
		imageID = types.StringValue(v.SourceImageID)
	}
	if v.SourceImageRevision != 0 {
		imageRevision = types.Int64Value(v.SourceImageRevision)
	}
	if v.SourceArtifactDigest != "" {
		artifactDigest = types.StringValue(v.SourceArtifactDigest)
	}
	return volumeModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), Name: types.StringValue(v.Name), SizeBytes: types.Int64Value(v.SizeBytes), StorageClassID: types.StringValue(v.StorageClassID), StorageClassRevision: types.Int64Value(v.StorageClassRevision), Bootable: types.BoolValue(v.Bootable), SourceType: types.StringValue(v.SourceType), SourceImageID: imageID, SourceImageRevision: imageRevision, SourceArtifactDigest: artifactDigest, DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), RealizationState: types.StringValue(v.RealizationState), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt)}
}

var _ resource.ResourceWithImportState = (*volumeResource)(nil)
var _ resource.ResourceWithConfigure = (*volumeResource)(nil)
