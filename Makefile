.PHONY: ui-build build test test-ui test-integration up down fmt vet check help

ui-build: ## Build the embedded frontend assets (frontend/dist)
	npm --prefix frontend run build

build: ui-build ## Build the Go binary with the embedded UI
	go build ./...

test: ui-build ## Run the Go test suite (UI built first for embed)
	go test ./...

test-ui: ## Run the frontend unit tests
	npm --prefix frontend test

test-integration: ui-build ## Run integration tests against a real MongoDB (Docker, or $$MSTASH_TEST_MONGO_URI)
	./scripts/test-integration.sh

up: ## Start the full stack (Mongo replica set + API) via Compose
	docker compose up --build

down: ## Stop the Compose stack
	docker compose down

fmt: ## Format Go sources
	gofmt -w .

vet: ui-build ## Run go vet (UI built first for embed)
	go vet ./...

check: fmt vet test test-ui ## Pre-commit gate: format, vet, and test

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'