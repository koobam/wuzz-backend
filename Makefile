SERVICES := gateway user catalog transaction
GOBIN    := $(shell go env GOPATH)/bin
VERSION  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

export PATH := $(GOBIN):$(PATH)

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install buf, protoc plugins, goose and swag
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/pressly/goose/v3/cmd/goose@latest

.PHONY: proto
proto: ## Lint and regenerate gRPC code from proto/
	$(GOBIN)/buf lint
	$(GOBIN)/buf generate

.PHONY: build
build: ## Build every service into bin/
	@for service in $(SERVICES) migrate; do \
		echo "building $$service"; \
		go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/$$service ./cmd/$$service || exit 1; \
	done

.PHONY: test
test: ## Run the unit tests
	go test -race -count=1 ./...

.PHONY: lint
lint: ## Vet and format-check
	go vet ./...
	@unformatted=$$(gofmt -l . | grep -v '^gen/' || true); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

.PHONY: up
up: ## Start the whole stack locally
	docker compose up --build -d

.PHONY: dev
dev: ## Start local dependencies (postgres, redis) and run migrations
	docker compose up -d postgres redis kafka mongo
	docker compose up migrate-user

.PHONY: run
run: ## Run one service on the host: make run SERVICE=gateway
	@test -n "$(SERVICE)" || (echo "usage: make run SERVICE=<$(SERVICES)>"; exit 1)
	go run ./cmd/$(SERVICE)

.PHONY: migrate
migrate: ## Apply migrations for one service: make migrate SERVICE=user
	@test -n "$(SERVICE)" || (echo "usage: make migrate SERVICE=<name>"; exit 1)
	MIGRATE_SERVICE=$(SERVICE) go run ./cmd/migrate

.PHONY: down
down: ## Stop the stack (keeps the database volume)
	docker compose down

.PHONY: reset
reset: ## Stop the stack and delete its data
	docker compose down -v

.PHONY: logs
logs: ## Tail the application services
	docker compose logs -f gateway user catalog transaction

.PHONY: images
images: ## Build an image per service. SERVICES=... to narrow it down.
	@for service in $(SERVICES); do \
		docker build --build-arg SERVICE=$$service --build-arg VERSION=$(VERSION) \
			-t wuzz-$$service:$(VERSION) . || exit 1; \
	done

.PHONY: staff
staff: ## Create/update a staff account: make staff PHONE=0812... ROLES=admin NAME="Ani"
	@test -n "$(PHONE)" || (echo 'usage: make staff PHONE=0812... ROLES=admin|outlet:1 [NAME=...]'; exit 1)
	go run ./cmd/staff -phone "$(PHONE)" -roles "$(or $(ROLES),admin)" -name "$(NAME)"

.PHONY: local local-down local-status
local: ## Run the whole stack on this machine (infra, services, support, frontends)
	./scripts/local.sh up

local-down: ## Stop what `make local` started
	./scripts/local.sh down

local-status: ## What is listening where
	./scripts/local.sh status
