# Loads .env (if present) into every recipe's environment.
-include .env
export

.PHONY: help setup db-up db-down db-reset server web test test-server test-web lint build docker loadtest drill backup e2e

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
	cd web && npm run lint && npm test && npm run build

build: ## Build the frontend and server binary
	cd web && npm run build
	cd server && go build -o ../bin/paperperps ./cmd/paperperps

docker: ## Build the production image
	docker build -t paperperps .

loadtest: ## Load test an isolated server on :8099 (LOADTEST_FLAGS="-accounts 50" to tweak)
	@psql "postgres://paperperps:paperperps@localhost:5433/paperperps" -qtAc "DROP DATABASE IF EXISTS paperperps_load WITH (FORCE)" -c "CREATE DATABASE paperperps_load"
	cd server && go build -o ../bin/paperperps-load ./cmd/paperperps
	DATABASE_URL="postgres://paperperps:paperperps@localhost:5433/paperperps_load?sslmode=disable" PORT=8099 WEB_DIR= ./bin/paperperps-load > bin/loadtest-server.log 2>&1 & \
		pid=$$!; sleep 5; (cd server && go run ./cmd/loadtest -url http://localhost:8099 $(LOADTEST_FLAGS)); status=$$?; kill $$pid; exit $$status

e2e: ## Browser smoke tests against a full stack with a fake Hyperliquid
	scripts/e2e.sh

drill: ## Redeploy and crash drill against a throwaway database
	scripts/drill-redeploy.sh

backup: ## Dump DATABASE_URL to backups/
	scripts/backup.sh
