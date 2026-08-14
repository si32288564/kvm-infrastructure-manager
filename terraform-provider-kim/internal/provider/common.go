package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/client"
)

var importUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func configureClient(req resource.ConfigureRequest, resp *resource.ConfigureResponse, target **client.Client) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider configuration", fmt.Sprintf("expected KIM client, got %T", req.ProviderData))
		return
	}
	*target = c
}
func addError(d *diag.Diagnostics, action string, err error) {
	var p *client.Problem
	if errors.As(err, &p) {
		d.AddError(action, fmt.Sprintf("KIM code=%s status=%d request_id=%s retryable=%t: %s", p.Code, p.Status, p.RequestID, p.Retryable, p.Detail))
		return
	}
	d.AddError(action, err.Error())
}
func notFound(err error) bool {
	var p *client.Problem
	return errors.As(err, &p) && p.Code == "RESOURCE_NOT_FOUND"
}
func importID(ctx context.Context, prefix string, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] != prefix || !importUUIDPattern.MatchString(parts[1]) {
		resp.Diagnostics.AddError("Invalid import identifier", fmt.Sprintf("expected %s/<uuid>", prefix))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
func boolValue(v types.Bool) bool { return !v.IsNull() && !v.IsUnknown() && v.ValueBool() }
func optionalInt(v types.Int64) any {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueInt64()
}
