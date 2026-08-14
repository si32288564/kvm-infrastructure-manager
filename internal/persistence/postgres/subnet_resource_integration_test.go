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

func TestSubnetResourceAuthorityPostgreSQL(t *testing.T) {
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
	if _, err = db.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('subnet-resource-integration',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%012x", uint64(time.Now().UnixNano())&0xffffffffffff)
	projectID := "10000000-0000-4000-8000-" + suffix
	projectDigest := digestBytes([]byte("subnet-project-" + suffix))
	if _, err = db.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'integration','subnet','subnet-project')`, projectID, "subnet-project-"+suffix, projectDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "subnet-project-"+suffix, projectDigest); err != nil {
		t.Fatal(err)
	}
	networkID := "subnet-parent-" + suffix
	network, err := CreateNetworkResource(ctx, db, NetworkResourceRequest{NetworkID: networkID, ProjectID: projectID, Name: "parent", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"})
	if err != nil {
		t.Fatal(err)
	}
	networkClaim, err := ClaimNetworkRealization(ctx, db, network.OperationID, "network-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, networkPlan, err := ovnadapter.RestoreStoredNetworkPlan(networkClaim.CanonicalPlan, networkClaim.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = AuthorizeNetworkRealizationApply(ctx, db, networkClaim); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptNetworkRealizationObservation(ctx, db, networkClaim, matchedNetworkObservation(networkClaim, networkPlan, "parent-backend", "RECEIVED")); err != nil {
		t.Fatal(err)
	}

	desired := SubnetResourceRequest{SubnetID: "subnet-resource-" + suffix, ProjectID: projectID, NetworkID: networkID, Name: "tenant-v4", IPFamily: "IPV4", CIDR: "10.88.0.7/24", GatewayPolicy: "AUTO", AllocationPolicy: "RANGE", AllocationStart: "10.88.0.10", AllocationEnd: "10.88.0.20", ReservedAddresses: []string{"10.88.0.12"}, DHCPEnabled: true, DNSServers: []string{"10.88.0.53"}}
	subnet, err := CreateSubnetResource(ctx, db, desired)
	if err != nil || subnet.CIDR != "10.88.0.0/24" || subnet.GatewayAddress != "10.88.0.1" || subnet.RealizationState != "PENDING" {
		t.Fatalf("create=%+v err=%v", subnet, err)
	}
	replay, err := CreateSubnetResource(ctx, db, desired)
	if err != nil || replay.OperationID != subnet.OperationID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err = AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: "pending-" + suffix, SubnetID: subnet.SubnetID, Mode: "AUTO", OwnerResourceType: "PORT", OwnerResourceID: "port", ExpectedSubnetRevision: 1}); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("pending Subnet allocated: %v", err)
	}
	overlap := desired
	overlap.SubnetID = "subnet-overlap-" + suffix
	overlap.Name = "overlap"
	if _, err = CreateSubnetResource(ctx, db, overlap); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("overlap err=%v", err)
	}
	claim, err := ClaimSubnetRealization(ctx, db, subnet.OperationID, "subnet-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, plan, err := ovnadapter.RestoreStoredSubnetPlan(claim.CanonicalPlan, claim.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = AuthorizeSubnetRealizationApply(ctx, db, claim); err != nil {
		t.Fatal(err)
	}
	if terminal, err := AcceptSubnetRealizationObservation(ctx, db, claim, matchedSubnetObservation(claim, plan, "dhcp-backend", "LOST")); err != nil || terminal != "VERIFIED" {
		t.Fatalf("terminal=%q err=%v", terminal, err)
	}

	desired.Name = "tenant-v4-updated"
	desired.DNSServers = []string{"10.88.0.54"}
	updated, err := UpdateSubnetResource(ctx, db, desired, 1)
	if err != nil || updated.Revision != 2 || updated.PoolGeneration != 2 || updated.RealizationGeneration != 2 {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	claim2, err := ClaimSubnetRealization(ctx, db, updated.OperationID, "subnet-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, plan2, _ := ovnadapter.RestoreStoredSubnetPlan(claim2.CanonicalPlan, claim2.PlanDigest)
	if err = AuthorizeSubnetRealizationApply(ctx, db, claim2); err != nil {
		t.Fatal(err)
	}
	unknown := matchedSubnetObservation(claim2, plan2, "dhcp-backend", "LOST")
	unknown.Observation.OptionsMatch = false
	if terminal, err := AcceptSubnetRealizationObservation(ctx, db, claim2, unknown); err != nil || terminal != "" {
		t.Fatalf("unknown terminal=%q err=%v", terminal, err)
	}
	successor, err := ClaimSubnetRealization(ctx, db, updated.OperationID, "subnet-successor", time.Minute)
	if err != nil || successor.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("successor=%+v err=%v", successor, err)
	}
	if err = AuthorizeSubnetRealizationApply(ctx, db, successor); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("apply before readback=%v", err)
	}
	if err = RecordSubnetRealizationReadBackStarted(ctx, db, successor); err != nil {
		t.Fatal(err)
	}
	if terminal, err := AcceptSubnetRealizationObservation(ctx, db, successor, matchedSubnetObservation(successor, plan2, "dhcp-backend", "UNKNOWN")); err != nil || terminal != "VERIFIED" {
		t.Fatalf("readback terminal=%q err=%v", terminal, err)
	}

	allocation, err := AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: "allocation-" + suffix, SubnetID: subnet.SubnetID, Mode: "AUTO", OwnerResourceType: "PORT", OwnerResourceID: "port-" + suffix, ExpectedSubnetRevision: 2})
	if err != nil || allocation.AssignedAddress == "10.88.0.12" {
		t.Fatalf("allocation=%+v err=%v", allocation, err)
	}
	replayed, err := AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: "allocation-" + suffix, SubnetID: subnet.SubnetID, Mode: "AUTO", OwnerResourceType: "PORT", OwnerResourceID: "port-" + suffix, ExpectedSubnetRevision: 2})
	if err != nil || replayed.AssignedAddress != allocation.AssignedAddress {
		t.Fatalf("allocation replay=%+v err=%v", replayed, err)
	}
	if _, err = AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: "explicit-collision-" + suffix, SubnetID: subnet.SubnetID, Mode: "EXPLICIT", RequestedAddress: allocation.AssignedAddress, OwnerResourceType: "PORT", OwnerResourceID: "other", ExpectedSubnetRevision: 2}); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("collision err=%v", err)
	}
	if _, err = AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: "stale-" + suffix, SubnetID: subnet.SubnetID, Mode: "AUTO", OwnerResourceType: "PORT", OwnerResourceID: "stale", ExpectedSubnetRevision: 1}); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("stale allocation err=%v", err)
	}
	concurrentResults := make(chan SubnetIPAllocation, 2)
	concurrentErrors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			value, e := AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: fmt.Sprintf("concurrent-%d-%s", index, suffix), SubnetID: subnet.SubnetID, Mode: "AUTO", OwnerResourceType: "PORT", OwnerResourceID: fmt.Sprintf("port-concurrent-%d", index), ExpectedSubnetRevision: 2})
			concurrentResults <- value
			concurrentErrors <- e
		}()
	}
	firstConcurrent, secondConcurrent := <-concurrentResults, <-concurrentResults
	if firstErr, secondErr := <-concurrentErrors, <-concurrentErrors; firstErr != nil || secondErr != nil || firstConcurrent.AssignedAddress == secondConcurrent.AssignedAddress {
		t.Fatalf("concurrent IPAM=%+v/%+v errors=%v/%v", firstConcurrent, secondConcurrent, firstErr, secondErr)
	}
	if _, err = RequestSubnetRetirement(ctx, db, subnet.SubnetID, 2); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("retirement with active allocation err=%v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE kim.subnet_resource_revision_evidence SET subnet_name='forged' WHERE subnet_id=$1 AND subnet_revision=1`, subnet.SubnetID); err == nil {
		t.Fatal("immutable Subnet revision accepted UPDATE")
	}

	retireDesired := desired
	retireDesired.SubnetID, retireDesired.Name, retireDesired.CIDR = "subnet-retire-"+suffix, "retire", "10.89.0.0/24"
	retireDesired.GatewayAddress, retireDesired.AllocationStart, retireDesired.AllocationEnd = "", "10.89.0.10", "10.89.0.20"
	retireDesired.ReservedAddresses, retireDesired.DNSServers = nil, []string{"10.89.0.53"}
	retireSubnet, err := CreateSubnetResource(ctx, db, retireDesired)
	if err != nil {
		t.Fatal(err)
	}
	retireCreateClaim, err := ClaimSubnetRealization(ctx, db, retireSubnet.OperationID, "subnet-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, retireCreatePlan, _ := ovnadapter.RestoreStoredSubnetPlan(retireCreateClaim.CanonicalPlan, retireCreateClaim.PlanDigest)
	if err = AuthorizeSubnetRealizationApply(ctx, db, retireCreateClaim); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptSubnetRealizationObservation(ctx, db, retireCreateClaim, matchedSubnetObservation(retireCreateClaim, retireCreatePlan, "retire-dhcp", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	retiring, err := RequestSubnetRetirement(ctx, db, retireSubnet.SubnetID, 1)
	if err != nil || retiring.Lifecycle != "RETIRE_PENDING" || retiring.PoolState != "RETIRE_PENDING" {
		t.Fatalf("retiring=%+v err=%v", retiring, err)
	}
	if _, err = AllocateSubnetIP(ctx, db, SubnetIPAllocationRequest{AllocationID: "frozen-" + suffix, SubnetID: retiring.SubnetID, Mode: "AUTO", OwnerResourceType: "PORT", OwnerResourceID: "frozen", ExpectedSubnetRevision: 2}); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("retiring pool allocated: %v", err)
	}
	retireClaim, err := ClaimSubnetRealization(ctx, db, retiring.OperationID, "subnet-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, retirePlan, _ := ovnadapter.RestoreStoredSubnetPlan(retireClaim.CanonicalPlan, retireClaim.PlanDigest)
	if err = AuthorizeSubnetRealizationApply(ctx, db, retireClaim); err != nil {
		t.Fatal(err)
	}
	present := matchedSubnetObservation(retireClaim, retirePlan, "retire-dhcp", "LOST")
	if terminal, err := AcceptSubnetRealizationObservation(ctx, db, retireClaim, present); err != nil || terminal != "" {
		t.Fatalf("present retirement=%q err=%v", terminal, err)
	}
	retireSuccessor, err := ClaimSubnetRealization(ctx, db, retiring.OperationID, "subnet-successor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = RecordSubnetRealizationReadBackStarted(ctx, db, retireSuccessor); err != nil {
		t.Fatal(err)
	}
	absent := matchedSubnetObservation(retireSuccessor, retirePlan, "", "UNKNOWN")
	absent.Observation.ObjectPresent, absent.Observation.OwnershipMarkerMatches, absent.Observation.PlanDigestMatches = false, false, false
	if terminal, err := AcceptSubnetRealizationObservation(ctx, db, retireSuccessor, absent); err != nil || terminal != "ABSENT" {
		t.Fatalf("absence terminal=%q err=%v", terminal, err)
	}
	deleted, err := GetSubnetResource(ctx, db, retireSubnet.SubnetID)
	if err != nil || deleted.Lifecycle != "DELETED" || deleted.PoolState != "RETIRED" {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
}

func matchedSubnetObservation(claim SubnetRealizationClaim, plan ovnadapter.SubnetPlan, backend, response string) SubnetRealizationObservation {
	return SubnetRealizationObservation{ObservationID: fmt.Sprintf("subnet-observation:%s:%d", claim.OperationID, claim.ClaimGeneration), OperationID: claim.OperationID, OperationGeneration: claim.OperationGeneration, ObservationGeneration: claim.ClaimGeneration, ApplyResponseState: response, DHCPObjectName: plan.DHCPObjectName, BackendUUID: backend, ObservationDigest: digestBytes([]byte(fmt.Sprintf("subnet-observation:%s:%d", claim.OperationID, claim.ClaimGeneration))), AdapterArtifactDigest: digestBytes([]byte("subnet-adapter-v1")), Observation: ovnadapter.SubnetObservation{ObjectPresent: plan.DHCPEnabled, OwnershipMarkerMatches: true, PlanDigestMatches: true, CIDRMatches: true, OptionsMatch: true, NetworkAssociationMatches: true, DHCPObjectName: plan.DHCPObjectName, BackendUUID: backend}}
}
