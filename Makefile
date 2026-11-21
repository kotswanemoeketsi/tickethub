# GotATicket developer commands. Run `make help` for the list.

BACKEND  := backend
FRONTEND := frontend
COMPOSE  := docker compose

.DEFAULT_GOAL := help
.PHONY: help setup db api worker seed web dev test test-backend test-frontend lint build up demo down reset logs

help: ## Show this help
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

setup: ## Install dependencies and create local config files
	@test -f $(BACKEND)/.env || cp $(BACKEND)/.env.example $(BACKEND)/.env
	cd $(BACKEND) && go mod download
	cd $(FRONTEND) && npm install

db: ## Create the local MySQL database and user (asks for the MySQL root password)
	mysql -u root -p < scripts/create-database.sql

api: ## Run the API (with the embedded worker) on :8080
	cd $(BACKEND) && go run ./cmd/gotaticket serve

worker: ## Run the background worker on its own
	cd $(BACKEND) && go run ./cmd/gotaticket worker

seed: ## Load demo events, orders and accounts (password: Password123)
	cd $(BACKEND) && go run ./cmd/gotaticket seed

web: ## Run the React app on :5173
	cd $(FRONTEND) && npm run dev

test: test-backend test-frontend ## Run all tests

test-backend: ## Run Go tests (integration tests need MySQL; see README)
	cd $(BACKEND) && go test -race ./...

test-frontend: ## Type-check and run frontend tests
	cd $(FRONTEND) && npm run typecheck && npm test

lint: ## Check formatting and vet the Go code
	cd $(BACKEND) && test -z "$$(gofmt -l .)" && go vet ./...

build: ## Build the API binary and the web bundle
	cd $(BACKEND) && CGO_ENABLED=0 go build -trimpath -o bin/gotaticket ./cmd/gotaticket
	cd $(FRONTEND) && npm run build

up: ## Docker: build and start the platform on http://localhost:8000
	$(COMPOSE) up -d --build

demo: ## Docker: the same, with demo events, orders and accounts
	$(COMPOSE) --profile demo up -d --build

down: ## Docker: stop everything (data is kept)
	$(COMPOSE) --profile demo down

reset: ## Docker: stop everything and delete the database and uploads
	$(COMPOSE) --profile demo down -v

logs: ## Docker: follow the app and worker logs
	$(COMPOSE) logs -f app worker
