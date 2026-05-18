.PHONY: run build test test-integration lint vet sqlc migrate-up migrate-down migrate-force tidy

# --- Toolchain ---
GO            ?= go
GOLANGCI_LINT ?= golangci-lint
SQLC          ?= sqlc

# --- App vars ---
APP_NAME      := api
BIN_DIR       := bin
PKG           := ./...

# --- Migration vars (override via env) ---
DATABASE_URL  ?= postgres://postgres:postgres@localhost:5432/agent_example?sslmode=disable
MIGRATIONS    := ./migrations
VERSION       ?=

## Run the API service locally
run:
	$(GO) run ./cmd/api

## Build the API binary
build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/$(APP_NAME) ./cmd/api

## Run unit tests (excludes //go:build integration)
test:
	$(GO) test -race -count=1 $(PKG)

## Run integration tests (requires Docker / testcontainers)
test-integration:
	$(GO) test -race -count=1 -tags=integration $(PKG)

## Lint via golangci-lint (config: .golangci.yml)
lint:
	$(GOLANGCI_LINT) run

## go vet
vet:
	$(GO) vet $(PKG)

## Regenerate sqlc code (config: sqlc.yaml)
sqlc:
	$(SQLC) generate

## Apply all up migrations
migrate-up:
	$(GO) run ./cmd/api migrate up

## Roll one migration down
migrate-down:
	$(GO) run ./cmd/api migrate down

## Force migration version (recover from dirty state). Usage: make migrate-force VERSION=1
migrate-force:
	$(GO) run ./cmd/api migrate force $(VERSION)

## go mod tidy
tidy:
	$(GO) mod tidy
