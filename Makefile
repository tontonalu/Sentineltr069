# SentinelACS — Makefile
# Convenção: comandos curtos, idempotentes, executáveis no servidor da empresa.
# Em modo dev local (rede sem acesso à infra interna), use `make dev` com .env apontando para stack local.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# ──────────────── Variáveis ────────────────
COMPOSE      := docker compose -f deploy/docker-compose.yml --env-file .env
COMPOSE_PROD := docker compose -f deploy/docker-compose.prod.yml --env-file .env
GO           := go
# `@latest` drifta do runtime pinado em go.mod e quebra a compilação quando o
# gerador emite símbolos ainda não presentes na versão importada. Resolvemos
# a versão exata a partir de go.mod (mesma estratégia do CI/Dockerfile).
TEMPL_VERSION := $(shell go list -m -f '{{.Version}}' github.com/a-h/templ)
TEMPL        := go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)
GOOSE        := go run github.com/pressly/goose/v3/cmd/goose@latest
SQLC         := go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# Tailwind standalone — versão pinada (mesma do Dockerfile). Cacheado em
# bin/.tailwindcss para evitar baixar a cada build. Detecta arch automaticamente.
TAILWIND_VERSION := v3.4.17
# Releases tailwindlabs usam "macos" (não "darwin") e "x64"/"arm64".
TAILWIND_OS   := $(shell uname -s | sed -e 's/Darwin/macos/' -e 's/Linux/linux/')
TAILWIND_ARCH := $(shell uname -m | sed -e 's/x86_64/x64/' -e 's/aarch64/arm64/')
TAILWIND_BIN := bin/.tailwindcss
TAILWIND := $(TAILWIND_BIN)

DB_URL ?= $(DATABASE_URL)

# ──────────────── Help ────────────────
.PHONY: help
help: ## Lista os alvos disponíveis
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ──────────────── Setup ────────────────
.PHONY: setup
setup: ## Instala ferramentas de dev (templ, goose, sqlc, air, gosec, govulncheck)
	$(GO) install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)
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

.PHONY: assets-vendor
assets-vendor: ## Baixa htmx.min.js + alpine.min.js para web/static/js/ (versões pinadas)
	@mkdir -p web/static/js
	@if [ ! -f web/static/js/htmx.min.js ]; then \
		echo "→ baixando htmx.min.js"; \
		curl -fsSL -o web/static/js/htmx.min.js "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js"; \
	fi
	@if [ ! -f web/static/js/alpine.min.js ]; then \
		echo "→ baixando alpine.min.js"; \
		curl -fsSL -o web/static/js/alpine.min.js "https://unpkg.com/alpinejs@3.14.8/dist/cdn.min.js"; \
	fi

# Baixa o tailwindcss standalone (mesma versão do Dockerfile) só na primeira vez.
# Sem isso, devs precisariam instalar Node + npm pra rodar `make tailwind`.
$(TAILWIND_BIN):
	@mkdir -p bin
	@echo "→ baixando tailwindcss $(TAILWIND_VERSION) ($(TAILWIND_OS)-$(TAILWIND_ARCH))"
	@curl -fsSL -o $@ "https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH)"
	@chmod +x $@

.PHONY: tailwind
tailwind: assets-vendor $(TAILWIND_BIN) ## Build CSS uma vez (production) + garante JS vendored
	cd web && ../$(TAILWIND) -i static/css/input.css -o static/css/app.css --minify

.PHONY: tailwind-watch
tailwind-watch: assets-vendor $(TAILWIND_BIN) ## Build CSS em modo watch (dev paralelo)
	cd web && ../$(TAILWIND) -i static/css/input.css -o static/css/app.css --watch

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
deploy-prod: ## Sobe stack de produção LOCALMENTE no servidor (use com cuidado)
	docker compose --env-file /opt/sentinelacs/config/.env \
		-f /opt/sentinelacs/docker-compose.yml \
		-f /opt/sentinelacs/docker-compose.prod.yml \
		pull
	docker compose --env-file /opt/sentinelacs/config/.env \
		-f /opt/sentinelacs/docker-compose.yml \
		-f /opt/sentinelacs/docker-compose.prod.yml \
		up -d

# ──────────────── Remoto (rodar do workstation) ────────────────
SSH_HOST ?= 177.72.177.102
SSH_USER ?= celinet

.PHONY: bootstrap-remote
bootstrap-remote: ## SCP+SSH bootstrap.sh no servidor (1ª vez). Requer SSH key já instalada.
	scp deploy/scripts/bootstrap.sh $(SSH_USER)@$(SSH_HOST):/tmp/
	ssh -t $(SSH_USER)@$(SSH_HOST) 'sudo bash /tmp/bootstrap.sh'

.PHONY: deploy-remote
deploy-remote: ## Trigger deploy via SSH no servidor (sem GitHub Actions). Usa imagem latest.
	rsync -az --delete deploy/docker-compose.yml deploy/docker-compose.prod.yml \
		$(SSH_USER)@$(SSH_HOST):/opt/sentinelacs/
	rsync -az --delete deploy/scripts/ \
		$(SSH_USER)@$(SSH_HOST):/opt/sentinelacs/scripts/
	ssh $(SSH_USER)@$(SSH_HOST) \
		'sudo chown -R sentinel:sentinel /opt/sentinelacs/scripts /opt/sentinelacs/*.yml && \
		 sudo chmod +x /opt/sentinelacs/scripts/*.sh && \
		 sudo -u sentinel /opt/sentinelacs/scripts/deploy.sh'

.PHONY: ssh
ssh: ## ssh interativo no servidor
	ssh $(SSH_USER)@$(SSH_HOST)

.PHONY: logs-remote
logs-remote: ## tail dos logs da app + worker no servidor
	ssh $(SSH_USER)@$(SSH_HOST) \
		'sudo docker compose --env-file /opt/sentinelacs/config/.env \
			-f /opt/sentinelacs/docker-compose.yml \
			-f /opt/sentinelacs/docker-compose.prod.yml \
			logs -f --tail=100 app worker'

# ──────────────── Limpeza ────────────────
.PHONY: clean
clean: ## Remove artefatos de build
	rm -rf bin/ tmp/ cover.out build-errors.log
	find . -name '*_templ.go' -delete
