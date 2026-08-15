package provider

import (
	"context"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
)

const typeName = "kim"

type kimProvider struct{ version string }
type providerModel struct {
	Endpoint                     types.String `tfsdk:"endpoint"`
	Token                        types.String `tfsdk:"token"`
	ClientID                     types.String `tfsdk:"client_id"`
	CACertificate                types.String `tfsdk:"ca_certificate"`
	InsecureSkipVerify           types.Bool   `tfsdk:"insecure_skip_verify"`
	RequestTimeoutSeconds        types.Int64  `tfsdk:"request_timeout_seconds"`
	OperationPollIntervalSeconds types.Int64  `tfsdk:"operation_poll_interval_seconds"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &kimProvider{version: version} }
}
func (p *kimProvider) Metadata(_ context.Context, _ provider.MetadataRequest, r *provider.MetadataResponse) {
	r.TypeName = typeName
	r.Version = p.version
}
func (p *kimProvider) Schema(_ context.Context, _ provider.SchemaRequest, r *provider.SchemaResponse) {
	r.Schema = schema.Schema{Description: "Experimental KIM Northbound API provider. Terraform state is not KIM authority.", Attributes: map[string]schema.Attribute{
		"endpoint":                        schema.StringAttribute{Optional: true, Description: "KIM controller-side Northbound API /api/v1 endpoint. KIM_ENDPOINT may be used."},
		"token":                           schema.StringAttribute{Optional: true, Sensitive: true, Description: "Externally issued Bearer token. KIM_TOKEN may be used and the value is not resource state."},
		"client_id":                       schema.StringAttribute{Optional: true, Description: "Stable Northbound automation-client identity used with per-resource client_reference for crash-safe Create. KIM_CLIENT_ID may be used."},
		"ca_certificate":                  schema.StringAttribute{Optional: true, Sensitive: true, Description: "Optional PEM trust anchor. KIM_CA_CERTIFICATE may be used."},
		"insecure_skip_verify":            schema.BoolAttribute{Optional: true, Description: "Development-only TLS verification bypass."},
		"request_timeout_seconds":         schema.Int64Attribute{Optional: true, Description: "Per-request timeout."},
		"operation_poll_interval_seconds": schema.Int64Attribute{Optional: true, Description: "Bounded asynchronous Operation poll interval."},
	}}
}
func (p *kimProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint := stringValue(m.Endpoint, os.Getenv("KIM_ENDPOINT"))
	token := stringValue(m.Token, os.Getenv("KIM_TOKEN"))
	clientID := stringValue(m.ClientID, os.Getenv("KIM_CLIENT_ID"))
	ca := stringValue(m.CACertificate, os.Getenv("KIM_CA_CERTIFICATE"))
	timeout := intValue(m.RequestTimeoutSeconds, 30)
	poll := intValue(m.OperationPollIntervalSeconds, 1)
	if endpoint == "" || token == "" || clientID == "" || timeout < 1 || poll < 1 {
		resp.Diagnostics.AddError("Invalid KIM provider configuration", "endpoint/token/client_id and positive request/poll timeouts are required; use provider attributes or KIM_ENDPOINT/KIM_TOKEN/KIM_CLIENT_ID.")
		return
	}
	c, err := client.New(client.Config{Endpoint: endpoint, Token: token, ClientID: clientID, CACertificate: ca, InsecureSkipVerify: !m.InsecureSkipVerify.IsNull() && m.InsecureSkipVerify.ValueBool(), Timeout: time.Duration(timeout) * time.Second, PollInterval: time.Duration(poll) * time.Second})
	if err != nil {
		resp.Diagnostics.AddError("Invalid KIM provider configuration", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}
func (p *kimProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewProjectResource, NewFlavorResource, NewAvailabilityPolicyResource, NewImageResource, NewNetworkResource, NewSubnetResource, NewPortResource, NewVolumeResource, NewVMResource}
}
func (p *kimProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }
func stringValue(v types.String, fallback string) string {
	if v.IsNull() || v.IsUnknown() {
		return fallback
	}
	return v.ValueString()
}
func intValue(v types.Int64, fallback int64) int64 {
	if v.IsNull() || v.IsUnknown() {
		return fallback
	}
	return v.ValueInt64()
}

var _ provider.Provider = (*kimProvider)(nil)
