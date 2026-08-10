GO ?= go

.PHONY: all bench-agent-transport build check docs-check fmt fmt-check generate-agent-protocol scale-agent-transport test test-agent-grpc-reconnect-storm test-agent-reconnect-storm test-agent-transport test-linux-host-inventory test-p1b-full-process test-p1c03-ovn-worker-db-failover test-p1c03-ovn-worker-drain test-p1c03-ovn-worker-fault test-p1c03-ovn-worker-latency-saturation test-p1c03-ovn-worker-renewal-response-loss test-p1c03-ovn-worker-repeated-db-failover test-p1c03-ovn-worker-soak test-postgres-integration validate-linux-host-inventory vet

PROTOC_GEN_GO_VERSION := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

all: check build

build:
	$(GO) build ./cmd/...

check: fmt-check vet test docs-check

docs-check:
	$(GO) run ./cmd/kim-doc-lint -root .

fmt:
	gofmt -w cmd db internal

fmt-check:
	test -z "$$(gofmt -l cmd db internal)"

test:
	$(GO) test ./...

test-agent-transport:
	$(GO) test -count=1 ./internal/agent/session ./internal/agent/transport/...

test-linux-host-inventory:
	$(GO) test -count=1 ./internal/agent/inventory/...

validate-linux-host-inventory:
	$(GO) run ./cmd/kim-host-inventory-validate

bench-agent-transport:
	$(GO) test -run '^$$' -bench 'Benchmark(GRPC|HTTP2)RoundTrip' -benchmem -benchtime=500ms -count=3 ./internal/agent/transport/grpcstream ./internal/agent/transport/http2stream

scale-agent-transport:
	$(GO) run ./cmd/kim-agent-transport-scale -candidate grpc -sessions 1000
	$(GO) run ./cmd/kim-agent-transport-scale -candidate http2 -sessions 1000
	$(GO) run ./cmd/kim-agent-transport-scale -mode hol -candidate grpc
	$(GO) run ./cmd/kim-agent-transport-scale -mode hol -candidate http2

test-agent-reconnect-storm:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	$(GO) run ./cmd/kim-agent-reconnect-storm -database-url "$(KIM_POSTGRES_TEST_URL)"

test-agent-grpc-reconnect-storm:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	$(GO) run ./cmd/kim-agent-grpc-reconnect-storm -database-url "$(KIM_POSTGRES_TEST_URL)"

test-postgres-integration:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	KIM_POSTGRES_TEST_URL="$(KIM_POSTGRES_TEST_URL)" $(GO) test -count=1 -run TestMigratePostgreSQLIntegration ./internal/persistence/postgres

test-p1b-full-process:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	KIM_POSTGRES_TEST_URL="$(KIM_POSTGRES_TEST_URL)" $(GO) test -count=1 -timeout 240s -run TestFullProcessCommandDeliveryFaultCampaign ./internal/qualification/p1bfault

test-p1c03-ovn-worker-fault:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	KIM_POSTGRES_TEST_URL="$(KIM_POSTGRES_TEST_URL)" $(GO) test -count=1 -timeout 120s -run TestOVNRuntimeWorkerProcessKillReadBackConvergence ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-db-failover:
	KIM_RUN_DOCKER_POSTGRES_FAILOVER=1 $(GO) test -count=1 -timeout 300s -run TestOVNRuntimeWorkerPostgreSQLFailoverConvergence ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-renewal-response-loss:
	KIM_RUN_DOCKER_POSTGRES_RENEWAL_RESPONSE_LOSS=1 $(GO) test -count=1 -timeout 180s -run TestOVNRuntimeClaimRenewalResponseLossConvergence ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-repeated-db-failover:
	KIM_RUN_DOCKER_POSTGRES_REPEATED_FAILOVER=1 $(GO) test -v -count=1 -timeout 420s -run TestOVNRuntimeWorkerRepeatedPostgreSQLFailoverDuringRenewal ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-latency-saturation:
	KIM_RUN_DOCKER_POSTGRES_OVN_LATENCY_SATURATION=1 $(GO) test -v -count=1 -timeout 180s -run TestOVNRuntimeSustainedLatencyPoolSaturation ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-drain:
	KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_DRAIN=1 $(GO) test -race -v -count=1 -timeout 120s -run TestOVNRuntimeWorkerScaleUpDrainDown ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-hard-drain:
	KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_HARD_DRAIN=1 $(GO) test -race -v -count=1 -timeout 180s -run TestOVNRuntimeWorkerProcessDrainBoundaries ./internal/qualification/p1c03ovnwork

test-p1d03-ovn-worker-rolling-upgrade:
	KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_ROLLING_UPGRADE=1 $(GO) test -race -v -count=1 -timeout 180s -run TestOVNRuntimeWorkerExplicitNMinusOneRollingUpgrade ./internal/qualification/p1c03ovnwork

test-p1d03-ovn-worker-upgrade-failover:
	KIM_RUN_DOCKER_POSTGRES_OVN_UPGRADE_FAILOVER=1 $(GO) test -race -v -count=1 -timeout 240s -run TestOVNRuntimeRollingUpgradeHardDrainPostgreSQLFailover ./internal/qualification/p1c03ovnwork

test-p1d03-upgrade-campaign:
	KIM_RUN_DOCKER_POSTGRES_UPGRADE_CAMPAIGN=1 $(GO) test -race -v -count=1 -timeout 180s -run TestProductUpgradeCampaignCoordinatorRecoveryAndCanaryDecision ./internal/qualification/p1c03ovnwork

test-p1d03-upgrade-coordinator-failover:
	KIM_RUN_DOCKER_POSTGRES_UPGRADE_COORDINATOR_FAILOVER=1 $(GO) test -race -v -count=1 -timeout 240s -run TestUpgradeCoordinatorProcessKillPostgreSQLFailover ./internal/qualification/p1c03ovnwork

test-p1d03-upgrade-target-executor:
	KIM_RUN_DOCKER_POSTGRES_UPGRADE_TARGET_EXECUTOR=1 $(GO) test -race -v -count=1 -timeout 240s -run TestUpgradeTargetExecutorProcessKillMultipleUnknownReadBackRecovery ./internal/qualification/p1c03ovnwork

test-p1d03-systemd-package-upgrade:
	KIM_RUN_REMOTE_SYSTEMD_PACKAGE_UPGRADE=1 $(GO) test -race -v -count=1 -timeout 360s -run TestUpgradeTargetSystemdDebianPackageKillReadBack ./internal/qualification/p1c03ovnwork

test-p1c03-ovn-worker-soak:
	KIM_RUN_DOCKER_POSTGRES_OVN_SOAK=1 $(GO) test -v -count=1 -timeout 240s -run TestOVNRuntimeBacklogRetryStormMultiWorkerSoak ./internal/qualification/p1c03ovnwork

vet:
	$(GO) vet ./...

generate-agent-protocol:
	mkdir -p bin/tools
	GOBIN="$(CURDIR)/bin/tools" $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOBIN="$(CURDIR)/bin/tools" $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	PATH="$(CURDIR)/bin/tools:$$PATH" protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/agentprotocol/v1/transport.proto
