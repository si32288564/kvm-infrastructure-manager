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
	"net"
	"strconv"
	"strings"
)

const PortResourceIntentSchema = "kim.network-intent.ovn-port-resource/v1"

type PortResourceIntentInput struct {
	OperationID, ProjectID, NetworkID, PortID string
	MACAddress, IPAddress, ExpectedChassis    string
	OperationGeneration, NetworkRevision      uint64
	PortRevision, RealizationGeneration       uint64
	BindingGeneration                         uint64
	DesiredState                              string
}

type PortResourcePlan struct {
	SchemaVersion         string            `json:"schema_version"`
	OperationID           string            `json:"operation_id"`
	OperationGeneration   uint64            `json:"operation_generation"`
	ProjectID             string            `json:"project_id"`
	NetworkID             string            `json:"network_id"`
	NetworkRevision       uint64            `json:"network_revision"`
	PortID                string            `json:"port_id"`
	PortRevision          uint64            `json:"port_revision"`
	RealizationGeneration uint64            `json:"realization_generation"`
	BindingGeneration     uint64            `json:"binding_generation,omitempty"`
	LogicalSwitchName     string            `json:"logical_switch_name"`
	LogicalPortName       string            `json:"logical_port_name"`
	MACAddress            string            `json:"mac_address"`
	IPAddress             string            `json:"ip_address,omitempty"`
	ExpectedChassis       string            `json:"expected_chassis,omitempty"`
	DesiredState          string            `json:"desired_state"`
	NetworkExternalIDs    map[string]string `json:"network_external_ids"`
	PortExternalIDs       map[string]string `json:"port_external_ids"`
}

type PortResourceObservation struct {
	LogicalPortName                                          string
	BackendUUID                                              string
	ObjectPresent, OwnershipMarkerMatches, PlanDigestMatches bool
	NetworkMatches, MACMatches, IPMatches, BindingMatches    bool
}

func (o PortResourceObservation) State(desired string) string {
	if desired == "ABSENT" {
		if !o.ObjectPresent {
			return "ABSENT"
		}
		return "UNKNOWN"
	}
	if o.ObjectPresent && o.OwnershipMarkerMatches && o.PlanDigestMatches && o.NetworkMatches && o.MACMatches && o.IPMatches && o.BindingMatches {
		return "VERIFIED"
	}
	return "UNKNOWN"
}

func PlanPortResource(in PortResourceIntentInput) ([]byte, string, error) {
	if in.OperationID == "" || in.ProjectID == "" || in.NetworkID == "" || in.PortID == "" || in.OperationGeneration == 0 || in.NetworkRevision == 0 || in.PortRevision == 0 || in.RealizationGeneration == 0 || (in.DesiredState != "PRESENT" && in.DesiredState != "ABSENT") {
		return nil, "", errors.New("complete typed Port resource intent is required")
	}
	if _, err := net.ParseMAC(in.MACAddress); err != nil {
		return nil, "", errors.New("valid Port MAC is required")
	}
	if in.IPAddress != "" && net.ParseIP(in.IPAddress) == nil {
		return nil, "", errors.New("valid optional Port IP is required")
	}
	if (in.ExpectedChassis == "") != (in.BindingGeneration == 0) {
		return nil, "", errors.New("chassis and binding generation must be supplied together")
	}
	portMarkers := map[string]string{
		"kim.owner": "KIM", "kim.aggregate_type": "PORT_RESOURCE", "kim.port_id": in.PortID,
		"kim.port_revision": strconv.FormatUint(in.PortRevision, 10), "kim.network_id": in.NetworkID,
		"kim.network_revision": strconv.FormatUint(in.NetworkRevision, 10), "kim.realization_generation": strconv.FormatUint(in.RealizationGeneration, 10),
	}
	if in.BindingGeneration > 0 {
		portMarkers["kim.binding_generation"] = strconv.FormatUint(in.BindingGeneration, 10)
	}
	plan := PortResourcePlan{SchemaVersion: PortResourceIntentSchema, OperationID: in.OperationID, OperationGeneration: in.OperationGeneration,
		ProjectID: in.ProjectID, NetworkID: in.NetworkID, NetworkRevision: in.NetworkRevision, PortID: in.PortID, PortRevision: in.PortRevision,
		RealizationGeneration: in.RealizationGeneration, BindingGeneration: in.BindingGeneration,
		LogicalSwitchName: objectName("kim-ls", in.NetworkID), LogicalPortName: BackendInterfaceID(in.PortID), MACAddress: in.MACAddress,
		IPAddress: in.IPAddress, ExpectedChassis: in.ExpectedChassis, DesiredState: in.DesiredState,
		NetworkExternalIDs: map[string]string{"kim.owner": "KIM", "kim.aggregate_type": "NETWORK", "kim.project_id": in.ProjectID, "kim.network_id": in.NetworkID, "kim.network_revision": strconv.FormatUint(in.NetworkRevision, 10)}, PortExternalIDs: portMarkers}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func RestoreStoredPortResourcePlan(raw []byte, digest string) ([]byte, PortResourcePlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var p PortResourcePlan
	if err := decoder.Decode(&p); err != nil {
		return nil, p, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, p, errors.New("Port resource plan contains trailing data")
	}
	canonical, err := json.Marshal(p)
	if err != nil {
		return nil, p, err
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != digest || p.SchemaVersion != PortResourceIntentSchema || p.OperationID == "" || p.OperationGeneration == 0 || p.NetworkRevision == 0 || p.PortRevision == 0 || p.RealizationGeneration == 0 || p.LogicalSwitchName != objectName("kim-ls", p.NetworkID) || p.LogicalPortName != BackendInterfaceID(p.PortID) || (p.DesiredState != "PRESENT" && p.DesiredState != "ABSENT") || p.PortExternalIDs["kim.owner"] != "KIM" || p.PortExternalIDs["kim.port_id"] != p.PortID {
		return nil, p, errors.New("invalid Port resource plan")
	}
	if _, err := net.ParseMAC(p.MACAddress); err != nil {
		return nil, p, err
	}
	if p.IPAddress != "" && net.ParseIP(p.IPAddress) == nil {
		return nil, p, errors.New("invalid Port IP")
	}
	if (p.ExpectedChassis == "") != (p.BindingGeneration == 0) {
		return nil, p, errors.New("invalid binding expectation")
	}
	return canonical, p, nil
}

func (runtime Runtime) ReconcilePortResource(ctx context.Context, raw []byte, digest string) (RuntimeResult, error) {
	return runtime.reconcilePortResource(ctx, raw, digest, true)
}
func (runtime Runtime) ObservePortResource(ctx context.Context, raw []byte, digest string) (RuntimeResult, error) {
	return runtime.reconcilePortResource(ctx, raw, digest, false)
}

func (runtime Runtime) reconcilePortResource(ctx context.Context, raw []byte, digest string, apply bool) (RuntimeResult, error) {
	if err := runtime.Config.validate(); err != nil {
		return RuntimeResult{}, err
	}
	_, p, err := RestoreStoredPortResourcePlan(raw, digest)
	if err != nil {
		return RuntimeResult{}, err
	}
	for _, v := range []string{p.OperationID, p.LogicalSwitchName, p.LogicalPortName} {
		if !safeOVNAtom.MatchString(v) {
			return RuntimeResult{}, errors.New("unsafe Port resource atom")
		}
	}
	runner := runtime.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	nb := runtime.Config.globalArgs(runtime.Config.NBDatabase)
	run := func(args ...string) ([]byte, error) {
		commandCtx, cancel := context.WithTimeout(ctx, runtime.Config.CommandTimeout)
		defer cancel()
		return runner.Run(commandCtx, runtime.Config.NBCTL, withGlobal(nb, args...)...)
	}
	read := func() (map[string]string, bool, string, string, string, error) {
		sw, err := run("--format=json", "--columns=external_ids", "find", "Logical_Switch", "name="+p.LogicalSwitchName)
		if err != nil {
			return nil, false, "", "", "", err
		}
		sm, sp, err := parseExternalIDs(sw)
		if err != nil || !sp || !markersPresent(sm, p.NetworkExternalIDs, "") {
			return nil, false, "", "", "", ErrForeignOVNObject
		}
		pr, err := run("--format=json", "--columns=external_ids", "find", "Logical_Switch_Port", "name="+p.LogicalPortName)
		if err != nil {
			return nil, false, "", "", "", err
		}
		pm, present, err := parseExternalIDs(pr)
		if err != nil {
			return nil, false, "", "", "", err
		}
		if !present {
			return pm, false, "", "", "", nil
		}
		addr, err := run("--if-exists", "get", "Logical_Switch_Port", p.LogicalPortName, "addresses")
		if err != nil {
			return nil, false, "", "", "", err
		}
		ch, err := run("--if-exists", "get", "Logical_Switch_Port", p.LogicalPortName, "options:requested-chassis")
		if err != nil {
			return nil, false, "", "", "", err
		}
		uuid, err := run("--data=bare", "--no-heading", "--columns=_uuid", "find", "Logical_Switch_Port", "name="+p.LogicalPortName)
		return pm, true, strings.TrimSpace(string(addr)), normalizeOVSReference(string(ch)), normalizeOVSReference(string(uuid)), err
	}
	pm, present, _, _, _, err := read()
	if err != nil {
		return RuntimeResult{}, err
	}
	legacyConsumerTransition := pm["kim.owner"] == "KIM" && pm["kim.aggregate_type"] == "PORT" && pm["kim.port_id"] == p.PortID && pm["kim.network_id"] == p.NetworkID
	if present && !markersPresent(pm, p.PortExternalIDs, digest) && !legacyConsumerTransition {
		return RuntimeResult{}, ErrForeignOVNObject
	}
	state := "UNKNOWN"
	if apply {
		state = "RECEIVED"
		var args []string
		if p.DesiredState == "ABSENT" {
			args = []string{"--if-exists", "lsp-del", p.LogicalPortName}
		} else {
			addresses := p.MACAddress
			if p.IPAddress != "" {
				addresses += " " + p.IPAddress
			}
			args = []string{"--may-exist", "lsp-add", p.LogicalSwitchName, p.LogicalPortName, "--", "lsp-set-addresses", p.LogicalPortName, addresses, "--", "set", "Logical_Switch_Port", p.LogicalPortName}
			if p.ExpectedChassis != "" {
				args = append(args, "options:requested-chassis="+p.ExpectedChassis)
			} else {
				args = append(args, "options:requested-chassis=[]")
			}
			args = append(args, markerAssignments(p.PortExternalIDs, digest)...)
		}
		if _, err := run(args...); err != nil {
			state = "LOST"
		}
	}
	pm, present, addresses, chassis, uuid, err := read()
	if err != nil {
		return RuntimeResult{}, err
	}
	expected := p.MACAddress
	if p.IPAddress != "" {
		expected += " " + p.IPAddress
	}
	clean := strings.Trim(strings.TrimSpace(addresses), "[]\"")
	bindingOK := (p.ExpectedChassis == "" && chassis == "") || chassis == p.ExpectedChassis
	o := PortResourceObservation{LogicalPortName: p.LogicalPortName, BackendUUID: uuid, ObjectPresent: present, OwnershipMarkerMatches: present && markersPresent(pm, p.PortExternalIDs, digest), PlanDigestMatches: present && pm["kim.object_set_digest"] == digest, NetworkMatches: true, MACMatches: present && strings.Contains(clean, p.MACAddress), IPMatches: p.IPAddress == "" || strings.Contains(clean, p.IPAddress), BindingMatches: bindingOK}
	if p.DesiredState == "ABSENT" {
		o.NetworkMatches = true
		o.MACMatches = true
		o.IPMatches = true
		o.BindingMatches = true
	}
	return RuntimeResult{PortResourceObservation: o, ApplyResponseState: state, NBObservationDigest: digestText(fmt.Sprintf("%v\x00%s\x00%s", pm, addresses, chassis))}, nil
}
