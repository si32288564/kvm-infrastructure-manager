// kim-real-kvm-recovery-authority-e2e binds an already-created, immutable
// PostgreSQL Command to the isolated real-KVM helper. It never accepts or
// prints a Lease token; the random capability is created and consumed inside
// recoveryauthority.Execute.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/qualification/recoveryauthority"
)

type request struct {
	CommandID      string `json:"command_id"`
	MessageID      string `json:"message_id"`
	VerificationID string `json:"verification_id"`
	Host           string `json:"host"`
	HelperPath     string `json:"helper_path"`
	VGName         string `json:"vg_name"`
	VGUUID         string `json:"vg_uuid"`
	CacheRoot      string `json:"cache_root"`
	StateRoot      string `json:"state_root"`
	OVSBridge      string `json:"ovs_bridge,omitempty"`
}

func main() {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		log.Fatal("KIM_POSTGRES_TEST_URL is required")
	}
	var desired request
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&desired); err != nil {
		log.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		log.Fatal("trailing request data")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 4)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	accepted, err := recoveryauthority.Execute(ctx, pool, recoveryauthority.RemoteConfig{
		Host: desired.Host, HelperPath: desired.HelperPath, VGName: desired.VGName,
		VGUUID: desired.VGUUID, CacheRoot: desired.CacheRoot, StateRoot: desired.StateRoot,
		OVSBridge: desired.OVSBridge,
	}, desired.CommandID, desired.MessageID, desired.VerificationID, 2*time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(accepted); err != nil {
		log.Fatal(err)
	}
}
