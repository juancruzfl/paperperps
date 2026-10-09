# Loads .env (if present) into every recipe's environment.
-include .env
export

.PHONY: help setup db-up db-down db-reset server web test test-server test-web lint build docker

help: ## List targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

setup: ## First-time setup: .env, npm install, start Postgres
	@test -f .env || cp .env.example .env
	cd web && npm install
	$(MAKE) db-up

db-up: ## Start local Postgres (port 5433)
	docker compose up -d --wait postgres

db-down: ## Stop local Postgres
	docker compose down

db-reset: ## Delete local Postgres data and start fresh
	docker compose down -v
	$(MAKE) db-up

server: ## Run the Go server on :8080
	cd server && go run ./cmd/paperperps

web: ## Run the Vite dev server on :5173 (proxies /api to :8080)
	cd web && npm run dev

test: test-server test-web ## Run all tests

test-server:
	cd server && go vet ./... && go test ./...

test-web:
	cd web && npm run lint && npm run build

build: ## Build the frontend and server binary
	cd web && npm run build
	cd server && go build -o ../bin/paperperps ./cmd/paperperps

docker: ## Build the production image
	docker build -t paperperps .
