package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

func TestPortResourceAuthorityPostgreSQL(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenWithMaxConnections(ctx, url, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('port-resource-integration',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%012x", uint64(time.Now().UnixNano())&0xffffffffffff)
	projectID := "20000000-0000-4000-8000-" + suffix
	pd := digestBytes([]byte("port-project-" + suffix))
	if _, err = db.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'integration','port','port-project')`, projectID, "port-project-"+suffix, pd); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "port-project-"+suffix, pd); err != nil {
		t.Fatal(err)
	}
	network, err := CreateNetworkResource(ctx, db, NetworkResourceRequest{NetworkID: "port-network-" + suffix, ProjectID: projectID, Name: "port-parent", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"})
	if err != nil {
		t.Fatal(err)
	}
	nc, err := ClaimNetworkRealization(ctx, db, network.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, np, _ := ovnadapter.RestoreStoredNetworkPlan(nc.CanonicalPlan, nc.PlanDigest)
	if err = AuthorizeNetworkRealizationApply(ctx, db, nc); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptNetworkRealizationObservation(ctx, db, nc, matchedNetworkObservation(nc, np, "network-backend", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	subnet, err := CreateSubnetResource(ctx, db, SubnetResourceRequest{SubnetID: "port-subnet-" + suffix, ProjectID: projectID, NetworkID: network.NetworkID, Name: "port-v4", IPFamily: "IPV4", CIDR: "10.91.0.0/24", GatewayPolicy: "AUTO", AllocationPolicy: "RANGE", AllocationStart: "10.91.0.10", AllocationEnd: "10.91.0.30", DHCPEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ClaimSubnetRealization(ctx, db, subnet.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, sp, _ := ovnadapter.RestoreStoredSubnetPlan(sc.CanonicalPlan, sc.PlanDigest)
	if err = AuthorizeSubnetRealizationApply(ctx, db, sc); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptSubnetRealizationObservation(ctx, db, sc, matchedSubnetObservation(sc, sp, "dhcp-backend", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	desired := PortResourceRequest{PortID: "port-resource-" + suffix, ProjectID: projectID, NetworkID: network.NetworkID, Name: "tenant-port", MACPolicy: "AUTO", SubnetID: subnet.SubnetID, IPAllocationMode: "AUTO", AttachmentPolicy: "ON_DEMAND", DatapathProfile: "STANDARD"}
	invalidMAC := desired
	invalidMAC.PortID = "port-invalid-mac-" + suffix
	invalidMAC.MACPolicy = "EXPLICIT"
	invalidMAC.RequestedMAC = "00:00:00:00:00:00"
	if _, err = CreatePortResource(ctx, db, invalidMAC); err == nil {
		t.Fatal("reserved zero MAC accepted")
	}
	port, err := CreatePortResource(ctx, db, desired)
	if err != nil || port.Revision != 1 || port.AttachmentState != "UNATTACHED" || port.MACAddress == "" || port.IPAddress == "" || port.RealizationState != "PENDING" {
		t.Fatalf("port=%+v err=%v", port, err)
	}
	replay, err := CreatePortResource(ctx, db, desired)
	if err != nil || replay.MACAddress != port.MACAddress || replay.IPAddress != port.IPAddress {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	collision := PortResourceRequest{PortID: "port-mac-collision-" + suffix, ProjectID: projectID, NetworkID: network.NetworkID, Name: "collision", MACPolicy: "EXPLICIT", RequestedMAC: port.MACAddress, IPAllocationMode: "NONE", AttachmentPolicy: "ON_DEMAND", DatapathProfile: "STANDARD"}
	if _, err = CreatePortResource(ctx, db, collision); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("active MAC collision=%v", err)
	}
	claim, err := ClaimPortRealization(ctx, db, port.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, plan, _ := ovnadapter.RestoreStoredPortResourcePlan(claim.CanonicalPlan, claim.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, claim); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptPortRealizationObservation(ctx, db, claim, matchedPortObservation(claim, plan, "lsp-backend", "LOST")); err != nil {
		t.Fatal(err)
	}
	desired.Name = "renamed"
	updated, err := UpdatePortResource(ctx, db, desired, 1)
	if err != nil || updated.Revision != 2 || updated.MACAddress != port.MACAddress || updated.IPAddress != port.IPAddress {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	uc, err := ClaimPortRealization(ctx, db, updated.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, up, _ := ovnadapter.RestoreStoredPortResourcePlan(uc.CanonicalPlan, uc.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, uc); err != nil {
		t.Fatal(err)
	}
	wrong := matchedPortObservation(uc, up, "lsp-backend", "LOST")
	wrong.Observation.MACMatches = false
	if terminal, err := AcceptPortRealizationObservation(ctx, db, uc, wrong); err != nil || terminal != "" {
		t.Fatalf("wrong terminal=%q err=%v", terminal, err)
	}
	successor, err := ClaimPortRealization(ctx, db, updated.OperationID, "successor", time.Minute)
	if err != nil || successor.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("successor=%+v err=%v", successor, err)
	}
	if err = AuthorizePortRealizationApply(ctx, db, successor); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("apply before readback=%v", err)
	}
	if err = RecordPortRealizationReadBackStarted(ctx, db, successor); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptPortRealizationObservation(ctx, db, successor, matchedPortObservation(successor, up, "lsp-backend", "UNKNOWN")); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE kim.port_resource_revision_evidence SET port_name='forged' WHERE port_id=$1 AND port_revision=1`, port.PortID); err == nil {
		t.Fatal("immutable Port revision accepted UPDATE")
	}
	retiring, err := RetirePortResource(ctx, db, port.PortID, 2)
	if err != nil || retiring.AttachmentState != "RETIRE_PENDING" {
		t.Fatalf("retiring=%+v err=%v", retiring, err)
	}
	rc, err := ClaimPortRealization(ctx, db, retiring.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, rp, _ := ovnadapter.RestoreStoredPortResourcePlan(rc.CanonicalPlan, rc.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, rc); err != nil {
		t.Fatal(err)
	}
	absent := matchedPortObservation(rc, rp, "", "LOST")
	absent.Observation = ovnadapter.PortResourceObservation{LogicalPortName: rp.LogicalPortName}
	if _, err = AcceptPortRealizationObservation(ctx, db, rc, absent); err != nil {
		t.Fatal(err)
	}
	deleted, err := GetPortResource(ctx, db, port.PortID)
	if err != nil || deleted.AttachmentState != "DELETED" || deleted.Lifecycle != "RELEASED" {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	var macState, ipState string
	if err = db.QueryRow(ctx, `SELECT m.allocation_state,i.allocation_state FROM kim.port_mac_allocations_current m JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_id=m.port_id WHERE m.port_id=$1`, port.PortID).Scan(&macState, &ipState); err != nil || macState != "RELEASED" || ipState != "RELEASED" {
		t.Fatalf("release=%s/%s err=%v", macState, ipState, err)
	}
	reused, err := CreatePortResource(ctx, db, collision)
	if err != nil || reused.MACAddress != port.MACAddress {
		t.Fatalf("released MAC reuse=%+v err=%v", reused, err)
	}
	if _, err = AcceptPortRealizationObservation(ctx, db, rc, absent); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("delayed old terminal replay=%v", err)
	}
	var reusedState string
	if err = db.QueryRow(ctx, `SELECT allocation_state FROM kim.port_mac_allocations_current WHERE port_id=$1`, reused.PortID).Scan(&reusedState); err != nil || reusedState != "ALLOCATED" {
		t.Fatalf("delayed cleanup changed new owner=%s err=%v", reusedState, err)
	}
	noIP := desired
	noIP.PortID = "port-no-ip-" + suffix
	noIP.Name = "no-ip"
	noIP.SubnetID = ""
	noIP.IPAllocationMode = "NONE"
	noIPPort, err := CreatePortResource(ctx, db, noIP)
	if err != nil || noIPPort.IPAllocationID != "" {
		t.Fatalf("no-IP create=%+v err=%v", noIPPort, err)
	}
	noIPC, err := ClaimPortRealization(ctx, db, noIPPort.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, noIPP, _ := ovnadapter.RestoreStoredPortResourcePlan(noIPC.CanonicalPlan, noIPC.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, noIPC); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptPortRealizationObservation(ctx, db, noIPC, matchedPortObservation(noIPC, noIPP, "no-ip-lsp", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	noIPRetire, err := RetirePortResource(ctx, db, noIP.PortID, 1)
	if err != nil {
		t.Fatal(err)
	}
	noIPRC, err := ClaimPortRealization(ctx, db, noIPRetire.OperationID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, noIPRP, _ := ovnadapter.RestoreStoredPortResourcePlan(noIPRC.CanonicalPlan, noIPRC.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, noIPRC); err != nil {
		t.Fatal(err)
	}
	noIPAbsent := matchedPortObservation(noIPRC, noIPRP, "", "RECEIVED")
	noIPAbsent.Observation = ovnadapter.PortResourceObservation{LogicalPortName: noIPRP.LogicalPortName}
	if _, err = AcceptPortRealizationObservation(ctx, db, noIPRC, noIPAbsent); err != nil {
		t.Fatal(err)
	}
}

func matchedPortObservation(c PortRealizationClaim, p ovnadapter.PortResourcePlan, backend, response string) PortRealizationObservation {
	return PortRealizationObservation{ObservationID: fmt.Sprintf("port-observation:%s:%d", c.OperationID, c.ClaimGeneration), OperationID: c.OperationID, OperationGeneration: c.OperationGeneration, ObservationGeneration: c.ClaimGeneration, ApplyResponseState: response, LogicalPortName: p.LogicalPortName, BackendUUID: backend, ObservationDigest: digestBytes([]byte(fmt.Sprintf("port-observation:%s:%d", c.OperationID, c.ClaimGeneration))), AdapterArtifactDigest: digestBytes([]byte("port-adapter-v1")), Observation: ovnadapter.PortResourceObservation{LogicalPortName: p.LogicalPortName, BackendUUID: backend, ObjectPresent: true, OwnershipMarkerMatches: true, PlanDigestMatches: true, NetworkMatches: true, MACMatches: true, IPMatches: true, BindingMatches: true}}
}
