package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type fixtureState struct {
	LogicalSwitchName string            `json:"logical_switch_name"`
	LogicalPortName   string            `json:"logical_port_name"`
	NetworkMarkers    map[string]string `json:"network_markers"`
	PortMarkers       map[string]string `json:"port_markers"`
	ChassisName       string            `json:"chassis_name"`
	Applied           bool              `json:"applied"`
	ApplyCount        int               `json:"apply_count"`
}

func main() {
	statePath := os.Getenv("KIM_OVN_FIXTURE_STATE")
	if statePath == "" {
		fatal(errors.New("KIM_OVN_FIXTURE_STATE is required"))
	}
	state, err := readState(statePath)
	if err != nil {
		fatal(err)
	}
	arguments := os.Args[1:]
	program := filepath.Base(os.Args[0])
	if program == "ovn-nbctl" {
		err = runNB(statePath, &state, arguments)
	} else if program == "ovn-sbctl" {
		err = runSB(state, arguments)
	} else {
		err = fmt.Errorf("unsupported fixture executable %q", program)
	}
	if err != nil {
		fatal(err)
	}
}

func runNB(statePath string, state *fixtureState, arguments []string) error {
	if contains(arguments, "ls-add") && contains(arguments, "lsp-add") {
		state.Applied = true
		state.ApplyCount++
		return writeState(statePath, *state)
	}
	if contains(arguments, "find") && contains(arguments, "Logical_Switch") {
		if state.Applied {
			if err := blockReadBackIfRequested(); err != nil {
				return err
			}
			return writeMarkerOutput(state.NetworkMarkers)
		}
		return writeEmptyMarkerOutput()
	}
	if contains(arguments, "find") && contains(arguments, "Logical_Switch_Port") {
		if state.Applied {
			return writeMarkerOutput(state.PortMarkers)
		}
		return writeEmptyMarkerOutput()
	}
	return fmt.Errorf("unsupported ovn-nbctl fixture arguments: %v", arguments)
}

func runSB(state fixtureState, arguments []string) error {
	if !state.Applied {
		return nil
	}
	if contains(arguments, "Port_Binding") && containsPrefix(arguments, "--columns=datapath") {
		fmt.Print("datapath-fixture")
		return nil
	}
	if contains(arguments, "Port_Binding") && containsPrefix(arguments, "--columns=chassis") {
		fmt.Print("chassis-fixture-uuid")
		return nil
	}
	if contains(arguments, "Chassis") && contains(arguments, "name") {
		encoded, _ := json.Marshal(state.ChassisName)
		fmt.Print(string(encoded))
		return nil
	}
	return fmt.Errorf("unsupported ovn-sbctl fixture arguments: %v", arguments)
}

func blockReadBackIfRequested() error {
	if os.Getenv("KIM_OVN_FIXTURE_BLOCK_READBACK") != "1" {
		return nil
	}
	signalPath := os.Getenv("KIM_OVN_FIXTURE_READBACK_SIGNAL")
	if signalPath == "" {
		return errors.New("read-back signal path is required")
	}
	if err := os.WriteFile(signalPath, []byte("started\n"), 0o600); err != nil {
		return err
	}
	if releasePath := os.Getenv("KIM_OVN_FIXTURE_READBACK_RELEASE"); releasePath != "" {
		for {
			if _, err := os.Stat(releasePath); err == nil {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	parent := os.Getppid()
	for os.Getppid() == parent && os.Getppid() != 1 {
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("parent worker stopped during read-back")
}

func writeMarkerOutput(markers map[string]string) error {
	keys := make([]string, 0, len(markers))
	for key := range markers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([][]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, []string{key, markers[key]})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"data": []any{[]any{[]any{"map", pairs}}}, "headings": []string{"external_ids"}})
}

func writeEmptyMarkerOutput() error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"data": []any{}, "headings": []string{"external_ids"}})
}

func readState(path string) (fixtureState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fixtureState{}, err
	}
	var state fixtureState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fixtureState{}, err
	}
	return state, nil
}

func writeState(path string, state fixtureState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsPrefix(values []string, expected string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, expected) {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
