DATABASE_URL ?= postgres://catalog:catalog@localhost:5432/catalog?sslmode=disable
export DATABASE_URL

.PHONY: db-up db-down run supplier test vet build

db-up: ## Start PostgreSQL in Docker
	docker compose up -d --wait postgres

db-down: ## Stop PostgreSQL
	docker compose down

run: ## Run the staff application
	go run ./cmd/catalog

supplier: ## Run the fictional supplier
	go run ./cmd/fakesupplier

test: ## Run all tests
	go test ./...

vet: ## Static checks
	go vet ./...

build: ## Build both programs into ./bin
	go build -o bin/ ./cmd/...
