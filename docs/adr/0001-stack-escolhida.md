# ADR-0001 — Stack escolhida

| Campo | Valor |
|---|---|
| Status | Aceito |
| Data | 02/05/2026 |
| Decisores | Weverton (solo dev) |

## Contexto

SentinelACS é uma plataforma de gestão TR-069 que precisa rodar em um servidor da empresa (rede interna), com baixo footprint operacional, time pequeno (1 dev), e integração próxima ao GenieACS, Voalle (ERP) e FreeRADIUS.

A escolha da stack precisa otimizar para:
1. Velocidade de desenvolvimento solo.
2. Deploy simples (binário único, container fino).
3. Manutenibilidade a longo prazo, com fronteiras de domínio claras.
4. Performance suficiente para ~50 mil CPEs (RNF-01).

## Decisão

Adotar a stack do [SentinelACS.md](../../SentinelACS.md) §4, sem desvios:

| Camada | Escolha |
|---|---|
| Linguagem | Go 1.23+ |
| HTTP Router | `chi` |
| Templates | `a-h/templ` |
| ORM/SQL | `sqlc` + `pgx` |
| Migrations | `goose` |
| Validação | `go-playground/validator` |
| Logger | `slog` (stdlib) |
| Config | `koanf` |
| Testes | `testify` + `testcontainers-go` |
| Frontend | HTMX 2.x + Alpine.js (mínimo) + Tailwind |
| Charts | Chart.js (carregado sob demanda) |
| Persistência | PostgreSQL 16 + TimescaleDB (mesma instância) |
| Cache / Event Bus | Redis 7 (incluindo Streams) |
| Orquestração | Docker Compose |
| Reverse Proxy | Traefik 3 com Let's Encrypt |
| CI/CD | GitHub Actions + GHCR |

## Consequências

**Positivas:**
- Type safety end-to-end (templ + sqlc) reduz bugs em tempo de execução, crítico para solo dev.
- SSR-first com HTMX evita complexidade de SPA + state management cliente.
- Monolito modular permite extração futura sem refactor disruptivo.
- Stack 100% open-source, sem vendor lock-in.

**Negativas / aceitas:**
- HTMX limita reatividade complexa — aceitável para o domínio (gestão administrativa, não app real-time).
- `templ generate` adiciona passo de build (mitigado por air em dev e Dockerfile em prod).
- Sem ORM — `sqlc` exige escrever SQL, mas é o trade-off para clareza e performance.

## Alternativas consideradas

- **Node/TypeScript + Next.js**: descartado por footprint (Node.js + npm) e complexidade de SSR híbrido.
- **Python/FastAPI**: produtivo, mas concorrência mais frágil para workers de provisionamento (TR-069 é I/O-bound massivo).
- **Rust + Axum**: excelente runtime, mas ecossistema templ-equivalente menos maduro e curva mais alta para solo dev.

## Referências

- [SentinelACS.md §4 — Stack Técnica](../../SentinelACS.md)
- [ROADMAP.md — Fase 0](../../ROADMAP.md)
