# SentinelACS

Plataforma de gestão TR-069 sobre GenieACS.
Documentação canônica: [SentinelACS.md](SentinelACS.md) — plano de execução: [ROADMAP.md](ROADMAP.md).

## Status

Fase 0 — Fundação (em andamento).

## Onde isto roda

A aplicação **não roda nesta máquina**. Deploy é no servidor da empresa, dentro da rede interna onde já existem:

- Voalle (ERP) — base de clientes ativa
- FreeRADIUS + Huawei NE8000 — autenticação PPPoE
- (a subir) GenieACS — pré-requisito da Fase 0 (Pré-req-A)

Esta máquina é apenas para **edição de código**. CI compila a imagem, GHCR armazena, o servidor da empresa puxa e sobe.

## Estrutura

```
cmd/
  server/        # binário web + API
  worker/        # provisioning / telemetry / alerting
  migrate/       # CLI de migrations (placeholder)
internal/
  domain/        # camada pura (sem deps externas)
  application/   # casos de uso
  infrastructure/# adapters (PG, Redis, GenieACS, ERP, RADIUS, notifiers)
  transport/     # HTTP handlers + API REST
  views/         # templ (.templ files)
  platform/      # cross-cutting (config, logger, crypto, eventbus)
migrations/      # SQL puro (goose)
deploy/          # Dockerfile + docker-compose (base + prod)
docs/adr/        # Architecture Decision Records
web/static/      # assets (htmx, alpine, css build do tailwind)
```

## Setup no servidor da empresa

```bash
# 1. Clonar o repo
git clone <url> /opt/sentinel-acs
cd /opt/sentinel-acs

# 2. Configurar env
cp .env.example .env
$EDITOR .env   # preencher Voalle, FreeRADIUS, etc.

# 3. Instalar ferramentas Go (uma vez)
make setup

# 4. Resolver dependências
make tidy

# 5. Subir stack
docker compose -f deploy/docker-compose.yml --env-file .env up -d

# 6. Aplicar migrations (quando existirem na Fase 1+)
make migrate-up

# Para produção pública (Traefik + TLS):
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml --env-file .env up -d
```

## Setup local (apenas para desenvolvimento)

```bash
# Stack mínima de dev — sem acesso a Voalle/FreeRADIUS reais
cp .env.example .env
make setup
make tidy
make up           # sobe Postgres, Redis, Mongo, GenieACS
make dev          # hot reload com air
```

A app sobe em <http://localhost:8080>. `/healthz` retorna `{"status":"ok","version":"..."}`.

## Decisões arquiteturais

Veja [docs/adr/](docs/adr/). Principais:

- [ADR-0001 — Stack escolhida](docs/adr/0001-stack-escolhida.md)

## Comandos úteis

`make help` lista todos os alvos disponíveis.

## Próximos passos da Fase 0

Ver [ROADMAP.md — Fase 0](ROADMAP.md#fase-0--fundação-23-sem). Pendentes:

- CP-0.3 — Tailwind + air rodando junto via `make dev`
- CP-0.4 — `/healthz` checando PG/Redis/GenieACS NBI (atualmente só responde `ok`)
- CP-0.5 — middleware `correlation_id`
- CP-0.6 — `koanf` carregando config
- CP-0.7 — CI verde em PR
- CP-0.8 — ADR-0001 ✅
- CP-0.9 — testar conexão de leitura ao `radacct` do FreeRADIUS
