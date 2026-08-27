# PayMux development tasks.
#
# node_modules is filtered out of Go package lists: the dashboard's
# dependencies contain a stray Go file that would otherwise be walked.
GO_PACKAGES := $(shell go list ./... | grep -v node_modules)
DASHBOARD   := apps/dashboard

.PHONY: help
help: ## Show the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Backend
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build the API and worker binaries
	go build -trimpath -o bin/paymux-api ./apps/api
	go build -trimpath -o bin/paymux-worker ./apps/worker

.PHONY: test
test: ## Run the Go unit tests
	go test $(GO_PACKAGES)

.PHONY: test-race
test-race: ## Run the Go tests with the race detector
	go test -race $(GO_PACKAGES)

.PHONY: test-integration
test-integration: ## Run the integration tests (needs PAYMUX_TEST_DATABASE_URL)
	go test -count=1 ./internal/integration/...

.PHONY: cover
cover: ## Run tests and report coverage
	go test -coverprofile=coverage.out $(GO_PACKAGES)
	go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format the Go sources
	gofmt -w apps examples internal migrations

.PHONY: lint
lint: ## Vet and format-check the Go sources
	go vet $(GO_PACKAGES)
	@test -z "$$(gofmt -l apps examples internal migrations)" || \
		(echo "these files need gofmt:"; gofmt -l apps examples internal migrations; exit 1)

.PHONY: lint-full
lint-full: ## Run golangci-lint, as CI does
	golangci-lint run ./...

.PHONY: vuln
vuln: ## Check dependencies and the standard library for known CVEs
	govulncheck ./...

# ---------------------------------------------------------------------------
# Dashboard
# ---------------------------------------------------------------------------

.PHONY: dashboard-install
dashboard-install: ## Install the dashboard's dependencies
	cd $(DASHBOARD) && npm install

.PHONY: dashboard-dev
dashboard-dev: ## Run the dashboard in development mode
	cd $(DASHBOARD) && npm run dev

.PHONY: dashboard-build
dashboard-build: ## Build the dashboard for production
	cd $(DASHBOARD) && npm run build

.PHONY: dashboard-lint
dashboard-lint: ## Lint and typecheck the dashboard
	cd $(DASHBOARD) && npm run lint && npm run typecheck

.PHONY: dashboard-test
dashboard-test: ## Run the dashboard tests
	cd $(DASHBOARD) && npm test

# ---------------------------------------------------------------------------
# Local environment
# ---------------------------------------------------------------------------

# Stamped into the api and worker binaries via -ldflags, so a deployed image
# reports something better than "dev" in its startup log. Exported because
# docker-compose.yml interpolates it into the build args.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
export VERSION

COMPOSE_PG := -f docker-compose.yml -f docker-compose.postgres.yml

.PHONY: up
up: ## Start api, worker and dashboard against your own PostgreSQL
	docker compose up -d --build
	@$(MAKE) --no-print-directory ports

.PHONY: up-postgres
up-postgres: ## Start the stack and a PostgreSQL container with it
	docker compose $(COMPOSE_PG) up -d --build
	@$(MAKE) --no-print-directory ports

.PHONY: up-proxied
up-proxied: ## Start without publishing host ports, for a platform that proxies
	docker compose -f docker-compose.yml -f docker-compose.proxied.yml up -d --build
	@$(MAKE) --no-print-directory ports

.PHONY: landing
landing: ## Build and serve the landing page on its own
	docker compose --profile landing up -d --build landing
	@echo "  landing  http://localhost:$${PAYMUX_LANDING_PORT:-7881}  (container port 80)"

# ---------------------------------------------------------------------------
# One service per file
#
# For a platform that deploys one app per compose file rather than a stack.
# --project-directory is not optional: compose reads .env from the project
# directory, which would otherwise be deployments/ and would miss the .env at
# the repository root.
# ---------------------------------------------------------------------------

SOLO := docker compose --project-directory . -f deployments/compose

.PHONY: deploy-api
deploy-api: ## Start only the API, from its own compose file
	$(SOLO).api.yml up -d --build

.PHONY: deploy-worker
deploy-worker: ## Start only the worker, from its own compose file
	$(SOLO).worker.yml up -d --build

.PHONY: deploy-dashboard
deploy-dashboard: ## Start only the dashboard, from its own compose file
	$(SOLO).dashboard.yml up -d --build

.PHONY: deploy-landing
deploy-landing: ## Start only the landing page, from its own compose file
	$(SOLO).landing.yml up -d --build

.PHONY: deploy-config
deploy-config: ## Validate every per-service compose file
	@for s in api worker dashboard landing; do printf "  %-10s " "$$s"; $(SOLO).$$s.yml config -q && echo "ok"; done

.PHONY: down
down: ## Stop the stack, including a bundled PostgreSQL if one was started
	docker compose $(COMPOSE_PG) down

.PHONY: logs
logs: ## Follow the stack's logs
	docker compose logs -f

.PHONY: db
db: ## Start only PostgreSQL, for running the backend locally
	docker compose $(COMPOSE_PG) up -d postgres

.PHONY: ports
ports: ## Show where each service listens, for binding domains
	@echo ""
	@echo "  Bind a domain to the container port, not the published one. A host"
	@echo "  like Easypanel or Coolify talks to the container directly."
	@echo ""
	@printf "  %-11s %-15s %s
" "SERVICE" "CONTAINER PORT" "WHAT IT SERVES"
	@printf "  %-11s %-15s %s
" "----------" "--------------" "-------------------------------------"
	@printf "  %-11s %-15s %s
" "api" "8080" "REST API and /webhooks/midtrans. Public."
	@printf "  %-11s %-15s %s
" "dashboard" "80" "Operations UI. Public, on its own domain."
	@printf "  %-11s %-15s %s
" "worker" "9090" "Prometheus metrics. Keep this internal."
	@echo ""
	@echo "  The worker has no API. It is unauthenticated on 9090, so give it no"
	@echo "  domain and no published port unless Prometheus needs to reach it."
	@echo ""
	@echo "  Published on this host right now:"
	@docker compose $(COMPOSE_PG) ps --format "    {{.Service}}  {{.Ports}}" 2>/dev/null || 		echo "    (nothing running)"
	@echo ""

.PHONY: key
key: ## Generate an encryption key for PAYMUX_ENCRYPTION_KEY
	@openssl rand -hex 32
