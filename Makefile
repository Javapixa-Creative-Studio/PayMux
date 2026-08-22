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

.PHONY: up
up: ## Start the full stack with docker compose
	docker compose up -d --build

.PHONY: down
down: ## Stop the stack
	docker compose down

.PHONY: logs
logs: ## Follow the stack's logs
	docker compose logs -f

.PHONY: db
db: ## Start only PostgreSQL, for running the backend locally
	docker compose up -d postgres

.PHONY: key
key: ## Generate an encryption key for PAYMUX_ENCRYPTION_KEY
	@openssl rand -hex 32
