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
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

const SubnetIntentSchema = "kim.network-intent.ovn-subnet/v1"

type SubnetIntentInput struct {
	OperationID, ProjectID, NetworkID, SubnetID, CIDR, GatewayAddress string
	DNSServiceAddresses                                               []string
	OperationGeneration, SubnetRevision, NetworkRevision              uint64
	RealizationGeneration                                             uint64
	DHCPEnabled                                                       bool
	DesiredState                                                      string
}

type SubnetPlan struct {
	SchemaVersion         string            `json:"schema_version"`
	OperationID           string            `json:"operation_id"`
	OperationGeneration   uint64            `json:"operation_generation"`
	ProjectID             string            `json:"project_id"`
	NetworkID             string            `json:"network_id"`
	NetworkRevision       uint64            `json:"network_revision"`
	SubnetID              string            `json:"subnet_id"`
	SubnetRevision        uint64            `json:"subnet_revision"`
	RealizationGeneration uint64            `json:"realization_generation"`
	DesiredState          string            `json:"desired_state"`
	DHCPEnabled           bool              `json:"dhcp_enabled"`
	CIDR                  string            `json:"cidr"`
	GatewayAddress        string            `json:"gateway_address,omitempty"`
	DNSServiceAddresses   []string          `json:"dns_server_addresses"`
	LogicalSwitchName     string            `json:"logical_switch_name"`
	DHCPObjectName        string            `json:"dhcp_object_name"`
	ExpectedExternalIDs   map[string]string `json:"expected_external_ids"`
	ExpectedNetworkIDs    map[string]string `json:"expected_network_external_ids"`
}

type SubnetObservation struct {
	ObjectPresent, OwnershipMarkerMatches, PlanDigestMatches bool
	CIDRMatches, OptionsMatch, NetworkAssociationMatches     bool
	DHCPObjectName, BackendUUID                              string
}

func (observation SubnetObservation) State(plan SubnetPlan) string {
	if plan.DesiredState == "ABSENT" {
		if !observation.ObjectPresent && observation.NetworkAssociationMatches {
			return "ABSENT"
		}
	} else if !plan.DHCPEnabled {
		if !observation.ObjectPresent && observation.NetworkAssociationMatches {
			return "VERIFIED"
		}
	} else if observation.ObjectPresent && observation.OwnershipMarkerMatches && observation.PlanDigestMatches && observation.CIDRMatches && observation.OptionsMatch && observation.NetworkAssociationMatches {
		return "VERIFIED"
	}
	if observation.ObjectPresent && !observation.OwnershipMarkerMatches {
		return "CONFLICTING"
	}
	return "UNKNOWN"
}

func PlanSubnet(input SubnetIntentInput) ([]byte, string, error) {
	if input.OperationID == "" || input.ProjectID == "" || input.NetworkID == "" || input.SubnetID == "" || input.CIDR == "" || input.OperationGeneration == 0 || input.SubnetRevision == 0 || input.NetworkRevision == 0 || input.RealizationGeneration == 0 || (input.DesiredState != "PRESENT" && input.DesiredState != "ABSENT") {
		return nil, "", errors.New("complete typed OVN Subnet intent is required")
	}
	prefix, err := netip.ParsePrefix(input.CIDR)
	if err != nil || !prefix.Addr().Is4() || prefix.Masked().String() != input.CIDR {
		return nil, "", errors.New("canonical IPv4 Subnet CIDR is required")
	}
	if input.GatewayAddress != "" {
		gateway, parseErr := netip.ParseAddr(input.GatewayAddress)
		if parseErr != nil || !gateway.Is4() || !prefix.Contains(gateway) {
			return nil, "", errors.New("closed IPv4 gateway is required")
		}
	}
	for _, raw := range input.DNSServiceAddresses {
		address, parseErr := netip.ParseAddr(raw)
		if parseErr != nil || !address.Is4() {
			return nil, "", errors.New("closed IPv4 DNS address is required")
		}
	}
	dns := append([]string(nil), input.DNSServiceAddresses...)
	sort.Strings(dns)
	dhcpName := objectName("kim-dhcp", input.SubnetID)
	markers := map[string]string{"kim.owner": "KIM", "kim.aggregate_type": "SUBNET", "kim.project_id": input.ProjectID,
		"kim.network_id": input.NetworkID, "kim.network_generation": strconv.FormatUint(input.NetworkRevision, 10),
		"kim.subnet_id": input.SubnetID, "kim.subnet_revision": strconv.FormatUint(input.SubnetRevision, 10),
		"kim.realization_generation": strconv.FormatUint(input.RealizationGeneration, 10), "kim.dhcp_object_name": dhcpName}
	networkMarkers := map[string]string{"kim.owner": "KIM", "kim.aggregate_type": "NETWORK", "kim.project_id": input.ProjectID,
		"kim.network_id": input.NetworkID, "kim.network_generation": strconv.FormatUint(input.NetworkRevision, 10)}
	plan := SubnetPlan{SchemaVersion: SubnetIntentSchema, OperationID: input.OperationID, OperationGeneration: input.OperationGeneration,
		ProjectID: input.ProjectID, NetworkID: input.NetworkID, NetworkRevision: input.NetworkRevision, SubnetID: input.SubnetID,
		SubnetRevision: input.SubnetRevision, RealizationGeneration: input.RealizationGeneration, DesiredState: input.DesiredState,
		DHCPEnabled: input.DHCPEnabled, CIDR: input.CIDR, GatewayAddress: input.GatewayAddress, DNSServiceAddresses: dns,
		LogicalSwitchName: objectName("kim-ls", input.NetworkID), DHCPObjectName: dhcpName,
		ExpectedExternalIDs: markers, ExpectedNetworkIDs: networkMarkers}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func RestoreStoredSubnetPlan(raw []byte, expectedDigest string) ([]byte, SubnetPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan SubnetPlan
	if err := decoder.Decode(&plan); err != nil {
		return nil, plan, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, plan, errors.New("OVN Subnet plan contains trailing data")
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, plan, err
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != expectedDigest || plan.SchemaVersion != SubnetIntentSchema || plan.OperationID == "" || plan.OperationGeneration == 0 || plan.ProjectID == "" || plan.NetworkID == "" || plan.NetworkRevision == 0 || plan.SubnetID == "" || plan.SubnetRevision == 0 || plan.RealizationGeneration == 0 || plan.CIDR == "" || (plan.DesiredState != "PRESENT" && plan.DesiredState != "ABSENT") || plan.LogicalSwitchName != objectName("kim-ls", plan.NetworkID) || plan.DHCPObjectName != objectName("kim-dhcp", plan.SubnetID) || plan.ExpectedExternalIDs["kim.owner"] != "KIM" || plan.ExpectedExternalIDs["kim.subnet_id"] != plan.SubnetID || plan.ExpectedNetworkIDs["kim.network_id"] != plan.NetworkID || plan.ExpectedNetworkIDs["kim.network_generation"] != strconv.FormatUint(plan.NetworkRevision, 10) {
		return nil, plan, errors.New("invalid stored OVN Subnet plan")
	}
	return canonical, plan, nil
}

func (runtime Runtime) ReconcileSubnet(ctx context.Context, raw []byte, digest string) (RuntimeResult, error) {
	return runtime.reconcileSubnet(ctx, raw, digest, true)
}

func (runtime Runtime) ObserveSubnet(ctx context.Context, raw []byte, digest string) (RuntimeResult, error) {
	return runtime.reconcileSubnet(ctx, raw, digest, false)
}

type subnetBackendState struct {
	networkMarkers, objectMarkers  map[string]string
	networkPresent, objectPresent  bool
	backendUUID, cidr, router, dns string
	observationDigest              string
}

func (runtime Runtime) reconcileSubnet(ctx context.Context, raw []byte, planDigest string, apply bool) (RuntimeResult, error) {
	if err := runtime.Config.validate(); err != nil {
		return RuntimeResult{}, err
	}
	_, plan, err := RestoreStoredSubnetPlan(raw, planDigest)
	if err != nil {
		return RuntimeResult{}, err
	}
	for _, value := range []string{plan.OperationID, plan.ProjectID, plan.NetworkID, plan.SubnetID, plan.LogicalSwitchName, plan.DHCPObjectName} {
		if !safeOVNAtom.MatchString(value) {
			return RuntimeResult{}, errors.New("OVN Subnet plan contains an unsafe atom")
		}
	}
	runner := runtime.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	run := func(args ...string) ([]byte, error) {
		commandContext, cancel := context.WithTimeout(ctx, runtime.Config.CommandTimeout)
		defer cancel()
		return runner.Run(commandContext, runtime.Config.NBCTL, args...)
	}
	global := runtime.Config.globalArgs(runtime.Config.NBDatabase)
	read := func() (subnetBackendState, error) {
		var state subnetBackendState
		networkRaw, err := run(withGlobal(global, "--format=json", "--columns=external_ids", "find", "Logical_Switch", "name="+plan.LogicalSwitchName)...)
		if err != nil {
			return state, err
		}
		state.networkMarkers, state.networkPresent, err = parseExternalIDs(networkRaw)
		if err != nil {
			return state, err
		}
		objectRaw, err := run(withGlobal(global, "--format=json", "--columns=external_ids", "find", "DHCP_Options", "external_ids:kim.dhcp_object_name="+plan.DHCPObjectName)...)
		if err != nil {
			return state, err
		}
		state.objectMarkers, state.objectPresent, err = parseExternalIDs(objectRaw)
		state.observationDigest = digestText(string(networkRaw) + string(objectRaw))
		if err != nil || !state.objectPresent {
			return state, err
		}
		uuid, err := run(withGlobal(global, "--data=bare", "--no-heading", "--columns=_uuid", "find", "DHCP_Options", "external_ids:kim.dhcp_object_name="+plan.DHCPObjectName)...)
		if err != nil {
			return state, err
		}
		state.backendUUID = normalizeOVSReference(string(uuid))
		for _, field := range []struct {
			target *string
			column string
		}{{&state.cidr, "cidr"}, {&state.router, "options:router"}, {&state.dns, "options:dns_server"}} {
			value, readErr := run(withGlobal(global, "--if-exists", "get", "DHCP_Options", state.backendUUID, field.column)...)
			if readErr != nil {
				return state, readErr
			}
			*field.target = strings.Trim(strings.TrimSpace(string(value)), `"`)
		}
		state.observationDigest = digestText(state.observationDigest + state.backendUUID + state.cidr + state.router + state.dns)
		return state, nil
	}
	before, err := read()
	if err != nil {
		return RuntimeResult{}, err
	}
	if before.objectPresent && !sameSubnetIncarnation(before.objectMarkers, plan) {
		return RuntimeResult{}, ErrForeignOVNObject
	}
	response := "UNKNOWN"
	if apply {
		response = "RECEIVED"
		wantObject := plan.DesiredState == "PRESENT" && plan.DHCPEnabled
		if wantObject {
			args := append([]string{}, global...)
			if before.objectPresent {
				args = append(args, "clear", "DHCP_Options", before.backendUUID, "options", "--", "set", "DHCP_Options", before.backendUUID, "cidr="+plan.CIDR)
			} else {
				args = append(args, "create", "DHCP_Options", "cidr="+plan.CIDR)
			}
			args = append(args, markerAssignments(plan.ExpectedExternalIDs, planDigest)...)
			if plan.GatewayAddress != "" {
				args = append(args, "options:router="+plan.GatewayAddress)
			}
			if len(plan.DNSServiceAddresses) > 0 {
				args = append(args, "options:dns_server="+strings.Join(plan.DNSServiceAddresses, ","))
			}
			if _, err := run(args...); err != nil {
				response = "LOST"
			}
		} else if before.objectPresent {
			if _, err := run(withGlobal(global, "--if-exists", "destroy", "DHCP_Options", before.backendUUID)...); err != nil {
				response = "LOST"
			}
		}
	}
	after := before
	if apply {
		after, err = read()
		if err != nil {
			return RuntimeResult{}, err
		}
	}
	observation := SubnetObservation{ObjectPresent: after.objectPresent, OwnershipMarkerMatches: markersPresent(after.objectMarkers, plan.ExpectedExternalIDs, planDigest), PlanDigestMatches: after.objectMarkers["kim.object_set_digest"] == planDigest, CIDRMatches: after.cidr == plan.CIDR, OptionsMatch: after.router == plan.GatewayAddress && after.dns == strings.Join(plan.DNSServiceAddresses, ","), NetworkAssociationMatches: after.networkPresent && markersPresent(after.networkMarkers, plan.ExpectedNetworkIDs, ""), DHCPObjectName: plan.DHCPObjectName, BackendUUID: after.backendUUID}
	return RuntimeResult{SubnetObservation: observation, ApplyResponseState: response, NBObservationDigest: after.observationDigest, SBObservationDigest: digestText(fmt.Sprintf("subnet:%s", plan.SubnetID))}, nil
}

func sameSubnetIncarnation(markers map[string]string, plan SubnetPlan) bool {
	return markers["kim.owner"] == "KIM" && markers["kim.aggregate_type"] == "SUBNET" && markers["kim.project_id"] == plan.ProjectID && markers["kim.network_id"] == plan.NetworkID && markers["kim.subnet_id"] == plan.SubnetID && markers["kim.dhcp_object_name"] == plan.DHCPObjectName
}
