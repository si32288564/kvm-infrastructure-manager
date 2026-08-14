package provider

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
	"net/http"
	"regexp"
	"time"
)

type imageResource struct{ client *client.Client }
type imageModel struct {
	ID                      types.String `tfsdk:"id"`
	ProjectID               types.String `tfsdk:"project_id"`
	Name                    types.String `tfsdk:"name"`
	Architecture            types.String `tfsdk:"architecture"`
	Format                  types.String `tfsdk:"format"`
	ExpectedDigest          types.String `tfsdk:"expected_digest"`
	SourceID                types.String `tfsdk:"source_id"`
	Visibility              types.String `tfsdk:"visibility"`
	LifecycleState          types.String `tfsdk:"lifecycle_state"`
	DeleteProtection        types.Bool   `tfsdk:"delete_protection"`
	VerifiedDigest          types.String `tfsdk:"verified_digest"`
	VerifiedSizeBytes       types.Int64  `tfsdk:"verified_size_bytes"`
	VerificationState       types.String `tfsdk:"verification_state"`
	Revision                types.Int64  `tfsdk:"revision"`
	CreatedAt               types.String `tfsdk:"created_at"`
	UpdatedAt               types.String `tfsdk:"updated_at"`
	IngestionTimeoutSeconds types.Int64  `tfsdk:"ingestion_timeout_seconds"`
}
type imageAPI struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"projectId"`
	Name              string  `json:"name"`
	Architecture      string  `json:"architecture"`
	Format            string  `json:"format"`
	ExpectedDigest    string  `json:"expectedDigest"`
	SourceID          string  `json:"sourceId"`
	Visibility        string  `json:"visibility"`
	VerifiedDigest    *string `json:"verifiedDigest"`
	VerifiedSizeBytes *int64  `json:"verifiedSizeBytes"`
	VerificationState string  `json:"verificationState"`
	LifecycleState    string  `json:"lifecycleState"`
	DeleteProtection  bool    `json:"deleteProtection"`
	Revision          int64   `json:"revision"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
}

func NewImageResource() resource.Resource { return &imageResource{} }
func (r *imageResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName + "_image"
}
func (r *imageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Experimental KIM Image. Apply completes only after the separate ingestion Operation is VERIFIED.", Attributes: map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}, "project_id": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}, "name": schema.StringAttribute{Required: true}, "architecture": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("X86_64", "AARCH64")}}, "format": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("RAW", "QCOW2")}}, "expected_digest": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-f]{64}$`), "must be a lowercase SHA-256 digest")}}, "source_id": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`), "must be a KIM source identifier")}}, "visibility": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("PRIVATE", "SHARED", "PUBLIC")}}, "lifecycle_state": schema.StringAttribute{Optional: true, Computed: true, Validators: []validator.String{stringvalidator.OneOf("ACTIVE", "DEPRECATED")}}, "delete_protection": schema.BoolAttribute{Optional: true, Computed: true}, "verified_digest": schema.StringAttribute{Computed: true}, "verified_size_bytes": schema.Int64Attribute{Computed: true}, "verification_state": schema.StringAttribute{Computed: true}, "revision": schema.Int64Attribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}, "ingestion_timeout_seconds": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.AtLeast(1)}, Description: "Provider-only maximum wait for the KIM Operation; default 7200."}}}
}
func (r *imageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, resp, &r.client)
}
func imageBody(v imageModel) map[string]any {
	return map[string]any{"projectId": v.ProjectID.ValueString(), "name": v.Name.ValueString(), "architecture": v.Architecture.ValueString(), "format": v.Format.ValueString(), "expectedDigest": v.ExpectedDigest.ValueString(), "sourceId": v.SourceID.ValueString(), "visibility": v.Visibility.ValueString(), "lifecycleState": stringValue(v.LifecycleState, "ACTIVE"), "deleteProtection": boolValue(v.DeleteProtection)}
}
func imagePatchBody(v imageModel) map[string]any {
	b := imageBody(v)
	delete(b, "projectId")
	return b
}
func (r *imageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var p imageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &p)...)
	body := imageBody(p)
	delete(body, "lifecycleState")
	key, _ := client.CreateIdempotencyKey("image", body)
	var out imageAPI
	response, err := r.client.Do(ctx, http.MethodPost, "/images", body, &out, map[string]string{"Idempotency-Key": key})
	if err != nil {
		addError(&resp.Diagnostics, "Create Image", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	// Preserve the committed persistent Image identity even when the separate
	// ingestion Operation times out or fails. Terraform must never reinterpret
	// an Operation diagnostic as proof that metadata was rolled back.
	resp.Diagnostics.Append(resp.State.Set(ctx, imageState(out, p.IngestionTimeoutSeconds))...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ingest(ctx, out, p.IngestionTimeoutSeconds); err != nil {
		addError(&resp.Diagnostics, "Ingest Image", err)
		return
	}
	r.readIntoState(ctx, out.ID, p.IngestionTimeoutSeconds, &resp.State, &resp.Diagnostics)
}
func (r *imageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var s imageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out imageAPI
	response, err := r.client.Do(ctx, http.MethodGet, "/images/"+s.ID.ValueString(), nil, &out, nil)
	if notFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addError(&resp.Diagnostics, "Read Image", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, imageState(out, s.IngestionTimeoutSeconds))...)
}
func (r *imageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var p, s imageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &p)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	var out imageAPI
	response, err := r.client.Do(ctx, http.MethodPatch, "/images/"+s.ID.ValueString(), imagePatchBody(p), &out, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", s.Revision.ValueInt64())})
	if err != nil {
		addError(&resp.Diagnostics, "Update Image", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	if out.VerificationState != "VERIFIED" {
		if err := r.ingest(ctx, out, p.IngestionTimeoutSeconds); err != nil {
			addError(&resp.Diagnostics, "Ingest Image revision", err)
			return
		}
	}
	r.readIntoState(ctx, out.ID, p.IngestionTimeoutSeconds, &resp.State, &resp.Diagnostics)
}
func (r *imageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var s imageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &s)...)
	_, err := r.client.Do(ctx, http.MethodDelete, "/images/"+s.ID.ValueString(), nil, nil, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", s.Revision.ValueInt64())})
	if !notFound(err) && err != nil {
		addError(&resp.Diagnostics, "Delete Image", err)
	}
}
func (r *imageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, "image", req, resp)
}
func (r *imageResource) ingest(ctx context.Context, v imageAPI, timeout types.Int64) error {
	key, _ := client.IdempotencyKey("image-ingestion", map[string]any{"id": v.ID, "revision": v.Revision, "expectedDigest": v.ExpectedDigest})
	var op client.Operation
	_, err := r.client.Do(ctx, http.MethodPost, "/images/"+v.ID+"/ingestions", map[string]any{}, &op, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", v.Revision), "Idempotency-Key": key})
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(intValue(timeout, 7200))*time.Second)
	defer cancel()
	_, err = r.client.WaitOperation(waitCtx, op.ID)
	return err
}
func (r *imageResource) readIntoState(ctx context.Context, id string, timeout types.Int64, state *tfsdk.State, d *diag.Diagnostics) {
	var out imageAPI
	response, err := r.client.Do(ctx, http.MethodGet, "/images/"+id, nil, &out, nil)
	if err != nil {
		addError(d, "Read verified Image", err)
		return
	}
	out.Revision = client.RevisionFromETag(response.ETag, out.Revision)
	d.Append(state.Set(ctx, imageState(out, timeout))...)
}
func imageState(v imageAPI, timeout types.Int64) imageModel {
	m := imageModel{ID: types.StringValue(v.ID), ProjectID: types.StringValue(v.ProjectID), Name: types.StringValue(v.Name), Architecture: types.StringValue(v.Architecture), Format: types.StringValue(v.Format), ExpectedDigest: types.StringValue(v.ExpectedDigest), SourceID: types.StringValue(v.SourceID), Visibility: types.StringValue(v.Visibility), VerificationState: types.StringValue(v.VerificationState), LifecycleState: types.StringValue(v.LifecycleState), DeleteProtection: types.BoolValue(v.DeleteProtection), Revision: types.Int64Value(v.Revision), CreatedAt: types.StringValue(v.CreatedAt), UpdatedAt: types.StringValue(v.UpdatedAt), VerifiedDigest: types.StringNull(), VerifiedSizeBytes: types.Int64Null(), IngestionTimeoutSeconds: timeout}
	if v.VerifiedDigest != nil {
		m.VerifiedDigest = types.StringValue(*v.VerifiedDigest)
	}
	if v.VerifiedSizeBytes != nil {
		m.VerifiedSizeBytes = types.Int64Value(*v.VerifiedSizeBytes)
	}
	return m
}

var _ resource.ResourceWithImportState = (*imageResource)(nil)
var _ resource.ResourceWithConfigure = (*imageResource)(nil)
