package httpapi

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOpenAPIProjectContractAndLifecycleMetadata(t *testing.T) {
	raw, err := os.ReadFile("../../../api/openapi/kim-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("OpenAPI is not valid JSON: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("OpenAPI version=%v", document["openapi"])
	}
	paths := document["paths"].(map[string]any)
	for path, methods := range map[string][]string{"/projects": {"post", "get"}, "/projects/{project_id}": {"get", "patch", "delete"}} {
		entry, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("path %s absent", path)
		}
		for _, method := range methods {
			if _, ok := entry[method]; !ok {
				t.Fatalf("%s %s absent", method, path)
			}
		}
	}
	components := document["components"].(map[string]any)
	securitySchemes := components["securitySchemes"].(map[string]any)
	oauth := securitySchemes["oauth2"].(map[string]any)
	flows := oauth["flows"].(map[string]any)
	for _, flow := range []string{"authorizationCode", "clientCredentials"} {
		if _, ok := flows[flow]; !ok {
			t.Fatalf("OAuth flow %s absent", flow)
		}
	}
	parameters := components["parameters"].(map[string]any)
	for _, parameter := range []string{"IdempotencyKey", "IfMatch", "RequestID", "ProjectID"} {
		if _, ok := parameters[parameter]; !ok {
			t.Fatalf("parameter %s absent", parameter)
		}
	}
	schemas := components["schemas"].(map[string]any)
	projectSchema := schemas["Project"].(map[string]any)
	resourceMetadata := projectSchema["x-kim-resource"].(map[string]any)
	if resourceMetadata["resourceType"] != "PROJECT" || resourceMetadata["identityField"] != "id" || resourceMetadata["revisionField"] != "revision" || resourceMetadata["mutationMode"] != "SYNCHRONOUS_AUTHORITY_COMMIT" {
		t.Fatalf("Project lifecycle metadata=%v", resourceMetadata)
	}
	properties := projectSchema["properties"].(map[string]any)
	for _, field := range []string{"id", "name", "deleteProtection", "revision", "createdAt", "updatedAt"} {
		value, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("Project field %s absent", field)
		}
		if _, isReference := value["$ref"]; !isReference {
			if _, classified := value["x-kim-field-class"]; !classified {
				t.Fatalf("Project field %s has no lifecycle classification", field)
			}
		}
	}
	text := string(raw)
	for _, forbiddenDesired := range []string{"hostId", "pcpu", "pmdCore", "rxq", "vhostSocket", "ovsUuid", "pciBdf", "lvUuid", "materializationGeneration", "recoveryGeneration", "evacuationGeneration"} {
		if strings.Contains(text, `"`+forbiddenDesired+`"`) {
			t.Fatalf("physical/internal field %s leaked into Phase 0 contract", forbiddenDesired)
		}
	}
	problem := schemas["Problem"].(map[string]any)
	problemProperties := problem["properties"].(map[string]any)
	codes := problemProperties["code"].(map[string]any)["enum"].([]any)
	for _, required := range []string{"VALIDATION_FAILED", "UNAUTHENTICATED", "FORBIDDEN", "RESOURCE_NOT_FOUND", "RESOURCE_CONFLICT", "STALE_REVISION", "IDEMPOTENCY_CONFLICT", "DEPENDENCY_CONFLICT", "DELETE_PROTECTED", "OPERATION_IN_PROGRESS", "INTERNAL_ERROR", "SERVICE_UNAVAILABLE"} {
		found := false
		for _, code := range codes {
			found = found || code == required
		}
		if !found {
			t.Fatalf("Problem code %s absent", required)
		}
	}
}
