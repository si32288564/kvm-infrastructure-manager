package ovnadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var safeOVNAtom = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,256}$`)

var ErrForeignOVNObject = errors.New("foreign or conflicting OVN object")

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type RuntimeConfig struct {
	NBDatabase, SBDatabase                      string
	NBCTL, SBCTL, OVSCTL                        string
	PrivateKeyPath, CertificatePath, CACertPath string
	CommandTimeout                              time.Duration
}

type RuntimeResult struct {
	Observation           Observation
	RetirementObservation PortBindingRetirementObservation
	ApplyResponseState    string
	NBObservationDigest   string
	SBObservationDigest   string
	OVSObservationDigest  string
	ChassisIdentityDigest string
}

type Runtime struct {
	Config RuntimeConfig
	Runner CommandRunner
}

func (runtime Runtime) ReconcilePort(ctx context.Context, canonicalPlan []byte, objectSetDigest string) (RuntimeResult, error) {
	return runtime.reconcilePort(ctx, canonicalPlan, objectSetDigest, true)
}

// ObservePort performs the same typed ownership and NB/SB read-back as
// ReconcilePort without issuing an OVN mutation. It is the mandatory first
// step after an expired or otherwise uncertain worker claim.
func (runtime Runtime) ObservePort(ctx context.Context, canonicalPlan []byte, objectSetDigest string) (RuntimeResult, error) {
	return runtime.reconcilePort(ctx, canonicalPlan, objectSetDigest, false)
}

func (runtime Runtime) RetirePortBinding(ctx context.Context, canonicalPlan []byte, objectSetDigest string) (RuntimeResult, error) {
	return runtime.retirePortBinding(ctx, canonicalPlan, objectSetDigest, true)
}

func (runtime Runtime) ObservePortBindingRetirement(ctx context.Context, canonicalPlan []byte, objectSetDigest string) (RuntimeResult, error) {
	return runtime.retirePortBinding(ctx, canonicalPlan, objectSetDigest, false)
}

func (runtime Runtime) retirePortBinding(ctx context.Context, canonicalPlan []byte, objectSetDigest string, apply bool) (RuntimeResult, error) {
	if err := runtime.Config.validate(); err != nil {
		return RuntimeResult{}, err
	}
	if !filepath.IsAbs(runtime.Config.OVSCTL) || filepath.Base(runtime.Config.OVSCTL) != "ovs-vsctl" {
		return RuntimeResult{}, errors.New("only standard ovs-vsctl is allowed for Port retirement observation")
	}
	canonical, plan, err := RestoreStoredPortBindingRetirementPlan(canonicalPlan, objectSetDigest)
	if err != nil || len(canonical) == 0 {
		return RuntimeResult{}, err
	}
	for _, value := range []string{plan.OperationID, plan.PortID, plan.SourceHostID, plan.SourceOVNChassisName, plan.LogicalSwitchName, plan.LogicalPortName} {
		if !safeOVNAtom.MatchString(value) {
			return RuntimeResult{}, errors.New("OVN Port retirement plan contains an unsafe atom")
		}
	}
	runner := runtime.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	run := func(name string, args ...string) ([]byte, error) {
		commandCtx, cancel := context.WithTimeout(ctx, runtime.Config.CommandTimeout)
		defer cancel()
		return runner.Run(commandCtx, name, args...)
	}
	nbGlobal := runtime.Config.globalArgs(runtime.Config.NBDatabase)
	sbGlobal := runtime.Config.globalArgs(runtime.Config.SBDatabase)
	readMarkers := func() (map[string]string, bool, map[string]string, bool, error) {
		switchRaw, switchErr := run(runtime.Config.NBCTL, withGlobal(nbGlobal, "--format=json", "--columns=external_ids", "find", "Logical_Switch", "name="+plan.LogicalSwitchName)...)
		portRaw, portErr := run(runtime.Config.NBCTL, withGlobal(nbGlobal, "--format=json", "--columns=external_ids", "find", "Logical_Switch_Port", "name="+plan.LogicalPortName)...)
		if switchErr != nil || portErr != nil {
			return nil, false, nil, false, errors.Join(switchErr, portErr)
		}
		switchMarkers, switchPresent, err := parseExternalIDs(switchRaw)
		if err != nil {
			return nil, false, nil, false, err
		}
		portMarkers, portPresent, err := parseExternalIDs(portRaw)
		return switchMarkers, switchPresent, portMarkers, portPresent, err
	}
	beforeSwitch, beforeSwitchPresent, beforePort, beforePortPresent, err := readMarkers()
	if err != nil {
		return RuntimeResult{}, err
	}
	if !beforeSwitchPresent || !beforePortPresent || !markersPresent(beforeSwitch, plan.ExpectedNetworkExternalIDs, "") || !markersPresent(beforePort, plan.ExpectedPortExternalIDs, plan.ExpectedObjectSetDigest) {
		return RuntimeResult{}, ErrForeignOVNObject
	}
	applyResponseState := "UNKNOWN"
	if apply {
		applyResponseState = "RECEIVED"
		if _, err := run(runtime.Config.NBCTL, withGlobal(nbGlobal, "remove", "Logical_Switch_Port", plan.LogicalPortName, "options", "requested-chassis")...); err != nil {
			applyResponseState = "LOST"
		}
	}
	afterSwitch, switchPresent, afterPort, portPresent, err := readMarkers()
	if err != nil {
		return RuntimeResult{}, err
	}
	requestedChassis, err := run(runtime.Config.NBCTL, withGlobal(nbGlobal, "--if-exists", "get", "Logical_Switch_Port", plan.LogicalPortName, "options:requested-chassis")...)
	if err != nil {
		return RuntimeResult{}, err
	}
	chassisReference, err := run(runtime.Config.SBCTL, withGlobal(sbGlobal, "--data=bare", "--no-heading", "--columns=chassis", "find", "Port_Binding", "logical_port="+plan.LogicalPortName)...)
	if err != nil {
		return RuntimeResult{}, err
	}
	observedChassis := ""
	if chassisUUID := normalizeOVSReference(string(chassisReference)); chassisUUID != "" {
		name, err := run(runtime.Config.SBCTL, withGlobal(sbGlobal, "--if-exists", "get", "Chassis", chassisUUID, "name")...)
		if err != nil {
			return RuntimeResult{}, err
		}
		observedChassis = strings.Trim(strings.TrimSpace(string(name)), "\"")
	}
	interfaces, err := run(runtime.Config.OVSCTL, "--data=bare", "--no-heading", "--columns=name", "find", "Interface", "external_ids:iface-id="+plan.LogicalPortName)
	if err != nil {
		return RuntimeResult{}, err
	}
	requested := normalizeOVSReference(string(requestedChassis))
	interfaceName := strings.TrimSpace(string(interfaces))
	observation := PortBindingRetirementObservation{
		OwnershipMarkerMatches: markersPresent(afterSwitch, plan.ExpectedNetworkExternalIDs, "") && markersPresent(afterPort, plan.ExpectedPortExternalIDs, plan.ExpectedObjectSetDigest),
		ObjectSetDigestMatches: afterPort["kim.object_set_digest"] == plan.ExpectedObjectSetDigest,
		LogicalSwitchPresent:   switchPresent, LogicalSwitchPortPresent: portPresent,
		RequestedChassisAbsent: requested == "", SourceChassisInactive: observedChassis != plan.SourceOVNChassisName,
		SourceOVSInterfaceAbsent: interfaceName == "",
	}
	return RuntimeResult{RetirementObservation: observation, ApplyResponseState: applyResponseState,
		NBObservationDigest:  digestText(fmt.Sprintf("%v\x00%v\x00%s", afterSwitch, afterPort, requested)),
		SBObservationDigest:  digestText(string(chassisReference) + "\x00" + observedChassis),
		OVSObservationDigest: digestText(interfaceName), ChassisIdentityDigest: digestText(observedChassis)}, nil
}

func (runtime Runtime) reconcilePort(ctx context.Context, canonicalPlan []byte, objectSetDigest string, apply bool) (RuntimeResult, error) {
	if err := runtime.Config.validate(); err != nil {
		return RuntimeResult{}, err
	}
	plan, err := DecodePortPlan(canonicalPlan, objectSetDigest)
	if err != nil {
		return RuntimeResult{}, err
	}
	if err := validateRuntimePlan(plan); err != nil {
		return RuntimeResult{}, err
	}
	runner := runtime.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	run := func(name string, args ...string) ([]byte, error) {
		commandCtx, cancel := context.WithTimeout(ctx, runtime.Config.CommandTimeout)
		defer cancel()
		return runner.Run(commandCtx, name, args...)
	}
	nbGlobal := runtime.Config.globalArgs(runtime.Config.NBDatabase)
	sbGlobal := runtime.Config.globalArgs(runtime.Config.SBDatabase)

	beforeSwitch, err := run(runtime.Config.NBCTL, withGlobal(nbGlobal,
		"--format=json", "--columns=external_ids", "find", "Logical_Switch", "name="+plan.LogicalSwitch.Name)...)
	if err != nil {
		return RuntimeResult{}, fmt.Errorf("read OVN Logical Switch before apply: %w", err)
	}
	beforeSwitchMarkers, switchPresent, err := parseExternalIDs(beforeSwitch)
	if err != nil {
		return RuntimeResult{}, fmt.Errorf("parse OVN Logical Switch before apply: %w", err)
	}
	if switchPresent && !markersPresent(beforeSwitchMarkers, plan.NetworkExternalIDs, "") {
		return RuntimeResult{}, ErrForeignOVNObject
	}
	beforePort, err := run(runtime.Config.NBCTL, withGlobal(nbGlobal,
		"--format=json", "--columns=external_ids", "find", "Logical_Switch_Port", "name="+plan.LogicalPort.Name)...)
	if err != nil {
		return RuntimeResult{}, fmt.Errorf("read OVN Logical Switch Port before apply: %w", err)
	}
	beforePortMarkers, portPresent, err := parseExternalIDs(beforePort)
	if err != nil {
		return RuntimeResult{}, fmt.Errorf("parse OVN Logical Switch Port before apply: %w", err)
	}
	if portPresent && !markersPresent(beforePortMarkers, plan.PortExternalIDs, objectSetDigest) {
		return RuntimeResult{}, ErrForeignOVNObject
	}

	applyResponseState := "UNKNOWN"
	if apply {
		applyArgs := append([]string{}, nbGlobal...)
		applyArgs = append(applyArgs, "--may-exist", "ls-add", plan.LogicalSwitch.Name, "--", "set", "Logical_Switch", plan.LogicalSwitch.Name)
		applyArgs = append(applyArgs, markerAssignments(plan.NetworkExternalIDs, "")...)
		applyArgs = append(applyArgs, "--", "--may-exist", "lsp-add", plan.LogicalSwitch.Name, plan.LogicalPort.Name,
			"--", "lsp-set-addresses", plan.LogicalPort.Name, plan.LogicalPort.MACAddress+" "+plan.LogicalPort.IPAddress,
			"--", "set", "Logical_Switch_Port", plan.LogicalPort.Name, "options:requested-chassis="+plan.LogicalPort.OVNChassisName)
		applyArgs = append(applyArgs, markerAssignments(plan.PortExternalIDs, objectSetDigest)...)
		applyResponseState = "RECEIVED"
		if _, err := run(runtime.Config.NBCTL, applyArgs...); err != nil {
			// A failed command response is an unknown apply outcome. Always read back
			// the stable object names and ownership markers before deciding.
			applyResponseState = "LOST"
		}
	}

	nbSwitch, switchErr := run(runtime.Config.NBCTL, withGlobal(nbGlobal,
		"--format=json", "--columns=external_ids", "find", "Logical_Switch", "name="+plan.LogicalSwitch.Name)...)
	nbPort, portErr := run(runtime.Config.NBCTL, withGlobal(nbGlobal,
		"--format=json", "--columns=external_ids", "find", "Logical_Switch_Port", "name="+plan.LogicalPort.Name)...)
	if switchErr != nil || portErr != nil {
		return RuntimeResult{}, errors.Join(switchErr, portErr)
	}
	nbSwitchMarkers, logicalSwitchPresent, err := parseExternalIDs(nbSwitch)
	if err != nil {
		return RuntimeResult{}, err
	}
	nbPortMarkers, logicalPortPresent, err := parseExternalIDs(nbPort)
	if err != nil {
		return RuntimeResult{}, err
	}
	ownershipMatches := markersPresent(nbSwitchMarkers, plan.NetworkExternalIDs, "") && markersPresent(nbPortMarkers, plan.PortExternalIDs, objectSetDigest)
	objectDigestMatches := nbPortMarkers["kim.object_set_digest"] == objectSetDigest

	sbBinding, sbErr := run(runtime.Config.SBCTL, withGlobal(sbGlobal,
		"--data=bare", "--no-heading", "--columns=datapath", "find", "Port_Binding", "logical_port="+plan.LogicalPort.Name)...)
	if sbErr != nil {
		return RuntimeResult{}, sbErr
	}
	chassisReference, chassisErr := run(runtime.Config.SBCTL, withGlobal(sbGlobal,
		"--data=bare", "--no-heading", "--columns=chassis", "find", "Port_Binding", "logical_port="+plan.LogicalPort.Name)...)
	if chassisErr != nil {
		return RuntimeResult{}, chassisErr
	}
	datapathUUID := normalizeOVSReference(string(sbBinding))
	chassisUUID := normalizeOVSReference(string(chassisReference))
	observedChassis := ""
	if chassisUUID != "" {
		name, err := run(runtime.Config.SBCTL, withGlobal(sbGlobal,
			"--if-exists", "get", "Chassis", chassisUUID, "name")...)
		if err != nil {
			return RuntimeResult{}, err
		}
		observedChassis = strings.Trim(strings.TrimSpace(string(name)), "\"")
	}
	observation := Observation{
		OwnershipMarkerMatches: ownershipMatches, ObjectSetDigestMatches: objectDigestMatches,
		LogicalSwitchPresent:     logicalSwitchPresent,
		LogicalSwitchPortPresent: logicalPortPresent,
		PortBindingPresent:       datapathUUID != "" || chassisUUID != "",
		DatapathPresent:          datapathUUID != "",
		ExpectedChassisMatches:   observedChassis == plan.LogicalPort.OVNChassisName,
	}
	return RuntimeResult{Observation: observation, ApplyResponseState: applyResponseState,
		NBObservationDigest:   digestText(string(nbSwitch) + "\x00" + string(nbPort)),
		SBObservationDigest:   digestText(string(sbBinding) + "\x00" + observedChassis),
		ChassisIdentityDigest: digestText(observedChassis)}, nil
}

func (config RuntimeConfig) validate() error {
	if config.NBCTL == "" || config.SBCTL == "" || config.CommandTimeout < time.Second || config.CommandTimeout > 2*time.Minute {
		return errors.New("bounded OVN runtime command configuration is required")
	}
	if !filepath.IsAbs(config.NBCTL) || !filepath.IsAbs(config.SBCTL) || filepath.Base(config.NBCTL) != "ovn-nbctl" || filepath.Base(config.SBCTL) != "ovn-sbctl" {
		return errors.New("only standard ovn-nbctl/ovn-sbctl executables are allowed")
	}
	for _, database := range []string{config.NBDatabase, config.SBDatabase} {
		if !strings.HasPrefix(database, "unix:") && !strings.HasPrefix(database, "ssl:") {
			return errors.New("OVN database endpoint must use unix or ssl transport")
		}
	}
	if strings.HasPrefix(config.NBDatabase, "ssl:") || strings.HasPrefix(config.SBDatabase, "ssl:") {
		if config.PrivateKeyPath == "" || config.CertificatePath == "" || config.CACertPath == "" {
			return errors.New("OVN ssl endpoint requires private key, certificate, and CA paths")
		}
	}
	return nil
}

func (config RuntimeConfig) globalArgs(database string) []string {
	args := []string{"--db=" + database, "--timeout=" + strconv.Itoa(int(config.CommandTimeout.Seconds()))}
	if strings.HasPrefix(database, "ssl:") {
		args = append(args, "--private-key="+config.PrivateKeyPath, "--certificate="+config.CertificatePath, "--ca-cert="+config.CACertPath)
	}
	return args
}

func withGlobal(global []string, arguments ...string) []string {
	combined := make([]string, 0, len(global)+len(arguments))
	combined = append(combined, global...)
	return append(combined, arguments...)
}

func validateRuntimePlan(plan PortPlan) error {
	values := []string{plan.LogicalSwitch.Name, plan.LogicalPort.Name, plan.LogicalPort.OVNChassisName, plan.LogicalPort.MACAddress, plan.LogicalPort.IPAddress}
	for key, value := range plan.NetworkExternalIDs {
		values = append(values, key, value)
	}
	for key, value := range plan.PortExternalIDs {
		values = append(values, key, value)
	}
	for _, value := range values {
		if !safeOVNAtom.MatchString(value) {
			return errors.New("OVN runtime plan contains an unsafe atom")
		}
	}
	return nil
}

func markerAssignments(markers map[string]string, objectSetDigest string) []string {
	keys := make([]string, 0, len(markers)+1)
	for key := range markers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assignments := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		assignments = append(assignments, "external_ids:"+key+"="+markers[key])
	}
	if objectSetDigest != "" {
		assignments = append(assignments, "external_ids:kim.object_set_digest="+objectSetDigest)
	}
	return assignments
}

func markersPresent(observed map[string]string, markers map[string]string, objectSetDigest string) bool {
	for key, value := range markers {
		if observed[key] != value {
			return false
		}
	}
	return objectSetDigest == "" || observed["kim.object_set_digest"] == objectSetDigest
}

func parseExternalIDs(raw []byte) (map[string]string, bool, error) {
	var table struct {
		Data [][]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		return nil, false, err
	}
	if len(table.Data) == 0 {
		return map[string]string{}, false, nil
	}
	if len(table.Data) != 1 || len(table.Data[0]) != 1 {
		return nil, false, errors.New("OVN ownership query returned an ambiguous object set")
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(table.Data[0][0], &encoded); err != nil || len(encoded) != 2 {
		return nil, false, errors.New("invalid OVN external_ids encoding")
	}
	var kind string
	var pairs [][]string
	if err := json.Unmarshal(encoded[0], &kind); err != nil || kind != "map" {
		return nil, false, errors.New("invalid OVN external_ids map kind")
	}
	if err := json.Unmarshal(encoded[1], &pairs); err != nil {
		return nil, false, err
	}
	markers := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		if len(pair) != 2 {
			return nil, false, errors.New("invalid OVN external_ids pair")
		}
		if _, duplicate := markers[pair[0]]; duplicate {
			return nil, false, errors.New("duplicate OVN external_ids key")
		}
		markers[pair[0]] = pair[1]
	}
	return markers, true, nil
}

func normalizeOVSReference(raw string) string {
	value := strings.Trim(strings.TrimSpace(raw), "[] \"\n\t")
	if value == "" || value == "[]" {
		return ""
	}
	return value
}

func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
