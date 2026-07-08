# Loupe — dev & test targets.
.DEFAULT_GOAL := help

# JSON snapshot imported by `make import` (override: make import JSON=path).
JSON ?= data/store.json

# load-env runs a recipe with .env sourced if it exists (dev LOUPE_DB_DRIVER/DSN).
load-env = if [ -f .env ]; then set -a; . ./.env; set +a; fi;

.PHONY: help test test-race vet run dev db-up db-down import sqlc build build-frontend clean clean-data

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

test: ## Run the Go test suite (no network, no gallery-dl needed)
	go test ./...

test-race: ## Run tests with the race detector
	go test -race ./...

vet: ## go vet
	go vet ./...

run: ## Run the app on :8787 with the embedded SQLite default
	go run -ldflags "$(LDFLAGS)" .

dev: ## Run the app against the docker-compose Postgres (sources .env)
	@$(load-env) go run -ldflags "$(LDFLAGS)" .

db-up: ## Start the Postgres 18 dev database
	docker compose -f docker-compose.dev.yml up -d

db-down: ## Stop the dev database (keeps the data volume)
	docker compose -f docker-compose.dev.yml down

import: ## Import a legacy store.json into the configured DB (sources .env)
	@$(load-env) go run . import $(JSON)

sqlc: ## Regenerate the sqlc query code (pins v1.31.1)
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

build-frontend: ## Rebuild the embedded SvelteKit UI
	cd frontend && npm install && npm run build

# Build metadata stamped into the binary (surfaced on the About page). The tag is
# only set when HEAD is exactly a tag; --exact-match prints nothing otherwise.
LDFLAGS = -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
          -X main.gitCommit=$(shell git rev-parse --short HEAD 2>/dev/null) \
          -X main.gitTag=$(shell git describe --tags --exact-match 2>/dev/null)

build: build-frontend ## Build the single production binary
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o loupe .

clean: ## Remove the built binary
	rm -f loupe

clean-data: ## Wipe local SQLite state (data/loupe.db*) to start fresh
	rm -f data/loupe.db data/loupe.db-wal data/loupe.db-shm
