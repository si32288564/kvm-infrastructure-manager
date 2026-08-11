package p1c03ovnwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestOVNRuntimeWorkerProcessKillReadBackConvergence(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workerBinary := buildBinary(t, root, filepath.Join(workspace, "kim-network-worker"), "./cmd/kim-network-worker")
	nbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-nbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")
	sbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-sbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")

	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ids := seedOVNAuthority(t, ctx, pool, suffix)
	publishCurrentTestWorkerRelease(t, ctx, pool, digest("qualified-ovn-adapter"))
	decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ovnadapter.DecodePortPlan(decision.CanonicalObjectSet, decision.ObjectSetDigest)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(workspace, "ovn-state.json")
	writeJSON(t, statePath, map[string]any{
		"logical_switch_name": plan.LogicalSwitch.Name,
		"logical_port_name":   plan.LogicalPort.Name,
		"network_markers":     plan.NetworkExternalIDs,
		"port_markers":        withDigest(plan.PortExternalIDs, decision.ObjectSetDigest),
		"chassis_name":        plan.LogicalPort.OVNChassisName,
		"applied":             false,
		"apply_count":         0,
	})
	readBackSignal := filepath.Join(workspace, "readback-started")
	baseArgs := []string{
		"-database-url", databaseURL, "-adapter-artifact-digest", digest("qualified-ovn-adapter"),
		"-ovn-nb-db", "unix:/fixture/nb.sock", "-ovn-sb-db", "unix:/fixture/sb.sock",
		"-ovn-nbctl", nbctl, "-ovn-sbctl", sbctl, "-poll-interval", "20ms",
		"-batch-limit", "1", "-claim-lease", "750ms", "-command-timeout", "5s",
		"-claim-maximum-lifetime", "750ms", "-claim-renew-interval", "0",
	}
	baseArgs = append(baseArgs, testWorkerReleaseArguments()...)
	workerA := startProcess(t, workerBinary, append(baseArgs, "-worker-id", "ovn-worker-a")...)
	workerA.cmd.Env = append(os.Environ(),
		"KIM_OVN_FIXTURE_STATE="+statePath,
		"KIM_OVN_FIXTURE_BLOCK_READBACK=1",
		"KIM_OVN_FIXTURE_READBACK_SIGNAL="+readBackSignal,
	)
	workerA.start(t)
	waitForFileOrProcessFailure(t, workerA, readBackSignal, 20*time.Second, "worker A did not reach post-apply read-back")
	workerA.kill(t)

	var firstOwner string
	var firstGeneration int64
	if err := pool.QueryRow(ctx, `SELECT claim_owner,claim_generation FROM kim.ovn_runtime_work_current WHERE intent_id=$1 AND intent_generation=1`, ids.intentID).Scan(&firstOwner, &firstGeneration); err != nil || firstOwner != "ovn-worker-a" || firstGeneration != 1 {
		t.Fatalf("worker A claim=%s/%d err=%v", firstOwner, firstGeneration, err)
	}
	stale := postgres.OVNRuntimeClaim{WorkID: fmt.Sprintf("ovn-runtime:%s:1", ids.intentID), Owner: "ovn-worker-a", ClaimGeneration: 1}
	eventually(t, 5*time.Second, func() bool {
		err := postgres.AuthorizeOVNRuntimeApply(ctx, pool, stale)
		return errors.Is(err, postgres.ErrStaleOVNRuntimeClaim)
	}, "worker A claim did not expire")

	workerB := startProcess(t, workerBinary, append(baseArgs, "-worker-id", "ovn-worker-b")...)
	workerB.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+statePath)
	workerB.start(t)
	defer workerB.stop()
	eventually(t, 20*time.Second, func() bool {
		var state string
		return pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE intent_id=$1 AND intent_generation=1`, ids.intentID).Scan(&state) == nil && state == "OBSERVED"
	}, "worker B did not converge the uncertain OVN work from read-back")

	var attempts, unknownEvents, readBackEvents, applyEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, stale.WorkID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED')
		FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1`, stale.WorkID).Scan(&unknownEvents, &readBackEvents, &applyEvents); err != nil {
		t.Fatal(err)
	}
	state := readFixtureState(t, statePath)
	if attempts != 2 || unknownEvents != 1 || readBackEvents != 1 || applyEvents != 1 || state.ApplyCount != 1 {
		t.Fatalf("attempts=%d unknown=%d readback=%d apply-events=%d physical-applies=%d", attempts, unknownEvents, readBackEvents, applyEvents, state.ApplyCount)
	}
	var acceptedOwner, claimMode string
	var acceptedGeneration int64
	if err := pool.QueryRow(ctx, `SELECT claim_owner,claim_generation,claim_mode FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1 ORDER BY claim_generation DESC LIMIT 1`, stale.WorkID).Scan(&acceptedOwner, &acceptedGeneration, &claimMode); err != nil || acceptedOwner != "ovn-worker-b" || acceptedGeneration != 2 || claimMode != "READ_BACK_FIRST" {
		t.Fatalf("recovery claim=%s/%d/%s err=%v", acceptedOwner, acceptedGeneration, claimMode, err)
	}
}

type authorityIDs struct{ intentID, portID string }

func seedOVNAuthority(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) authorityIDs {
	t.Helper()
	hostID, poolID := "ovn-fi-host-"+suffix, "ovn-fi-pool-"+suffix
	imageID, flavorID := "ovn-fi-image-"+suffix, "ovn-fi-flavor-"+suffix
	admissionID, requestID := "ovn-fi-admission-"+suffix, "ovn-fi-request-"+suffix
	networkID, subnetID := "ovn-fi-network-"+suffix, "ovn-fi-subnet-"+suffix
	segmentID, portID := "ovn-fi-segment-"+suffix, "ovn-fi-port-"+suffix
	intentID := "ovn-fi-intent-" + suffix
	emptyDigest := digest("[]")
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES($1,1,'ACTIVE') ON CONFLICT(singleton) DO NOTHING`, "ovn-fi-"+suffix)
	batch.Queue(`INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID)
	batch.Queue(`INSERT INTO kim.image_revision_evidence(
		image_id,image_revision,owner_project_id,image_format,size_bytes,checksum_algorithm,
		declared_checksum,observed_checksum,signature_state,source_uri,visibility,validation_state,
		validation_reason,metadata,metadata_digest,revision_digest
	) VALUES($1,1,'project','RAW',4096,'SHA256',$2,$2,'VERIFIED','fixture://image','PRIVATE','VERIFIED','fixture','{}',$3,$4)`, imageID, digest("image"), digest("{}"), digest("image-revision"))
	batch.Queue(`INSERT INTO kim.flavor_revision_evidence(
		flavor_id,flavor_revision,owner_project_id,name,vcpus,memory_mib,root_disk_gib,
		numa_policy,cpu_allocation,cpu_pinning,extra_specs,shape_digest,revision_digest
	) VALUES($1,1,'project','fixture',1,512,1,'NONE','SHARED',false,'{}',$2,$3)`, flavorID, digest("shape"), digest("flavor-revision"))
	batch.Queue(`INSERT INTO kim.placement_pools_current(pool_id,pool_generation,lifecycle_state,policy_id,policy_generation)
		VALUES($1,1,'ACTIVE','fixture-policy',1)`, poolID)
	batch.Queue(`INSERT INTO kim.placement_admission_decisions(
		admission_id,request_id,request_digest,evaluation_digest,project_id,workload_id,host_id,
		pool_id,pool_generation,pool_policy_id,pool_policy_generation,membership_set_generation,membership_generation,
		image_id,image_revision,flavor_id,flavor_revision,flavor_shape_digest,capability_generation,
		baseline_assignment_generation,preflight_generation,compliance_generation,
		pci_requirements,pci_requirements_digest,network_requirements,network_requirements_digest,
		storage_requirements,storage_requirements_digest,decision_state,explanation
	) VALUES($1,$2,$3,$4,'project',$5,$6,$7,1,'fixture-policy',1,1,1,$8,1,$9,1,$10,1,1,1,1,
		'[]',$11,'[]',$11,'[]',$11,'ACCEPTED','{}')`, admissionID, requestID, digest("request"), digest("evaluation"), "workload-"+suffix, hostID, poolID, imageID, flavorID, digest("shape"), emptyDigest)
	batch.Queue(`INSERT INTO kim.networks_current(network_id,project_id,network_generation,lifecycle_state,mtu)
		VALUES($1,'project',1,'ACTIVE',1500)`, networkID)
	batch.Queue(`INSERT INTO kim.network_subnets_current(
		subnet_id,network_id,subnet_generation,lifecycle_state,cidr,allocation_start,allocation_end
	) VALUES($1,$2,1,'ACTIVE','192.0.2.0/24','192.0.2.10','192.0.2.200')`, subnetID, networkID)
	batch.Queue(`INSERT INTO kim.network_segment_claims_current(
		segment_claim_id,network_id,segment_generation,segment_type,scope_id,segment_id,provider_mapping_revision,claim_state
	) VALUES($1,$2,1,'VLAN',$3,100,1,'ACTIVE')`, segmentID, networkID, "scope-"+suffix)
	batch.Queue(`INSERT INTO kim.host_network_mappings_current(
		host_id,segment_claim_id,mapping_generation,mapping_state,maximum_mtu,supported_binding_types,ovn_chassis_name
	) VALUES($1,$2,1,'CURRENT',1500,ARRAY['OVS'],'chassis-fixture')`, hostID, segmentID)
	batch.Queue(`INSERT INTO kim.network_ports_current(
		port_id,placement_admission_id,project_id,workload_id,network_id,subnet_id,port_generation,desired_state
	) VALUES($1,$2,'project',$3,$4,$5,1,'ACTIVE')`, portID, admissionID, "workload-"+suffix, networkID, subnetID)
	batch.Queue(`INSERT INTO kim.network_identity_claims(
		identity_claim_id,placement_admission_id,port_id,project_id,network_id,subnet_id,
		claim_type,ip_address,allocation_source,claim_generation,claim_state
	) VALUES($1,$2,$3,'project',$4,$5,'IP','192.0.2.20','EXPLICIT',1,'ACTIVE')`, "ip-"+suffix, admissionID, portID, networkID, subnetID)
	batch.Queue(`INSERT INTO kim.network_identity_claims(
		identity_claim_id,placement_admission_id,port_id,project_id,network_id,subnet_id,
		claim_type,mac_address,allocation_source,claim_generation,claim_state
	) VALUES($1,$2,$3,'project',$4,$5,'MAC','02:00:00:00:00:20','EXPLICIT',1,'ACTIVE')`, "mac-"+suffix, admissionID, portID, networkID, subnetID)
	batch.Queue(`INSERT INTO kim.port_bindings_current(
		port_id,placement_admission_id,host_id,segment_claim_id,binding_generation,binding_type,binding_state
	) VALUES($1,$2,$3,$4,1,'OVS','ACTIVE')`, portID, admissionID, hostID, segmentID)
	results := pool.SendBatch(ctx, batch)
	for range 14 {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			t.Fatal(err)
		}
	}
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}
	return authorityIDs{intentID: intentID, portID: portID}
}

type fixtureState struct {
	ApplyCount int `json:"apply_count"`
}

func withDigest(markers map[string]string, objectDigest string) map[string]string {
	copy := make(map[string]string, len(markers)+1)
	for key, value := range markers {
		copy[key] = value
	}
	copy["kim.object_set_digest"] = objectDigest
	return copy
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureState(t *testing.T, path string) fixtureState {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state fixtureState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func buildBinary(t *testing.T, root, output, target string) string {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, target)
	command.Dir = root
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v: %s", target, err, raw)
	}
	return output
}

type process struct {
	cmd      *exec.Cmd
	done     chan error
	output   bytes.Buffer
	stopOnce sync.Once
}

func startProcess(t *testing.T, binary string, arguments ...string) *process {
	t.Helper()
	return &process{cmd: exec.Command(binary, arguments...), done: make(chan error, 1)}
}

func (process *process) start(t *testing.T) {
	t.Helper()
	process.cmd.Stdout = &process.output
	process.cmd.Stderr = &process.output
	if err := process.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { process.done <- process.cmd.Wait() }()
}

func waitForFileOrProcessFailure(t *testing.T, process *process, path string, timeout time.Duration, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-process.done:
			t.Fatalf("%s: process stopped: %v: %s", message, err, process.output.String())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: %s", message, process.output.String())
}

func (process *process) kill(t *testing.T) {
	t.Helper()
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-process.done
}

func (process *process) stop() {
	process.stopOnce.Do(func() {
		select {
		case <-process.done:
			return
		default:
		}
		_ = process.cmd.Process.Signal(os.Interrupt)
		select {
		case <-process.done:
		case <-time.After(2 * time.Second):
			_ = process.cmd.Process.Kill()
			<-process.done
		}
	})
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(message)
}
