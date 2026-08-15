package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/httpapi"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/phase2"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

func TestNorthboundPhase2PostgreSQL(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenWithMaxConnections(ctx, url, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('northbound-phase2',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%012x", uint64(time.Now().UnixNano())&0xffffffffffff)
	projectID := "20000000-0000-4000-8000-" + suffix
	pd := digestBytes([]byte("northbound-phase2-" + suffix))
	if _, err = db.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'integration','phase2','phase2-project')`, projectID, "phase2-"+suffix, pd); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "phase2-"+suffix, pd); err != nil {
		t.Fatal(err)
	}
	p := resource.Principal{Issuer: "phase2-integration", Subject: "automation-" + suffix, Type: "AUTOMATION"}
	if _, err = db.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','','ADMIN','ACTIVE',1)`, "phase2-admin-"+suffix, p.Issuer, p.Subject, p.Type); err != nil {
		t.Fatal(err)
	}
	store := NorthboundPhase2Store{DB: db}
	svc := phase2.Service{Store: store}
	create := func(k phase2.Kind, d phase2.Desired, key string) phase2.Resource {
		t.Helper()
		v, replay, e := svc.Create(ctx, p, k, phase2.CreateRequest{Desired: d, IdempotencyKey: key, RequestID: "request-" + key, CanonicalPath: "/api/v1/" + k.Plural()})
		if e != nil || replay {
			t.Fatalf("create %s=%+v replay=%v err=%v", k, v, replay, e)
		}
		return v
	}
	network := create(phase2.Network, phase2.Desired{ProjectID: projectID, Name: "tenant-overlay", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"}, "network-"+suffix)
	completeNorthboundNetwork(t, ctx, db, network.OperationID)
	replayed, replay, err := svc.Create(ctx, p, phase2.Network, phase2.CreateRequest{Desired: phase2.Desired{ProjectID: projectID, Name: "tenant-overlay", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"}, IdempotencyKey: "network-" + suffix, RequestID: "network-replay", CanonicalPath: "/api/v1/networks"})
	if err != nil || !replay || replayed.ID != network.ID {
		t.Fatalf("network replay=%+v/%v/%v", replayed, replay, err)
	}
	_, _, err = svc.Create(ctx, p, phase2.Network, phase2.CreateRequest{Desired: phase2.Desired{ProjectID: projectID, Name: "different", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"}, IdempotencyKey: "network-" + suffix, RequestID: "network-conflict", CanonicalPath: "/api/v1/networks"})
	if !errors.Is(err, resource.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict=%v", err)
	}
	subnet := create(phase2.Subnet, phase2.Desired{ProjectID: projectID, NetworkID: network.ID, Name: "tenant-v4", IPFamily: "IPV4", CIDR: "10.91.0.0/24", GatewayPolicy: "AUTO", AllocationPolicy: "RANGE", AllocationStart: "10.91.0.10", AllocationEnd: "10.91.0.200", DHCPEnabled: true, DNSServers: []string{"10.91.0.53"}}, "subnet-"+suffix)
	completeNorthboundSubnet(t, ctx, db, subnet.OperationID)
	port := create(phase2.Port, phase2.Desired{ProjectID: projectID, NetworkID: network.ID, Name: "unattached", MACPolicy: "AUTO", SubnetID: subnet.ID, IPAllocationMode: "AUTO", AttachmentPolicy: "ON_DEMAND", DatapathProfile: "STANDARD"}, "port-"+suffix)
	completeNorthboundPort(t, ctx, db, port.OperationID)
	host := "phase2-volume-host-" + suffix
	fingerprint := digestBytes([]byte(host + "-cert"))
	prepareSessionIdentityFixture(t, ctx, db, host, 1, fingerprint)
	if _, err = AdmitAgentSession(ctx, db, AgentSessionAdmission{SessionAttemptID: host + "-attempt", HostID: host, ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, db, host)
	if err = UpdateHostReadinessGate(ctx, db, HostReadinessGate{HostID: host, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	class := "phase2-storage-" + suffix
	if err = RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: "phase2-backend-" + suffix, HostID: host, VGUUID: "phase2-vg-" + suffix, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: class, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "phase2-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	volume := create(phase2.Volume, phase2.Desired{ProjectID: projectID, Name: "blank-root", SizeBytes: 16 << 20, StorageClassID: class, StorageClassRevision: 1, Bootable: true, SourceType: "BLANK"}, "volume-"+suffix)
	if volume.RealizationState != "PENDING" || volume.OperationID == "" {
		t.Fatalf("volume=%+v", volume)
	}
	for _, entry := range []struct {
		k  phase2.Kind
		id string
	}{{phase2.Network, network.ID}, {phase2.Subnet, subnet.ID}, {phase2.Port, port.ID}, {phase2.Volume, volume.ID}} {
		got, e := svc.Get(ctx, p, entry.k, entry.id, "get-"+string(entry.k))
		if e != nil || got.ID != entry.id {
			t.Fatalf("get %s=%+v/%v", entry.k, got, e)
		}
		page, e := svc.List(ctx, p, entry.k, phase2.ListRequest{ProjectID: projectID, Limit: 100}, "list-"+string(entry.k))
		if e != nil || len(page.Items) < 1 {
			t.Fatalf("list %s=%+v/%v", entry.k, page, e)
		}
	}
	op, err := svc.GetOperation(ctx, p, volume.OperationID, "volume-operation")
	if err != nil || op.TargetResourceType != "VOLUME" || op.Phase != "PENDING" {
		t.Fatalf("operation=%+v/%v", op, err)
	}
	server := httptest.NewServer(httpapi.Server{Projects: project.Service{Store: NorthboundProjectStore{DB: db}}, Phase2: svc, Authenticator: integrationPrincipalAuthenticator{subjects: map[string]project.Principal{"phase2": p}}}.Handler())
	defer server.Close()
	response, err := integrationRequest(server.Client(), "GET", server.URL+"/api/v1/networks/"+network.ID, "", map[string]string{"X-Test-Principal": "phase2"})
	if err != nil {
		t.Fatal(err)
	}
	var viaHTTP phase2.Resource
	if err = json.NewDecoder(response.Body).Decode(&viaHTTP); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("ETag") != "\"1\"" || viaHTTP.ID != network.ID {
		t.Fatalf("HTTP Network=%d/%q/%+v", response.StatusCode, response.Header.Get("ETag"), viaHTTP)
	}
	response, err = integrationRequest(server.Client(), "GET", server.URL+"/api/v1/operations/"+volume.OperationID, "", map[string]string{"X-Test-Principal": "phase2"})
	if err != nil {
		t.Fatal(err)
	}
	var viaHTTPOperation phase2.Operation
	if err = json.NewDecoder(response.Body).Decode(&viaHTTPOperation); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || viaHTTPOperation.TargetResourceType != "VOLUME" {
		t.Fatalf("HTTP Operation=%d/%+v", response.StatusCode, viaHTTPOperation)
	}
	name := "renamed-network"
	updated, err := svc.Patch(ctx, p, phase2.Network, network.ID, 1, phase2.Patch{Name: &name}, "network-patch")
	if err != nil || updated.Revision != 2 {
		t.Fatalf("patch=%+v/%v", updated, err)
	}
	if _, err = svc.Patch(ctx, p, phase2.Network, network.ID, 1, phase2.Patch{Name: &name}, "stale"); !errors.Is(err, resource.ErrStaleRevision) {
		t.Fatalf("stale patch=%v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE kim.northbound_phase2_idempotency_evidence SET response_status=202 WHERE resource_id=$1`, network.ID); err == nil {
		t.Fatal("immutable Phase 2 idempotency evidence accepted UPDATE")
	}
}

func completeNorthboundNetwork(t *testing.T, ctx context.Context, db TxBeginner, operation string) {
	t.Helper()
	c, e := ClaimNetworkRealization(ctx, db, operation, "northbound-worker", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	_, p, e := ovnadapter.RestoreStoredNetworkPlan(c.CanonicalPlan, c.PlanDigest)
	if e != nil {
		t.Fatal(e)
	}
	if e = AuthorizeNetworkRealizationApply(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if _, e = AcceptNetworkRealizationObservation(ctx, db, c, matchedNetworkObservation(c, p, "northbound-network", "RECEIVED")); e != nil {
		t.Fatal(e)
	}
}
func completeNorthboundSubnet(t *testing.T, ctx context.Context, db TxBeginner, operation string) {
	t.Helper()
	c, e := ClaimSubnetRealization(ctx, db, operation, "northbound-worker", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	_, p, e := ovnadapter.RestoreStoredSubnetPlan(c.CanonicalPlan, c.PlanDigest)
	if e != nil {
		t.Fatal(e)
	}
	if e = AuthorizeSubnetRealizationApply(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if _, e = AcceptSubnetRealizationObservation(ctx, db, c, matchedSubnetObservation(c, p, "northbound-dhcp", "RECEIVED")); e != nil {
		t.Fatal(e)
	}
}
func completeNorthboundPort(t *testing.T, ctx context.Context, db TxBeginner, operation string) {
	t.Helper()
	c, e := ClaimPortRealization(ctx, db, operation, "northbound-worker", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	_, p, e := ovnadapter.RestoreStoredPortResourcePlan(c.CanonicalPlan, c.PlanDigest)
	if e != nil {
		t.Fatal(e)
	}
	if e = AuthorizePortRealizationApply(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if _, e = AcceptPortRealizationObservation(ctx, db, c, matchedPortObservation(c, p, "northbound-port", "RECEIVED")); e != nil {
		t.Fatal(e)
	}
}
