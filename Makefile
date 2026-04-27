.PHONY: help run build test test-integration lint compose-up compose-down db-shell clean

BIN_DIR := bin
BINARY  := $(BIN_DIR)/server
PKG     := ./cmd/server

GO_LDFLAGS     := -s -w
GO_BUILD_FLAGS := -trimpath -ldflags='$(GO_LDFLAGS)'

COMPOSE_FILE := deploy/docker-compose.yml

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

run: ## Run the server locally
	go run $(PKG)

build: ## Build static binary into bin/server (CGO disabled, stripped)
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(BINARY) $(PKG)

test: ## Run unit tests with race detector (integration tests excluded)
	go test -race ./...

test-integration: ## Run integration tests against Postgres in testcontainers
	go test -race -tags=integration ./tests/integration/...

lint: ## Run golangci-lint
	golangci-lint run ./...

compose-up: ## Start the full stack (foreground, with rebuild)
	docker compose -f $(COMPOSE_FILE) up --build

compose-down: ## Stop the stack (volumes preserved)
	docker compose -f $(COMPOSE_FILE) down

db-shell: ## Open psql shell on the running compose Postgres
	docker compose -f $(COMPOSE_FILE) exec postgres psql -U stocksim

clean: ## Remove built artifacts
	rm -rf $(BIN_DIR)
