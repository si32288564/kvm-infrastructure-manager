package ovnadapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const NetworkIntentSchema = "kim.network-intent.ovn-network/v1"

type NetworkIntentInput struct {
	OperationID, ProjectID, NetworkID, AllocationID, SegmentType string
	OperationGeneration, AuthorityRevision, BackendRevision      uint64
	AllocationGeneration, RealizationGeneration                  uint64
	SegmentID                                                    uint32
	DesiredState                                                 string
}

type NetworkPlan struct {
	SchemaVersion         string            `json:"schema_version"`
	OperationID           string            `json:"operation_id"`
	OperationGeneration   uint64            `json:"operation_generation"`
	ProjectID             string            `json:"project_id"`
	NetworkID             string            `json:"network_id"`
	AuthorityRevision     uint64            `json:"authority_revision"`
	BackendRevision       uint64            `json:"backend_revision"`
	AllocationID          string            `json:"allocation_id"`
	AllocationGeneration  uint64            `json:"allocation_generation"`
	RealizationGeneration uint64            `json:"realization_generation"`
	SegmentType           string            `json:"segment_type"`
	SegmentID             uint32            `json:"segment_id"`
	DesiredState          string            `json:"desired_state"`
	LogicalSwitchName     string            `json:"logical_switch_name"`
	ExpectedExternalIDs   map[string]string `json:"expected_external_ids"`
}

type NetworkObservation struct {
	LogicalSwitchPresent, OwnershipMarkerMatches, PlanDigestMatches bool
	LogicalSwitchName, BackendUUID                                  string
}

func (o NetworkObservation) State(desired string) string {
	if desired == "ABSENT" {
		if !o.LogicalSwitchPresent {
			return "ABSENT"
		}
		if o.OwnershipMarkerMatches && o.PlanDigestMatches {
			return "UNKNOWN"
		}
		return "CONFLICTING"
	}
	if o.LogicalSwitchPresent && o.OwnershipMarkerMatches && o.PlanDigestMatches {
		return "VERIFIED"
	}
	if o.LogicalSwitchPresent && (!o.OwnershipMarkerMatches || !o.PlanDigestMatches) {
		return "CONFLICTING"
	}
	return "UNKNOWN"
}

func PlanNetwork(input NetworkIntentInput) ([]byte, string, error) {
	if input.OperationID == "" || input.ProjectID == "" || input.NetworkID == "" || input.AllocationID == "" ||
		input.OperationGeneration == 0 || input.AuthorityRevision == 0 || input.BackendRevision == 0 ||
		input.AllocationGeneration == 0 || input.RealizationGeneration == 0 || input.SegmentID == 0 ||
		(input.SegmentType != "VNI" && input.SegmentType != "VLAN") ||
		(input.DesiredState != "PRESENT" && input.DesiredState != "ABSENT") {
		return nil, "", errors.New("complete typed OVN Network intent is required")
	}
	markers := map[string]string{
		"kim.owner": "KIM", "kim.aggregate_type": "NETWORK", "kim.project_id": input.ProjectID,
		"kim.network_id": input.NetworkID, "kim.network_revision": strconv.FormatUint(input.BackendRevision, 10),
		"kim.network_generation": strconv.FormatUint(input.BackendRevision, 10),
		"kim.allocation_id":      input.AllocationID, "kim.allocation_generation": strconv.FormatUint(input.AllocationGeneration, 10),
		"kim.realization_generation": strconv.FormatUint(input.RealizationGeneration, 10),
		"kim.segment_type":           input.SegmentType, "kim.segment_id": strconv.FormatUint(uint64(input.SegmentID), 10),
	}
	plan := NetworkPlan{SchemaVersion: NetworkIntentSchema, OperationID: input.OperationID,
		OperationGeneration: input.OperationGeneration, ProjectID: input.ProjectID, NetworkID: input.NetworkID,
		AuthorityRevision: input.AuthorityRevision, BackendRevision: input.BackendRevision,
		AllocationID: input.AllocationID, AllocationGeneration: input.AllocationGeneration,
		RealizationGeneration: input.RealizationGeneration, SegmentType: input.SegmentType,
		SegmentID: input.SegmentID, DesiredState: input.DesiredState,
		LogicalSwitchName: objectName("kim-ls", input.NetworkID), ExpectedExternalIDs: markers}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func RestoreStoredNetworkPlan(raw []byte, expectedDigest string) ([]byte, NetworkPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan NetworkPlan
	if err := decoder.Decode(&plan); err != nil {
		return nil, plan, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, plan, errors.New("OVN Network plan contains trailing data")
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, plan, err
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != expectedDigest || plan.SchemaVersion != NetworkIntentSchema ||
		plan.OperationID == "" || plan.OperationGeneration == 0 || plan.ProjectID == "" || plan.NetworkID == "" ||
		plan.AuthorityRevision == 0 || plan.BackendRevision == 0 || plan.AllocationID == "" || plan.AllocationGeneration == 0 ||
		plan.RealizationGeneration == 0 || plan.SegmentID == 0 || (plan.SegmentType != "VNI" && plan.SegmentType != "VLAN") ||
		(plan.DesiredState != "PRESENT" && plan.DesiredState != "ABSENT") ||
		plan.LogicalSwitchName != objectName("kim-ls", plan.NetworkID) || plan.ExpectedExternalIDs["kim.owner"] != "KIM" ||
		plan.ExpectedExternalIDs["kim.network_id"] != plan.NetworkID ||
		plan.ExpectedExternalIDs["kim.network_revision"] != strconv.FormatUint(plan.BackendRevision, 10) ||
		plan.ExpectedExternalIDs["kim.allocation_generation"] != strconv.FormatUint(plan.AllocationGeneration, 10) {
		return nil, plan, errors.New("invalid stored OVN Network plan")
	}
	return canonical, plan, nil
}

func (runtime Runtime) ReconcileNetwork(ctx context.Context, canonicalPlan []byte, digest string) (RuntimeResult, error) {
	return runtime.reconcileNetwork(ctx, canonicalPlan, digest, true)
}

func (runtime Runtime) ObserveNetwork(ctx context.Context, canonicalPlan []byte, digest string) (RuntimeResult, error) {
	return runtime.reconcileNetwork(ctx, canonicalPlan, digest, false)
}

func (runtime Runtime) reconcileNetwork(ctx context.Context, canonicalPlan []byte, planDigest string, apply bool) (RuntimeResult, error) {
	if err := runtime.Config.validate(); err != nil {
		return RuntimeResult{}, err
	}
	_, plan, err := RestoreStoredNetworkPlan(canonicalPlan, planDigest)
	if err != nil {
		return RuntimeResult{}, err
	}
	for _, value := range []string{plan.OperationID, plan.ProjectID, plan.NetworkID, plan.AllocationID, plan.LogicalSwitchName} {
		if !safeOVNAtom.MatchString(value) {
			return RuntimeResult{}, errors.New("OVN Network plan contains an unsafe atom")
		}
	}
	runner := runtime.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	run := func(args ...string) ([]byte, error) {
		commandCtx, cancel := context.WithTimeout(ctx, runtime.Config.CommandTimeout)
		defer cancel()
		return runner.Run(commandCtx, runtime.Config.NBCTL, args...)
	}
	global := runtime.Config.globalArgs(runtime.Config.NBDatabase)
	read := func() (map[string]string, bool, string, string, error) {
		raw, err := run(withGlobal(global, "--format=json", "--columns=external_ids", "find", "Logical_Switch", "name="+plan.LogicalSwitchName)...)
		if err != nil {
			return nil, false, "", "", err
		}
		markers, present, err := parseExternalIDs(raw)
		if err != nil || !present {
			return markers, present, digestText(string(raw)), "", err
		}
		uuid, uuidErr := run(withGlobal(global, "--data=bare", "--no-heading", "--columns=_uuid", "find", "Logical_Switch", "name="+plan.LogicalSwitchName)...)
		return markers, present, digestText(string(raw)), normalizeOVSReference(string(uuid)), uuidErr
	}
	before, present, _, _, err := read()
	if err != nil {
		return RuntimeResult{}, err
	}
	if present && !sameNetworkIncarnation(before, plan) {
		return RuntimeResult{}, ErrForeignOVNObject
	}
	response := "UNKNOWN"
	if apply {
		response = "RECEIVED"
		if plan.DesiredState == "PRESENT" {
			args := append([]string{}, global...)
			args = append(args, "--may-exist", "ls-add", plan.LogicalSwitchName, "--", "set", "Logical_Switch", plan.LogicalSwitchName)
			args = append(args, markerAssignments(plan.ExpectedExternalIDs, planDigest)...)
			if _, err := run(args...); err != nil {
				response = "LOST"
			}
		} else if present {
			if _, err := run(withGlobal(global, "--if-exists", "ls-del", plan.LogicalSwitchName)...); err != nil {
				response = "LOST"
			}
		}
	}
	after, afterPresent, observationDigest, backendUUID, err := read()
	if err != nil {
		return RuntimeResult{}, err
	}
	observation := NetworkObservation{LogicalSwitchPresent: afterPresent,
		OwnershipMarkerMatches: markersPresent(after, plan.ExpectedExternalIDs, planDigest),
		PlanDigestMatches:      after["kim.object_set_digest"] == planDigest,
		LogicalSwitchName:      plan.LogicalSwitchName, BackendUUID: backendUUID}
	return RuntimeResult{NetworkObservation: observation, ApplyResponseState: response,
		NBObservationDigest: observationDigest, SBObservationDigest: digestText(fmt.Sprintf("network:%s", plan.NetworkID))}, nil
}

func sameNetworkIncarnation(markers map[string]string, plan NetworkPlan) bool {
	return markers["kim.owner"] == "KIM" &&
		markers["kim.aggregate_type"] == "NETWORK" &&
		markers["kim.project_id"] == plan.ProjectID &&
		markers["kim.network_id"] == plan.NetworkID &&
		markers["kim.allocation_id"] == plan.AllocationID &&
		markers["kim.allocation_generation"] == strconv.FormatUint(plan.AllocationGeneration, 10) &&
		markers["kim.segment_type"] == plan.SegmentType &&
		markers["kim.segment_id"] == strconv.FormatUint(uint64(plan.SegmentID), 10)
}
