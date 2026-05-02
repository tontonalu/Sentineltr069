# Roadmap SentinelACS — Plano com Checkpoints

**Documento de execução** — derivado de [SentinelACS.md](SentinelACS.md) §12.
Cada checkpoint é binário (passa/não passa) e serve como gate para a fase seguinte.

| Campo | Valor |
|---|---|
| Versão | 1.2 |
| Data | 02/05/2026 |
| Status | Fase 0 ✅ · Fase 1 em execução |

## Realidade do projeto

- **Time**: solo dev (Weverton) em **tempo integral** (40h+/sem) — prazos do doc original ajustados em ~50% para refletir uma pessoa só.
- **ERP**: Voalle **já em produção** com base ativa de clientes — sync de customers precisa ser antecipado para antes da Fase 3 (templates dependem de variáveis de customer).
- **RADIUS**: **FreeRADIUS** (banco com tabela `radacct`) + concentrador **Huawei NE8000** — não há Juniper. Integração da Fase 7 lê o banco do FreeRADIUS, não consulta o roteador via NETCONF.
- **GenieACS**: ainda **não existe** em produção — passa a ser pré-requisito explícito antes da Fase 0.
- **MVP**: Fases 0-5 (inclui telemetria e alertas). Plugin Voalle completo (write/webhook), RADIUS e auditoria avançada são pós-MVP.

---

## Sumário

- [Pré-flight](#pré-flight-resolver-antes-da-fase-0)
- [Fase 0 — Fundação](#fase-0--fundação-23-sem)
- [Fase 1 — Identidade & RBAC](#fase-1--identidade--rbac-152-sem)
- [Fase 2 — Inventário & GenieACS](#fase-2--inventário--genieacs-3-sem)
- [Fase 2.5 — Voalle Read-Only](#fase-25--voalle-read-only-1-sem)
- [Fase 3 — Templates & Provisionamento](#fase-3--templates--provisionamento-34-sem)
- [Fase 4 — Telemetria & Dashboards](#fase-4--telemetria--dashboards-3-sem)
- [Fase 5 — Alertas & Notificações](#fase-5--alertas--notificações-23-sem)
- [Fase 6 — Voalle Completo](#fase-6--voalle-completo-write--webhook-23-sem)
- [Fase 7 — Sessão PPPoE (FreeRADIUS)](#fase-7--sessão-pppoe-freeradius-12-sem)
- [Fase 8 — Auditoria & Hardening](#fase-8--auditoria--hardening-12-sem)
- [Fase 9 — CI/CD & Deploy](#fase-9--cicd--deploy-paralelo)
- [Cronograma agregado](#cronograma-agregado)

---

## Pré-flight (resolver antes da Fase 0)

### Pré-requisitos de infraestrutura

- **Pré-req-A** — Subir **GenieACS em produção (ou homologação estável)**:
  - Porta 7547 (CWMP) exposta com TLS para os CPEs.
  - Porta 7557 (NBI) restrita à rede Docker — nunca pública.
  - MongoDB com volume persistente + backup diário.
  - Virtual Parameters canônicos da §7.4 do doc principal criados (`WiFi.SSID.2G/5G`, `WiFi.Password.*`, `WAN.PPPoE.*`, `WAN.OpticalRx/Tx`, `Device.Uptime`, `Device.Firmware`, etc.).
  - Pelo menos 1 CPE de teste informando.
- **Pré-req-B** — Validar acesso de leitura ao banco do **FreeRADIUS**:
  - Confirmar dialeto (PostgreSQL ou MySQL) e versão.
  - Criar usuário read-only com SELECT em `radacct`.
  - Testar conectividade do host de dev até o banco.
  - Documentar campos relevantes: `acctstarttime`, `acctsessionid`, `framedipaddress`, `nasipaddress`, `username`, `acctinputoctets`, `acctoutputoctets`.

### Decisões a fechar

| # | Decisão | Trava qual fase? | Sugestão inicial |
|---|---|---|---|
| 1 | Multi-tenancy real? | Fase 1 (modelo de dados) | Fechar em "single-tenant Celinet" agora |
| 2 | Versionamento templates | Fase 3 | Reaplicação manual; nunca automática |
| 3 | Limite 2-eyes | Fase 8 | >1.000 devices, papel `senior_operator` aprova |
| 4 | Backup off-site | Fase 9 | Backblaze B2 (custo + simplicidade) |
| 5 | WhatsApp gateway | Fase 5 | **Evolution API confirmada** |
| 6 | Retenção telemetria | Fase 4 | 90d hot + downsample para 1 ano |

---

## Fase 0 — Fundação (2-3 sem)

**Objetivo:** repositório navegável e stack subindo localmente.

- [x] **CP-0.1** — Repo inicializado: `go.mod`, estrutura `cmd/`, `internal/`, `migrations/`, `deploy/`, `web/`, `Makefile`
- [x] **CP-0.2** — `docker-compose.yml` sobe Postgres+TimescaleDB, Redis, Mongo, GenieACS (cwmp/nbi/fs), app, worker. Pode ser usado tanto em dev local quanto no servidor da empresa
- [x] **CP-0.3** — `make dev` executa: deps + `templ generate` + tailwind + `air`. Targets `tailwind`/`tailwind-watch` adicionados
- [x] **CP-0.4** — `/healthz` retorna 200 com versão (SHA via -ldflags) e checa PG (Ping)/Redis (PING)/GenieACS NBI (GET /devices)
- [x] **CP-0.5** — `slog` JSON + middleware `correlation_id` (UUIDv4) propagando via context e header `X-Correlation-ID`
- [x] **CP-0.6** — `koanf` carregando defaults → `config.yaml` → env (mapeamento explícito em `envBindings`)
- [x] **CP-0.7** — Pipeline CI completo: lint (golangci-lint), templ-check, test (race+cover), security (gosec+govulncheck), build (GHCR)
- [x] **CP-0.8** — ADR-0001 em `docs/adr/0001-stack-escolhida.md`
- [ ] **CP-0.9** — Conexão de leitura ao `radacct` testada (depende de credenciais reais do FreeRADIUS — Pré-req-B)

**Definition of Done:** dev clona o repo e em <10 min tem o ambiente rodando.

**Hardening adicional (não estava no plano original, mas exigido pelo deploy em servidor remoto):**
- [x] **HARD-1** — `assets.go` na raiz com `//go:embed` para `migrations/` e `web/static/`. **Nada legível no FS de produção** — só os 3 binários
- [x] **HARD-2** — Build com `-trimpath -ldflags="-s -w -buildid="` no Dockerfile e Makefile

---

## Fase 1 — Identidade & RBAC (1.5-2 sem)

**Objetivo:** login funcional com sessão e permissões granulares.

- [x] **CP-1.1** — Migrations 001: `users`, `roles`, `permissions`, `role_permissions`, `user_roles`, `sessions` + roles de sistema (superadmin/operator/viewer) com permissões pré-atribuídas. Embutida no binário via `//go:embed`
- [x] **CP-1.2** — Argon2id em `platform/crypto/argon2.go` — `HashPassword`, `VerifyPassword` (constant-time), `NeedsRehash` (upgrade transparente). Defaults OWASP 2024
- [x] **CP-1.3** — Login HTMX em `internal/views/pages/auth/login.templ` + handler em `handlers/auth.go`. CSRF via middleware double-submit cookie (`middleware/csrf.go`). Cookie sessão `HttpOnly + SameSite=Strict + Secure(prod)`
- [x] **CP-1.4** — `middleware/auth.go`: `RequireAuth` (carrega user + EffectivePermissions no context, redireciona ou HX-Redirect em falha) e `RequirePermission(resource, action)`
- [x] **CP-1.5** — TOTP completo:
  - `application/identity/totp.go` — Enroll (QR PNG + secret), Confirm, Verify, Disable
  - `application/identity/preauth.go` — pre-auth tokens em Redis (TTL 5min) entre senha→TOTP
  - `crypto/secret.go` — AES-256-GCM cifra `users.totp_secret` (chave em arquivo via `APP_AGE_KEY_FILE`)
  - Pages `auth/totp.templ` (verify + enroll) com data URL do QR
- [x] **CP-1.6** — `migrate -cmd seed` cria/reativa `admin@local` (parametrizado via `SEED_ADMIN_EMAIL/PASSWORD/NAME`). Idempotente
- [x] **CP-1.7** — `/admin/users` CRUD funcional:
  - Lista paginada com toggle ativo inline (HTMX swap por linha)
  - Criar usuário com validação (`AdminService.CreateUser`) — email normalizado, senha mínima 12, ao menos 1 role
  - Detail page com roles atribuídos + atribuir/revogar role (escopo global)
  - Proteção: `user.read` para leitura, `user.write` para mutações
  - **Falta** (Fase 2+): atribuir roles com escopo de POP (POPs ainda não existem)
- [x] **CP-1.8** — Rate limit Redis (`platform/ratelimit`): janela fixa via INCR+ExpireNX. Aplicado em `/login` (10/5min por IP) e `/login/totp` (5/5min por IP). Headers `X-RateLimit-*` + `Retry-After`. Fail-open quando Redis cai
- [~] **CP-1.9** — Parte 1 ✅ testes da camada de aplicação com fakes em memória:
  - `login_test.go`: success, invalid password, user not found (anti-enumeração), inactive user, needs TOTP, expired session
  - `admin_users_test.go`: create success, validation, duplicate email, set active, assign/revoke role
  - `argon2_test.go`: hash+verify+rehash (já existente)
  - **Falta**: testes integração com testcontainers-go cobrindo Postgres real (Repos + EffectivePermissions)

**Definition of Done:** usuário não-admin logado vê apenas o que pode.

**Status atual:** 8/9 checkpoints concluídos · CP-1.9 com testes unitários ✅, integração pendente.

---

## Fase 2 — Inventário & GenieACS (3 sem)

**Objetivo:** dashboard com lista de CPEs sincronizada.

- [x] **CP-2.1** — Migration 00002 aplicada: `pops`, `vendors`, `device_models`, `customers`, `devices` + índices completos (incl. GIN em tags), trigger `updated_at`, seed de 10 vendors brasileiros, novas permissões + grants para roles existentes
- [x] **CP-2.2** — Cliente Go GenieACS NBI completo em `internal/infrastructure/genieacs/`:
  - `GetDevice`, `QueryDevices` (com filter Mongo), `DeleteDevice`
  - `SetParameterValues`, `GetParameterValues`, `Refresh`, `Reboot`, `FactoryReset`, `Download`
  - `ConnectionRequest` (refreshObject vazio + connection_request flag)
  - `GetTask`, `GetFaults`
  - `APIError` tipado para status >= 400
  - 7 testes via `httptest` cobrindo parse, encoding, basic auth e erros
- [ ] **CP-2.3** — Virtual Parameters canônicos validados (depende do Pré-req-A — GenieACS rodando)
- [x] **CP-2.4** — Sync job 1 min em `cmd/worker`:
  - `application/inventory.SyncService.Tick` — query NBI com projection canônica, fallback TR-098/TR-181/virtual params, find-or-create de vendor/model com cache por execução, link customer por PPPoE login, status calc com threshold configurável (30 min default)
  - `cmd/worker/main.go` — boot completo, primeira execução imediata, ticker 1 min, timeout 5 min por tick, graceful shutdown
  - 5 testes via httptest cobrindo: criação nova, idempotência, link customer, status offline, slugify
- [x] **CP-2.5** — Cache Redis em `GetDevice` (TTL 30s) — `Client.WithCache(redis, ttl)`. Writes (`postTask`, `DeleteDevice`) invalidam a entrada automaticamente via `defer InvalidateDevice`. Wireado em `cmd/server/main.go`
- [x] **CP-2.6** — `/devices` com lista paginada (50/página), filtros (POP, vendor, status, busca livre por serial/MAC/genieacs_id), badges de status, links para detail
- [x] **CP-2.7** — `/devices/{id}` mostra:
  - Identificação (serial, MAC, OUI, vendor, modelo + tag TR data model)
  - Conectividade (firmware, IP WAN, último inform, último boot)
  - Negócio (cliente vinculado + plano + POP)
  - Tags do GenieACS
  - **Ações com permission gating**: "Connection Request" (`device.connection_req`) e "Reboot" (`device.reboot`) com confirm
- [x] **CP-2.8** — `inventory.ComputeStatus(lastInform, now, threshold)` no domínio + uso no SyncService. Threshold parametrizado (30 min default no worker)
- [ ] **CP-2.9** — Testes com 2 CPEs reais (1 TR-098, 1 TR-181) — depende do Pré-req-A

**Status atual:** **7/9 checkpoints concluídos**. CP-2.3 e CP-2.9 dependem do GenieACS rodando.

**Definition of Done:** lista 100% dos CPEs do GenieACS com ações básicas remotas funcionando.

---

## Fase 2.5 — Voalle Read-Only (1 sem)

**Objetivo:** customers reais sincronizados antes dos templates entrarem em jogo. Sem isso, Fase 3 fica sem dados para popular `{{customer.full_name}}` e similares.

> Esta é uma versão **enxuta** do Plugin Voalle (apenas leitura). Webhook, block/unblock e captive portal ficam para a Fase 6.

- [x] **CP-2.5.1** — `internal/infrastructure/erp/voalle/`: `config.go` (parse + defaults com schema configurável), `oauth.go` (token manager com cache + retry exponencial 3x + invalidate em 401), `provider.go` (Info/HealthCheck/SyncCustomers/GetCustomerByID; Block/Unblock/Webhook devolvem `ErrCapabilityUnsupported`)
- [x] **CP-2.5.2** — `internal/infrastructure/erp/plugin.go` define `Provider`, `ProviderInfo` (com `Has(Capability)`), `Customer` canônico, `SyncOptions/Result/Cursor`, `WebhookEvent`. `errors.go` com `ErrCapabilityUnsupported`, `ErrCustomerNotFound`, `ErrAuth`
- [x] **CP-2.5.3** — `registry.go` com `Register/New/List`. Voalle registra-se via `init()`. Caller faz blank import (`_ "..../erp/voalle"`) — feito em `cmd/server` e `cmd/worker`
- [x] **CP-2.5.4** — OAuth client_credentials com token cacheado (refresh 30s antes de expirar), retry 3x com backoff exponencial, retry automático em 401 (token invalidado)
- [x] **CP-2.5.5** — `application/integration/erp_sync.go` — `ERPSyncService.Tick` paginado, mantém `lastSince` para sync incremental, idempotente (Upsert por external_source+external_id). Worker dispara a cada 5 min (configurável via `VOALLE_SYNC_INTERVAL`)
- [x] **CP-2.5.6** — Já feito em CP-2.4: `application/inventory/sync.go` busca customer por `pppoe_login` e linka. Quando Voalle popula `customers` antes do GenieACS-tick, o link acontece naturalmente
- [~] **CP-2.5.7** — `/integrations` mostra plugins registrados, status habilitado/desabilitado, BaseURL mascarado, sync interval e capabilities. **Falta**: histórico de runs (depende de StatusTracker em Redis para ser compartilhado server↔worker)

**Definition of Done:** N customers reais do Voalle no banco; M devices vinculados a customers via PPPoE.

**Status atual:** **6.5/7 checkpoints**. Plugin funcional pronto para conectar; falta apenas histórico de runs visível na UI.

**Testes:** 5 testes em `voalle/provider_test.go` (happy path, token cacheado, info, capabilities unsupported, parse config).

---

## Fase 3 — Templates & Provisionamento (3-4 sem)

**Objetivo:** mudança em massa via UI sem travar.

> Pré-requisito: variáveis `customer.*` já estão disponíveis (Fase 2.5).

- [x] **CP-3.1** — Migrations: `config_profiles`, `profile_parameters`, `config_profiles_history`, `customer_config_snapshots`, `provisioning_jobs`, `provisioning_batches` (`migrations/00003_init_templates.sql`)
- [x] **CP-3.2** — Engine sandbox **próprio** (não Pongo2 — ver nota abaixo) com filtros canônicos (`upper`, `lower`, `title`, `trim`, `first_word`, `last_word`, `last_n_digits`, `first_n_digits`, `digits_only`, `slugify`, `mask_phone`, `default`, `replace`, `substring`, `date`) + 12 testes em `engine_test.go`
- [x] **CP-3.3** — CRUD de profile + parameters em `/templates` (handlers `templates.go`, templ pages `list/form/detail`, repos `ProfileRepo` + `ParameterRepo`)
- [x] **CP-3.4** — Versionamento incremental em `Service.Update` (header OU params alterados → bump v); `config_profiles_history` snapshot append-only via `ProfileHistoryRepo`; 5 testes em `service_test.go`
- [x] **CP-3.5** — Worker (`cmd/worker`) consome stream `provisioning.requested` (Redis Streams + consumer group `provisioning-workers`) **+ polling fallback** a cada 30s (cobre Redis indisponível e retries agendados)
- [x] **CP-3.6** — Fluxo end-to-end implementado: `Service.ApplyToDevice` → render → enqueue job → worker `Executor.RunByID` → `genieacs.SetParameterValues` (com `connection_request` automático) → `MarkDone` com task_id; preview disponível em `POST /devices/{id}/templates/{profileID}/preview`
- [x] **CP-3.7** — UI/API de aplicação em massa: `Service.ApplyBulk` cria `provisioning_batches` + N jobs com `batch_id`; threshold de aprovação em **1000 devices** → `awaiting_approval`; endpoint `POST /templates/{id}/apply-bulk`
- [x] **CP-3.8** — Retry exponencial (`30s * (retry_count+1)`) em `JobRepo.MarkFailed`, max 3 tentativas em `Executor.maxRetry`; throttle por batch limit no consumer (`provisioningBatchSize=10` por iter, ~6× = 60/min worker — ajustável); `RecountFromJobs` mantém contadores agregados
- [ ] **CP-3.9** — SSE atualiza UI conforme jobs completam (sem reload) — **pendente**, JSON polling via `/provisioning/jobs/{id}` funciona
- [ ] **CP-3.10** — **Teste de carga**: 1.000 jobs em paralelo sem travar UI; 5.000 jobs em <30 min — **pendente, aguardando GenieACS de homologação**

**Nota — Engine de templates:** descartamos Pongo2 (peso ~3MB, sandbox dependente de configuração, superfície de exec/import) em favor de um engine minimalista próprio (`internal/application/templates/engine.go`). Sintaxe `{{ var.path | filter | filter:arg }}`, sandbox **por construção** (zero acesso a fs/net/exec, apenas leitura de Context). Cobre 100% dos casos do RF-03 sem ônus de dependência. Filtros são `FilterFunc` puras registradas no construtor — extensão trivial via `engine.RegisterFilter`.

**Definition of Done parcial:** profile CRUD completo, versionamento funcionando, fluxo single-device end-to-end pronto. Falta validação RNF-02 com CPEs reais (depende de Pré-req-A) e SSE para UX em massa.

---

## Fase 4 — Telemetria & Dashboards (3 sem)

**Objetivo:** dashboards históricos por device e POP.

- [x] **CP-4.1** — Hypertables `telemetry_wifi`, `telemetry_wan`, `telemetry_system` + retention policies (90d/180d/30d) + compressão segmentada por `device_id` (`migrations/00004_init_telemetry.sql`)
- [x] **CP-4.2** — Continuous aggregates `telemetry_*_hourly` com `add_continuous_aggregate_policy` (refresh a cada 30 min, janela 1h-3h offset)
- [x] **CP-4.3** — Worker `telemetry-collector` em chunks de 200 com 5 goroutines paralelas (`internal/application/telemetry/collector.go`); usa **soft refresh** via `GetDevice` (cache 30s) em vez de `Refresh` ativo — ver nota abaixo
- [x] **CP-4.4** — Parser canônico (`parser.go`) com fallback TR-098/TR-181 + virtual params preferidos (`VirtualParameters.WiFi24G_*` etc.); 6 testes em `parser_test.go`; insert via `pgx.CopyFrom` para volume
- [x] **CP-4.5** — `/devices/{id}/history?range=24h|7d|30d` renderiza **gráfico SVG inline** server-rendered (ver nota abaixo). Sumário 24h: clientes atual/média, uptime, CPU/Mem
- [ ] **CP-4.6** — `/pops/{id}` com agregados (devices online, clientes Wi-Fi totais, sinal médio) — **pendente**, depende de UI de POPs (ainda não temos handler `/pops`)
- [ ] **CP-4.7** — Coleta validada: 1.000 devices em <5 min — **pendente, aguardando GenieACS de homologação**

**Nota — Soft Refresh vs Active Refresh:** o doc original (§10.1) prevê `Refresh` em chunks com poll de task. Optei por **soft refresh** (lê snapshot do último inform via `GetDevice`) porque (1) é dramaticamente mais simples, (2) reusa o cache Redis 30s já existente, (3) granularidade efetiva de 5 min é alcançada porque CPEs informam regularmente. Active refresh fica como upgrade opcional via flag, depois que virtual params canônicos forem deployed (Pré-req-A).

**Nota — SVG inline vs Chart.js:** descartei Chart.js (~250 KB) em favor de SVG server-rendered (`history_helpers.go` gera os atributos `points` da `<polyline>`). Razões: (1) zero dependência de arquivos JS no `/static/`, (2) coerente com filosofia "binário compilado, sem arquivos soltos no servidor", (3) suficiente para os gráficos de linha duplos do MVP. Se interatividade rica (zoom, tooltips móveis) virar requisito, vale revisitar.

**Definition of Done parcial:** gráfico de clientes conectados das últimas 24h funcional via UI; falta CP-4.6 (POP) e validação de carga.

---

## Fase 5 — Alertas & Notificações (2-3 sem)

**Objetivo:** alerta multi-canal disparando com cooldown.

- [x] **CP-5.1** — Migrations `alert_rules`, `alerts`, `notifications` + permissões `alert.read/manage/acknowledge` (`migrations/00005_init_alerts.sql`)
- [x] **CP-5.2** — DSL declarativa em `internal/domain/alerting/dsl.go` com `Validate()` (operadores, agregações, métrica + janela); roundtrip JSON testado em `dsl_test.go` (4 testes)
- [x] **CP-5.3** — Engine `internal/application/alerting/engine.go` roda a cada 1 min no worker; auto-resolve quando condição limpa; idempotência por (rule, device); 5 testes em `engine_test.go`
- [x] **CP-5.4** — 3 adapters em `internal/infrastructure/notifier/`: **WhatsApp** (Evolution API), **Telegram** (Bot API direta — sem SDK pesado), **SMTP** (stdlib `net/smtp` + STARTTLS/TLS implícito); cada um habilitado independentemente via env
- [x] **CP-5.5** — Cooldown checado em `LastFiredForRule` antes de cada disparo; default 15min, configurável por regra (até 1440min)
- [x] **CP-5.6** — UI `/alerts` lista alertas ativos + tabela de regras + form CRUD (JSON DSL + canais editáveis); ack/resolve via HTMX swap
- [ ] **CP-5.7** — Teste end-to-end: derrubar 11% dos devices de um POP → alerta crítico no WhatsApp + Telegram em <2 min — **pendente, depende de Pré-req-A (GenieACS) + canais reais**

**Nota — DSL ad-hoc vs JSON Schema:** descartei `go-playground/validator` e JSON Schema externos em favor de uma struct Go com `Validate()` (~100 linhas). Razões: (1) zero deps, (2) erros de validação em português, (3) o conjunto de regras suportadas é fechado (6 metrics, 6 aggregations, 6 operators) — JSON Schema ficaria por cima de algo já tipado. Quando virar caso de uso para regras user-built complexas, vale revisitar com CEL.

**Nota — Telegram sem SDK:** o doc menciona `go-telegram/bot` (~6 MB compilado). Implementei via `net/http` direto (1 POST + parse) — alinhado com filosofia "binário enxuto". Mesmo padrão usado para Evolution API.

**Definition of Done parcial:** alertas criados, persistidos, com cooldown e auto-resolve funcionais; UI completa. **CP-5.7 (E2E real)** falta — mas sistema arquiteturalmente está MVP-ready.

**🎯 Marco — MVP fechado** ao final da fase 5. Próximas fases (6-9) já entram em pós-MVP.

---

## Fase 6 — Voalle Completo (write + webhook) (2-3 sem)

**Objetivo:** sincronização bidirecional. A versão read-only entregue na Fase 2.5 ganha capacidade de escrita e reação a eventos.

- [ ] **CP-6.1** — Implementar `BlockCustomer` e `UnblockCustomer` no provider Voalle
- [ ] **CP-6.2** — Webhook `/webhooks/erp/voalle` com validação de assinatura + idempotência por event_id
- [ ] **CP-6.3** — `customer.cancelled` → publica evento → worker aplica profile "bloqueio" no device vinculado
- [ ] **CP-6.4** — `customer.suspended` → aplica profile "captive portal" (redireciona para portal de pagamento)
- [ ] **CP-6.5** — `contract.plan_changed` → reavalia profile aplicado, agenda reprovisionamento
- [ ] **CP-6.6** — Capabilities do provider expandidas: `CapBlockCustomer`, `CapUnblockCustomer`, `CapWebhookIncoming`
- [ ] **CP-6.7** — Testes com `httptest` simulando respostas e webhooks Voalle (sem hit em prod)

**Definition of Done:** cliente cancelado no Voalle bloqueia o CPE em <2 min sem ação humana.

---

## Fase 7 — Sessão PPPoE (FreeRADIUS) (1-2 sem)

**Objetivo:** visibilidade de sessão PPPoE ativa lendo direto da contabilidade do FreeRADIUS.

> Sem NETCONF/SSH no NE8000. O NE8000 aparece apenas como `nasipaddress` na tabela `radacct`.

- [ ] **CP-7.1** — Adapter `internal/infrastructure/radius/freeradius/` com cliente para tabela `radacct` (PG ou MySQL conforme dialeto local validado no Pré-req-B)
- [ ] **CP-7.2** — Query parametrizada por PPPoE login retorna sessão ativa: `acctstarttime`, `acctsessionid`, `framedipaddress`, `nasipaddress` (= NE8000), `acctinputoctets`, `acctoutputoctets`
- [ ] **CP-7.3** — `/devices/{id}` mostra: IP da sessão, NAS-IP, uptime, bytes in/out
- [ ] **CP-7.4** — Cache de 30s para evitar martelar o banco do FreeRADIUS
- [ ] **CP-7.5** — *(opcional)* Disconnect via CoA-Disconnect se a infra suportar — caso não, pular e documentar limitação

**Definition of Done:** time de suporte vê estado real da conexão sem abrir terminal.

---

## Fase 8 — Auditoria & Hardening (1-2 sem)

**Objetivo:** trilha completa + segurança validada.

- [ ] **CP-8.1** — `audit_logs` com role `audit_writer` (sem UPDATE/DELETE concedido)
- [ ] **CP-8.2** — Decorator/middleware grava before/after JSON em toda mutation sensível
- [ ] **CP-8.3** — Cifragem `age` para `totp_secret`, `pppoe_password`, segredos de plugin (chave em env, não no DB)
- [ ] **CP-8.4** — Aprovação 2-eyes em batches >1.000 devices (workflow + UI)
- [ ] **CP-8.5** — CSP estrita (sem `unsafe-inline`); HTMX hashes + Alpine via nonce
- [ ] **CP-8.6** — **Hardening solo dev**: checklist OWASP Top 10 manual + `gosec` no CI + `govulncheck` em deps. Pen test profissional fica como item pós-MVP.
- [ ] **CP-8.7** — UI `/audit` com filtros por actor/resource/action e diff visual

**Definition of Done:** qualquer ação sensível é rastreável em <1 min com diff completo.

---

## Fase 9 — CI/CD & Deploy (paralelo)

**Objetivo:** push na main → produção em 5 min com rollback automático.

**Servidor de produção:** Debian 13 (trixie) em `177.72.177.102`, usuário `celinet`. Stack via Docker Compose. Tudo documentado em [deploy/README.md](./deploy/README.md).

- [x] **CP-9.1** — `.github/workflows/ci.yml` (lint, templ-check, test, gosec, govulncheck, docker build) — *já estava feito desde Fase 0*
- [x] **CP-9.2** — Imagem multi-stage publicada em GHCR taggeada por SHA + `latest` (mesmo workflow CI)
- [x] **CP-9.3** — `docker-compose.prod.yml` + Traefik v3 com Let's Encrypt; HSTS + headers de segurança configurados
- [x] **CP-9.4** — [`.github/workflows/deploy.yml`](./.github/workflows/deploy.yml) dispara após CI verde: rsync compose+scripts → `deploy.sh` no servidor → healthcheck 30×2s
- [x] **CP-9.5** — [`deploy/scripts/rollback.sh`](./deploy/scripts/rollback.sh) reverte para `.last-good-sha`; `deploy.sh` chama automaticamente em caso de healthcheck falho
- [x] **CP-9.6** — `state/last-good-sha` gravado após cada deploy bem-sucedido
- [x] **CP-9.7** — Notificação Telegram em sucesso (do `deploy.sh`) e em falha (do workflow GitHub Actions)
- [x] **CP-9.8** — [`deploy/scripts/backup.sh`](./deploy/scripts/backup.sh) com cron horário (PG) e diário (Mongo); cifragem com `age` (chave em `/opt/sentinelacs/secrets/age.key`); upload off-site via rclone para Backblaze B2 (opcional); subcomandos `restore-pg` e `restore-mongo` para validação fora de prod
- [x] **CP-9 extra: bootstrap idempotente** — [`deploy/scripts/bootstrap.sh`](./deploy/scripts/bootstrap.sh) provisiona Debian 13 limpo: Docker oficial + ufw (firewall com regra explícita por porta) + fail2ban + unattended-upgrades + SSH hardening (root off, password off — só após detectar key auth) + cron de backup
- [x] **CP-9 extra: init-secrets** — [`deploy/scripts/init-secrets.sh`](./deploy/scripts/init-secrets.sh) gera `/opt/sentinelacs/config/.env` com 3 secrets aleatórios (session, postgres, redis) + prompt interativo para domínio/email/owner
- [x] **CP-9 extra: Makefile remoto** — `make bootstrap-remote` / `make deploy-remote` / `make ssh` / `make logs-remote` operam o servidor do workstation
- [x] **CP-9 extra: provision.yml GHA** — workflow `Provision (1ª vez)` faz bootstrap **ponta-a-ponta via GitHub Actions**: usa `BOOTSTRAP_SSH_PASSWORD` 1× (apaga depois) → instala `SSH_PUBLIC_KEY` → roda bootstrap.sh → init-secrets non-interactive → docker login GHCR → deploy.sh → seed do admin com senha aleatória mostrada no Job Summary
- [x] **CP-9 extra: seed-admin.yml** — workflow on-demand para regenerar senha do admin (não-destrutivo se admin já existe)

**Notas operacionais:**

- Firewall: 22/80/443 + 7547 (CWMP) + 7567 (GenieACS-FS para download de firmware). NBI (7557), Postgres (5432), Redis (6379) e Mongo (27017) ficam **na rede docker interna** — nunca expostos.
- SSH hardening só desliga senha após detectar `~/.ssh/authorized_keys` populado — defesa contra trancar o operador fora.
- 2 chaves age: `secrets/age.key` (cifragem de backups) e `secrets/app.age.key` (cifragem de TOTP/credenciais runtime). **Ambas precisam de backup off-site separado** — sem elas os dados são irrecuperáveis.

**Pendentes (precisam acesso real ao servidor):**

- [ ] **Validação E2E**: rodar bootstrap → init-secrets → deploy → healthcheck verde → criar admin via `migrate -cmd seed` → login + TOTP funcionais → 1 CPE real conectando em 7547
- [ ] **Ensaio de rollback**: deploy intencionalmente quebrado → confirmar volta automática para `.last-good-sha` em <2 min (CP-9.5 validation)
- [ ] **Ensaio de restore**: pg_dump + age decrypt + pg_restore em VM isolada → confirmar dados consistentes (CP-9.8 validation)

**Definition of Done parcial:** scripts e workflows prontos. Falta a validação prática no servidor real (CP-9.5 e CP-9.8 ensaios).

---

## Cronograma agregado

| Fase | Duração ajustada | Acumulado |
|---|---|---|
| 0 — Fundação | 2-3 sem | 3 |
| 1 — Identidade & RBAC | 1.5-2 sem | 5 |
| 2 — Inventário & GenieACS | 3 sem | 8 |
| **2.5 — Voalle Read-Only** | **1 sem** | **9** |
| 3 — Templates & Provisionamento | 3-4 sem | 13 |
| 4 — Telemetria & Dashboards | 3 sem | 16 |
| 5 — Alertas & Notificações | 2-3 sem | 19 |
| **MVP fechado** | | **~19 sem (~4.5 meses)** |
| 6 — Voalle Completo | 2-3 sem | 22 |
| 7 — Sessão PPPoE (FreeRADIUS) | 1-2 sem | 24 |
| 8 — Auditoria & Hardening | 1-2 sem | 26 |
| 9 — CI/CD & Deploy | paralelo | — |

**Total ajustado:** ~26 semanas (~6 meses) tempo integral solo.

---

## Resumo de progresso (snapshot 02/05/2026)

| Fase | Concluído | Em andamento | Pendente |
|---|---|---|---|
| **0 — Fundação** | 8/9 (+ 2 hardening extra) | — | CP-0.9 (radacct test) |
| **1 — Identidade & RBAC** | 8/9 | CP-1.9 (parte testcontainers) | — |
| **2 — Inventário & GenieACS** | **7/9** | — | CP-2.3, 2.9 (deps Pré-req-A) |
| **2.5 — Voalle Read-Only** | **6.5/7** | CP-2.5.7 (histórico de runs na UI) | — |
| **3 — Templates & Provisionamento** | **8/10** | — | CP-3.9 (SSE), CP-3.10 (carga — dep Pré-req-A) |
| **4 — Telemetria & Dashboards** | **5/7** | — | CP-4.6 (UI POPs), CP-4.7 (carga — dep Pré-req-A) |
| **5 — Alertas & Notificações** | **6/7** | — | CP-5.7 (E2E — dep canais reais + GenieACS) |
| **🎯 MVP fechado** | | | restam apenas validações end-to-end com infra real |
| **9 — CI/CD & Deploy** | **8/8** + 3 extras | — | validação E2E no servidor real |
| **6-8** | — | — | pós-MVP (Voalle write, RADIUS, hardening avançado) |

**Próximas ações sugeridas (em ordem):**

1. **Smoke test integrado no servidor**:
   ```bash
   make tidy && make generate && make build && make test
   ./bin/migrate -cmd up
   SEED_ADMIN_PASSWORD='senha-12+' ./bin/migrate -cmd seed
   ./bin/worker &
   ./bin/server
   # Login → /templates → criar profile com 1 parâmetro (ex: SSID 2.4G via {{customer.pppoe_login}}_2G)
   # /devices/{id} → POST /devices/{id}/templates/{profileID}/preview → ver render
   # POST /devices/{id}/templates/{profileID}/apply → job criado, worker dispara em até 30s
   # /provisioning/jobs/{id} → status → done/failed
   ```
2. **Pré-req-A** + **CP-2.3** — criar virtual params canônicos no GenieACS (lista §7.4 do doc principal). Sem virtual params, sync usa fallback TR-098/TR-181 (já funciona)
3. **CP-2.9** — testar com 2 CPEs reais (1 TR-098, 1 TR-181) — valida o fallback de paths
4. **CP-3.6 e-2-e** — testar trocar SSID em 1 CPE real → validar `connection_request` + `setParameterValues` (depende de Pré-req-A)
5. **CP-3.9 SSE** — `GET /provisioning/batches/{id}/events` empurra updates conforme `RecountFromJobs` muda contadores; UI pode atualizar barra de progresso sem polling
6. **CP-3.10 carga** — script `loadtest/` com 1.000 jobs → métricas de tempo + sem travar UI
7. **🚀 Bootstrap do servidor 177.72.177.102** (Fase 9 prática):
   1. `ssh-copy-id celinet@177.72.177.102` (depois de gerar key local)
   2. `make bootstrap-remote` → instala Docker, ufw, fail2ban, hardening
   3. `ssh celinet@177.72.177.102 'sudo -u sentinel /opt/sentinelacs/scripts/init-secrets.sh'`
   4. apontar DNS A para o IP, login no GHCR, `make deploy-remote`
   5. configurar GitHub Secrets/Variables conforme [deploy/README.md](./deploy/README.md)
   6. seed do admin via `migrate -cmd seed`
8. **CP-4.6 — UI POPs** — handler `/pops/{id}` com tabela agregada (devices online por POP, somatório de clientes Wi-Fi por banda, sinal óptico médio). Reusa `TelemetryRepo` com filtro por POP
9. **CP-4.7 — carga telemetria** — script gera 1k devices online no GenieACS de homologação; medir duração do tick + samples gravados
10. **CP-5.7 — E2E alertas** — configurar Evolution + Telegram em homolog; criar regra "POP offline > 10%"; derrubar containers de CPE simulados; medir latência fired→message ≤ 2 min
11. **CP-9 ensaios** — provar rollback automático (deploy intencionalmente quebrado) e restore (pg_dump → age → restore em VM)
12. **CP-1.9 parte 2** — testes integração com testcontainers-go (PG real para `EffectivePermissions`)
13. **🎯 MVP em produção** — depois desses itens, sistema validado end-to-end
14. Avançar para **Fase 6** — Voalle Completo (write + webhook), pós-MVP

---

## Como usar este documento

1. Marcar `[x]` em cada checkpoint conforme for concluído (commit dedicado).
2. Não avançar para a próxima fase sem 100% dos checkpoints da fase anterior atendidos.
3. Revisão semanal: atualizar status, registrar bloqueios, ajustar estimativas.
4. Cada fase fechada gera entrada em `docs/adr/` se houve decisão arquitetural relevante.
5. **Modo solo dev**: paralelizar fases adjacentes é aceitável quando não houver dependência de dados (ex: Fase 9 desde a Fase 0). **Não paralelizar Fase 2.5 e Fase 3** — templates dependem dos customers sincronizados.

*Documento vivo — atualizar a cada checkpoint concluído.*
