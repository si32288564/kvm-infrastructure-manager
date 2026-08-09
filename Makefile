GO ?= go

.PHONY: all bench-agent-transport build check docs-check fmt fmt-check generate-agent-protocol scale-agent-transport test test-agent-transport test-postgres-integration vet

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

bench-agent-transport:
	$(GO) test -run '^$$' -bench 'Benchmark(GRPC|HTTP2)RoundTrip' -benchmem -benchtime=500ms -count=3 ./internal/agent/transport/grpcstream ./internal/agent/transport/http2stream

scale-agent-transport:
	$(GO) run ./cmd/kim-agent-transport-scale -candidate grpc -sessions 1000
	$(GO) run ./cmd/kim-agent-transport-scale -candidate http2 -sessions 1000

test-postgres-integration:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	KIM_POSTGRES_TEST_URL="$(KIM_POSTGRES_TEST_URL)" $(GO) test -count=1 -run TestMigratePostgreSQLIntegration ./internal/persistence/postgres

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
