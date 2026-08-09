GO ?= go

.PHONY: all build check docs-check fmt fmt-check test test-postgres-integration vet

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

test-postgres-integration:
	test -n "$(KIM_POSTGRES_TEST_URL)"
	KIM_POSTGRES_TEST_URL="$(KIM_POSTGRES_TEST_URL)" $(GO) test -count=1 -run TestMigratePostgreSQLIntegration ./internal/persistence/postgres

vet:
	$(GO) vet ./...
