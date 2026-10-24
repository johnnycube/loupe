# Loupe — dev & test targets.
#
# The contract targets (tools, fmt, lint, test, cover, audit, build, check,
# check-fast) are the interface the git hooks and CI rely on; keep their names.
# `make check` is the definition of green. See CONTRIBUTING.md.
SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

# JSON snapshot imported by `make import` (override: make import JSON=path).
JSON ?= data/store.json

# Coverage floor for `make cover` (statements, generated sqlc code excluded).
# A ratchet: raise it as tests are added, never lower it.
COVER_MIN ?= 30

# Pinned tool versions, installed by `make tools`. Renovate bumps them through
# the `# renovate:` annotations (customManagers:makefileVersions).
# renovate: datasource=go depName=github.com/golangci/golangci-lint/v2
GOLANGCI_VERSION ?= v2.15.0
# renovate: datasource=go depName=golang.org/x/vuln
GOVULNCHECK_VERSION ?= v1.9.0
# renovate: datasource=go depName=github.com/zricethezav/gitleaks/v8
GITLEAKS_VERSION ?= v8.30.1
# renovate: datasource=go depName=github.com/fleetlint/fleetlint
FLEETLINT_VERSION ?= v0.8.1
# renovate: datasource=go depName=github.com/sqlc-dev/sqlc
SQLC_VERSION ?= v1.31.1

# load-env runs a recipe with .env sourced if it exists (dev LOUPE_DB_DRIVER/DSN).
load-env = if [ -f .env ]; then set -a; . ./.env; set +a; fi;

.PHONY: help tools fmt lint test test-race vet cover audit check-fast check \
        run dev db-up db-down import sqlc build build-frontend clean clean-data

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---- contract -----------------------------------------------------------------

tools: ## Install the pinned dev tools (Go tools into GOBIN, frontend deps from the lockfile)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)
	go install github.com/fleetlint/fleetlint/cmd/fleetlint@$(FLEETLINT_VERSION)
	cd frontend && npm ci

fmt: ## Format in place (gofumpt + goimports, prettier)
	golangci-lint fmt
	cd frontend && npm run format

lint: ## Format check + linters + svelte-check, warnings are errors
	golangci-lint run
	cd frontend && npm run lint
	cd frontend && npm run check

test: ## Run the Go test suite with the race detector (no network, no gallery-dl needed)
	go test -race -shuffle=on ./...

test-race: test ## Alias of `test` (kept for muscle memory)

vet: ## go vet
	go vet ./...

cover: ## Go coverage; fails if the total (generated sqlc code excluded) is below COVER_MIN
	go test -count=1 -coverprofile=coverage.out ./...
	@grep -v '/internal/db/' coverage.out > coverage.filtered.out
	@total=$$(go tool cover -func=coverage.filtered.out | awk '/^total:/ { sub("%", "", $$3); print int($$3) }'); \
	  echo "coverage: $${total}% (COVER_MIN=$(COVER_MIN)%)"; \
	  if [ "$$total" -lt "$(COVER_MIN)" ]; then echo "coverage is below COVER_MIN"; exit 1; fi

audit: ## Vulnerabilities (Go + npm), tidy module graph, leaked secrets
	govulncheck ./...
	go mod tidy -diff
	cd frontend && npm audit --audit-level=high
	gitleaks git --no-banner --redact .

check-fast: ## Before every commit: lint + the Go suite without the race detector
	$(MAKE) lint
	go test ./...

check: lint test cover audit build ## Definition of green: everything CI runs
	fleetlint check --fail-on error

# ---- dev ----------------------------------------------------------------------

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

sqlc: ## Regenerate the sqlc query code
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

# ---- build --------------------------------------------------------------------

build-frontend: ## Rebuild the embedded SvelteKit UI (installs from the lockfile)
	cd frontend && npm ci && npm run build

# Build metadata stamped into the binary (surfaced on the About page). The tag is
# only set when HEAD is exactly a tag; --exact-match prints nothing otherwise.
LDFLAGS = -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
          -X main.gitCommit=$(shell git rev-parse --short HEAD 2>/dev/null) \
          -X main.gitTag=$(shell git describe --tags --exact-match 2>/dev/null)

build: build-frontend ## Build the single production binary
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o loupe .

clean: ## Remove the built binary and coverage profiles
	rm -f loupe coverage.out coverage.filtered.out

clean-data: ## Wipe local SQLite state (data/loupe.db*) to start fresh
	rm -f data/loupe.db data/loupe.db-wal data/loupe.db-shm
