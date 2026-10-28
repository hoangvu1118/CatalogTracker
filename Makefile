DATABASE_URL ?= postgres://catalog:catalog@localhost:5432/catalog?sslmode=disable
export DATABASE_URL

.PHONY: db-up db-down run supplier supplier-big test test-unit vet build

db-up: ## Start PostgreSQL in Docker
	docker compose up -d --wait postgres

db-down: ## Stop PostgreSQL
	docker compose down

run: ## Run the staff application
	go run ./cmd/catalog

supplier: ## Run the fictional supplier with the demo story feed (slowed down to watch)
	go run ./cmd/fakesupplier -delay 700ms

supplier-big: ## Run the fictional supplier with a large generated, messy feed
	go run ./cmd/fakesupplier -generate -seed 42 -delay 150ms

test: ## Run all tests (needs PostgreSQL: make db-up)
	go test -race -count=1 ./...

test-unit: ## Run tests that need no database
	DATABASE_URL= go test -race -count=1 ./...

vet: ## Static checks
	go vet ./...

build: ## Build both programs into ./bin
	go build -o bin/ ./cmd/...
