package provider

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestResourceSchemasMatchNorthboundContract(t *testing.T) {
	tests := []struct {
		name     string
		resource resource.Resource
		required []string
		optional []string
		computed []string
	}{
		{"kim_project", NewProjectResource(), []string{"client_reference", "name"}, []string{"delete_protection"}, []string{"id", "revision", "created_at", "updated_at"}},
		{"kim_flavor", NewFlavorResource(), []string{"client_reference", "project_id", "name", "vcpus", "memory_mib", "root_disk_gib", "numa_policy", "cpu_allocation", "cpu_pinning"}, []string{"numa_nodes", "huge_page_size_kib", "delete_protection"}, []string{"id", "revision", "created_at", "updated_at"}},
		{"kim_availability_policy", NewAvailabilityPolicyResource(), []string{"client_reference", "name", "availability_mode", "max_attempts"}, []string{"delete_protection"}, []string{"id", "revision", "created_at", "updated_at"}},
		{"kim_image", NewImageResource(), []string{"client_reference", "project_id", "name", "architecture", "format", "expected_digest", "source_id", "visibility"}, []string{"lifecycle_state", "delete_protection", "ingestion_timeout_seconds"}, []string{"id", "verified_digest", "verified_size_bytes", "verification_state", "revision", "created_at", "updated_at"}},
		{"kim_network", NewNetworkResource(), []string{"client_reference", "project_id", "name", "profile", "mtu", "segment_policy"}, []string{"requested_segment_id", "delete_protection"}, []string{"id", "revision", "realization_state", "created_at", "updated_at"}},
		{"kim_subnet", NewSubnetResource(), []string{"client_reference", "project_id", "network_id", "name", "ip_family", "cidr", "gateway_policy", "allocation_policy", "dhcp_enabled"}, []string{"gateway_address", "allocation_start", "allocation_end", "reserved_addresses", "dns_servers", "delete_protection"}, []string{"id", "revision", "realization_state", "created_at", "updated_at"}},
		{"kim_port", NewPortResource(), []string{"client_reference", "project_id", "network_id", "name", "mac_policy", "ip_allocation_mode", "attachment_policy", "datapath_profile"}, []string{"requested_mac", "subnet_id", "requested_ip", "delete_protection"}, []string{"id", "revision", "realization_state", "created_at", "updated_at"}},
		{"kim_volume", NewVolumeResource(), []string{"client_reference", "project_id", "name", "size_bytes", "storage_class_id", "storage_class_revision", "bootable", "source_type"}, []string{"source_image_id", "source_image_revision", "source_artifact_digest", "delete_protection"}, []string{"id", "revision", "realization_state", "created_at", "updated_at"}},
		{"kim_vm", NewVMResource(), []string{"client_reference", "project_id", "name", "flavor_id", "flavor_revision", "image_id", "image_revision", "availability_policy_id", "availability_policy_revision", "placement_scope_id", "placement_scope_generation", "root_volume", "ports", "data_volumes", "desired_power_state"}, []string{"delete_protection"}, []string{"id", "revision", "runtime_intent_generation", "lifecycle_state", "convergence_state", "created_at", "updated_at"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response resource.SchemaResponse
			test.resource.Schema(context.Background(), resource.SchemaRequest{}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			for _, name := range test.required {
				if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsRequired() {
					t.Errorf("%s must be required", name)
				}
			}
			if attribute := response.Schema.Attributes["client_reference"]; attribute == nil || !attribute.IsWriteOnly() {
				t.Error("client_reference must be write-only")
			}
			for _, name := range test.optional {
				if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsOptional() {
					t.Errorf("%s must be optional", name)
				}
			}
			for _, name := range test.computed {
				if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsComputed() {
					t.Errorf("%s must be computed", name)
				}
			}
			for name := range response.Schema.Attributes {
				lower := strings.ToLower(name)
				for _, forbidden := range []string{"host", "pcpu", "pmd", "rxq", "socket", "bdf", "lv_uuid", "agent", "recovery", "evacuate", "operation_id", "attempt_id"} {
					if strings.Contains(lower, forbidden) {
						t.Errorf("physical/internal field leaked: %s", name)
					}
				}
			}
		})
	}
}

func TestOpenAPIImportFormatsAndImageRevisionPatch(t *testing.T) {
	raw, err := os.ReadFile("../../../api/openapi/kim-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	wantImports := map[string]string{"Project": "project/<uuid>", "Flavor": "flavor/<uuid>", "AvailabilityPolicy": "availability-policy/<uuid>", "Image": "image/<uuid>", "Network": "network/<uuid>", "Subnet": "subnet/<uuid>", "Port": "port/<uuid>", "Volume": "volume/<uuid>", "VM": "vm/<uuid>"}
	for name, want := range wantImports {
		resourceContract := schemas[name].(map[string]any)["x-kim-resource"].(map[string]any)
		if got := resourceContract["importIdentifierFormat"]; got != want {
			t.Errorf("%s import=%v want=%s", name, got, want)
		}
	}
	properties := schemas["ImagePatch"].(map[string]any)["properties"].(map[string]any)
	got := make([]string, 0, len(properties))
	for name := range properties {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"architecture", "deleteProtection", "expectedDigest", "format", "lifecycleState", "name", "sourceId", "visibility"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImagePatch fields=%v want=%v", got, want)
	}
}

func TestImportIdentifierRequiresContractPrefixAndUUID(t *testing.T) {
	if !importUUIDPattern.MatchString("c7a768d4-6f4c-4f67-9a4c-4ea9c16092ab") || importUUIDPattern.MatchString("not-a-uuid") {
		t.Fatal("import UUID validation drift")
	}
}
