# SentinelACS — Makefile
# Convenção: comandos curtos, idempotentes, executáveis no servidor da empresa.
# Em modo dev local (rede sem acesso à infra interna), use `make dev` com .env apontando para stack local.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# ──────────────── Variáveis ────────────────
COMPOSE      := docker compose -f deploy/docker-compose.yml --env-file .env
COMPOSE_PROD := docker compose -f deploy/docker-compose.prod.yml --env-file .env
GO           := go
TEMPL        := go run github.com/a-h/templ/cmd/templ@latest
GOOSE        := go run github.com/pressly/goose/v3/cmd/goose@latest
SQLC         := go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest
TAILWIND     := tailwindcss

DB_URL ?= $(DATABASE_URL)

# ──────────────── Help ────────────────
.PHONY: help
help: ## Lista os alvos disponíveis
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ──────────────── Setup ────────────────
.PHONY: setup
setup: ## Instala ferramentas de dev (templ, goose, sqlc, air, gosec, govulncheck)
	$(GO) install github.com/a-h/templ/cmd/templ@latest
	$(GO) install github.com/pressly/goose/v3/cmd/goose@latest
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	$(GO) install github.com/air-verse/air@latest
	$(GO) install github.com/securego/gosec/v2/cmd/gosec@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

# ──────────────── Dev workflow ────────────────
.PHONY: up
up: ## Sobe stack de dependências (Postgres, Redis, GenieACS, Mongo)
	$(COMPOSE) up -d postgres redis mongo genieacs-cwmp genieacs-nbi genieacs-fs

.PHONY: down
down: ## Derruba a stack
	$(COMPOSE) down

.PHONY: logs
logs: ## Tail de logs da stack
	$(COMPOSE) logs -f --tail=100

.PHONY: dev
dev: up generate tailwind ## Sobe deps + roda servidor com hot reload (requer air)
	@echo "→ rode 'make tailwind-watch' em outro terminal se quiser CSS live"
	air

.PHONY: generate
generate: ## Gera código (templ + sqlc)
	$(TEMPL) generate
	$(SQLC) generate -f internal/infrastructure/postgres/sqlc.yaml || true

.PHONY: tailwind
tailwind: ## Build CSS uma vez (production)
	cd web && $(TAILWIND) -i static/css/input.css -o static/css/app.css --minify

.PHONY: tailwind-watch
tailwind-watch: ## Build CSS em modo watch (dev paralelo)
	cd web && $(TAILWIND) -i static/css/input.css -o static/css/app.css --watch

# ──────────────── Migrations ────────────────
.PHONY: migrate-up
migrate-up: ## Aplica migrations
	$(GOOSE) -dir migrations postgres "$(DB_URL)" up

.PHONY: migrate-down
migrate-down: ## Reverte última migration
	$(GOOSE) -dir migrations postgres "$(DB_URL)" down

.PHONY: migrate-status
migrate-status: ## Status das migrations
	$(GOOSE) -dir migrations postgres "$(DB_URL)" status

.PHONY: migrate-create
migrate-create: ## Cria nova migration: make migrate-create NAME=add_users
	$(GOOSE) -dir migrations create $(NAME) sql

# ──────────────── Build & Test ────────────────
.PHONY: build
build: generate tailwind ## Compila os 3 binários (server, worker, migrate) com strip + trimpath
	@VERSION=$$(git rev-parse --short HEAD 2>/dev/null || echo dev); \
	echo "→ build version=$$VERSION"; \
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$$VERSION -buildid=" -o bin/server  ./cmd/server; \
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$$VERSION -buildid=" -o bin/worker  ./cmd/worker; \
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -buildid="                          -o bin/migrate ./cmd/migrate

.PHONY: test
test: ## Roda testes com race detector
	$(GO) test -race -coverprofile=cover.out ./...

.PHONY: lint
lint: ## golangci-lint
	golangci-lint run ./...

.PHONY: sec
sec: ## gosec + govulncheck
	gosec -quiet ./...
	govulncheck ./...

# ──────────────── Docker ────────────────
.PHONY: docker-build
docker-build: ## Build da imagem
	docker build -f deploy/Dockerfile -t sentinel-acs:local .

.PHONY: deploy-prod
deploy-prod: ## Sobe stack de produção (no servidor da empresa)
	$(COMPOSE_PROD) pull
	$(COMPOSE_PROD) up -d

# ──────────────── Limpeza ────────────────
.PHONY: clean
clean: ## Remove artefatos de build
	rm -rf bin/ tmp/ cover.out build-errors.log
	find . -name '*_templ.go' -delete
