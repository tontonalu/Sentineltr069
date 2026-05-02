# SentinelACS — Plataforma de Gestão TR-069

**Documento de Planejamento e Arquitetura**

| Campo | Valor |
|---|---|
| **Projeto** | SentinelACS |
| **Versão do documento** | 1.1 |
| **Data** | 01/05/2026 |
| **Responsável técnico** | Weverton |
| **Stack principal** | Go 1.23+ · HTMX · Templ · PostgreSQL · TimescaleDB · Redis · GenieACS |
| **Deploy** | Docker Compose em servidor próprio |
| **Licenciamento** | Proprietário (interno) |

---

## Sumário

1. [Visão Geral](#1-visão-geral)
2. [Objetivos e Requisitos](#2-objetivos-e-requisitos)
3. [Arquitetura](#3-arquitetura)
4. [Stack Técnica](#4-stack-técnica)
5. [Estrutura do Projeto](#5-estrutura-do-projeto)
6. [Modelo de Dados](#6-modelo-de-dados)
7. [Integração com GenieACS](#7-integração-com-genieacs)
8. [Sistema de Templates](#8-sistema-de-templates)
9. [Módulo de Integrações de ERP (Plugins)](#9-módulo-de-integrações-de-erp-plugins)
10. [Telemetria e Alertas](#10-telemetria-e-alertas)
11. [Segurança](#11-segurança)
12. [Roadmap em Fases](#12-roadmap-em-fases)
13. [CI/CD e Deploy](#13-cicd-e-deploy)
14. [Observabilidade](#14-observabilidade)
15. [Decisões em Aberto](#15-decisões-em-aberto)
16. [Glossário](#16-glossário)

---

## 1. Visão Geral

O **SentinelACS** é uma plataforma web de gestão centralizada de CPEs (Customer Premises Equipment) para provedores de internet, construída sobre o GenieACS como engine TR-069/CWMP. A plataforma fornece uma camada de negócio rica em cima do GenieACS, oferecendo:

- Inventário unificado de equipamentos (ONUs, ONTs, roteadores)
- Provisionamento em massa via templates por fabricante/modelo
- Telemetria histórica (Wi-Fi, clientes conectados, sinal óptico, tráfego)
- Sistema de alertas multi-canal (WhatsApp, Telegram, e-mail)
- Integração plugável com múltiplos ERPs (Voalle como primeiro)
- RBAC granular com escopo por POP/filial
- Auditoria completa de todas as operações sensíveis

A arquitetura é deliberadamente um **monolito modular** em Go, otimizada para um time pequeno de desenvolvimento e operação, mas com fronteiras de domínio claras que permitem extração de serviços no futuro caso necessário.

---

## 2. Objetivos e Requisitos

### 2.1 Objetivos de Negócio

- Reduzir tempo médio de atendimento de chamados via diagnóstico remoto
- Permitir provisionamento em massa de configurações (firmware, Wi-Fi, PPPoE)
- Centralizar telemetria para análise proativa (clientes 2.4G vs 5G, sinal óptico, etc.)
- Padronizar configurações por fabricante/modelo via templates versionados
- Manter trilha de auditoria completa para compliance interno

### 2.2 Requisitos Funcionais (MVP Completo)

| RF | Descrição |
|---|---|
| RF-01 | Login com TOTP opcional, sessão httpOnly e RBAC com scopes |
| RF-02 | Inventário de devices sincronizado com GenieACS |
| RF-03 | CRUD de fabricantes, modelos e templates de configuração |
| RF-04 | Provisionamento individual e em massa via filtros |
| RF-05 | Coleta periódica de telemetria com retenção configurável |
| RF-06 | Dashboards por device e dashboards globais por POP |
| RF-07 | Engine de regras de alerta com canais WhatsApp/Telegram/SMTP |
| RF-08 | Auditoria append-only de todas as operações sensíveis |
| RF-09 | Integração plugável com ERPs (primeiro: Voalle) |
| RF-10 | Integração com RADIUS/Juniper para sessão PPPoE |
| RF-11 | API REST documentada (OpenAPI) para integrações futuras |

### 2.3 Requisitos Não-Funcionais

| RNF | Descrição |
|---|---|
| RNF-01 | Suportar até **50 mil CPEs** ativos sem degradação |
| RNF-02 | Provisionamento em massa de **5 mil CPEs** sem travar UI |
| RNF-03 | Disponibilidade alvo de **99.5%** (até 3,6h de downtime/mês) |
| RNF-04 | RTO de 4h, RPO de 1h (backup horário) |
| RNF-05 | Rollback de deploy em até **5 minutos** |
| RNF-06 | Logs estruturados, métricas Prometheus, traces OpenTelemetry |
| RNF-07 | Segredos cifrados em repouso (chave fora do banco) |
| RNF-08 | Rate limit por usuário e por IP em endpoints sensíveis |

---

## 3. Arquitetura

### 3.1 Diagrama de Componentes

```
┌──────────────────────────────────────────────────────────────┐
│                     Navegador (HTMX + Alpine)                │
└────────────────────────┬─────────────────────────────────────┘
                         │ HTML over HTTP + SSE
┌────────────────────────▼─────────────────────────────────────┐
│              SentinelACS (Go Monolito Modular)                │
│  ┌─────────┬──────────┬──────────┬──────────┬─────────────┐  │
│  │   Web   │   API    │  Worker  │ Scheduler│  Notifier   │  │
│  │ (Templ) │ (REST)   │ (Async)  │  (Cron)  │ (WA/TG/Mail)│  │
│  └─────────┴──────────┴──────────┴──────────┴─────────────┘  │
│       │         │         │           │            │         │
│  ┌────▼─────────▼─────────▼───────────▼────────────▼──────┐  │
│  │           Application Layer (Casos de Uso)              │  │
│  └─────────────────────────────────────────────────────────┘  │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │              Domain Layer (Entidades + Regras)          │  │
│  └─────────────────────────────────────────────────────────┘  │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │      Infrastructure (Adapters: PG, Redis, GenieACS,     │  │
│  │              ERPs, RADIUS, Notifiers, etc)              │  │
│  └─────────────────────────────────────────────────────────┘  │
└──────┬───────────┬───────────┬───────────┬──────────┬────────┘
       │           │           │           │          │
   ┌───▼──┐   ┌───▼────┐  ┌───▼────┐  ┌───▼─────┐  ┌─▼────────┐
   │Genie │   │Postgres│  │ Redis  │  │Timescale│  │ Externos │
   │ ACS  │   │(config)│  │(cache+ │  │(telemetria)│ │ ERP/RAD/ │
   │ NBI  │   │        │  │ queue) │  │          │  │ WA/TG/SMTP│
   └──┬───┘   └────────┘  └────────┘  └─────────┘  └──────────┘
      │ TR-069 (CWMP)
   ┌──▼──────────────────────────────────┐
   │  CPEs (Huawei, ZTE, Intelbras, etc) │
   └─────────────────────────────────────┘
```

### 3.2 Princípios Arquiteturais

1. **Monolito modular** — um único deployable, mas com fronteiras de domínio explícitas (`internal/domain/<bounded-context>`).
2. **Clean Architecture** — dependências apontam sempre pra dentro: transport → application → domain. Infrastructure implementa interfaces definidas no domínio.
3. **Plugin Architecture para ERPs** — cada ERP é um módulo isolado implementando uma interface comum, sem afetar o core.
4. **Event-driven interno** — Redis Streams como bus de eventos para desacoplar provisioning, alertas e auditoria.
5. **GenieACS como appliance** — consumido via NBI HTTP, sem modificações no seu código-fonte.
6. **Workers assíncronos** — operações TR-069 são lentas (dependem do CPE atender Inform/Connection Request); UI nunca bloqueia.
7. **SSR-first com hipermídia** — HTMX devolve fragmentos HTML; sem SPA, sem state management cliente complexo.

### 3.3 Bounded Contexts

| Contexto | Responsabilidade |
|---|---|
| `identity` | Usuários, roles, permissões, sessões, TOTP |
| `inventory` | Devices, vendors, models, customers, POPs |
| `templates` | Profiles, parâmetros canônicos, mapeamentos por modelo |
| `provisioning` | Jobs de provisionamento, filas, retries |
| `telemetry` | Coleta, armazenamento e consulta de séries temporais |
| `alerting` | Regras, alertas ativos, canais de notificação |
| `audit` | Trilha append-only de eventos sensíveis |
| `integration` | Plugins de ERP, RADIUS, webhooks |

---

## 4. Stack Técnica

### 4.1 Backend

| Componente | Tecnologia | Justificativa |
|---|---|---|
| Linguagem | Go 1.23+ | Concorrência nativa, baixo footprint, deploy simples |
| HTTP Router | `chi` | Leve, idiomático, middleware composable |
| Templates | `a-h/templ` | Type-safe, compile-time, sem reflection |
| ORM/SQL | `sqlc` + `pgx` | Gera Go a partir de SQL, sem mágica de runtime |
| Migrations | `goose` | Simples, suporta SQL puro e Go |
| Validação | `go-playground/validator` | Padrão da comunidade |
| Logger | `slog` (stdlib) | Estruturado nativo, sem deps |
| Config | `koanf` | Multi-source (env, YAML, flags) |
| Testes | `testify` + `testcontainers-go` | Integração real com PG/Redis |

### 4.2 Frontend

| Componente | Tecnologia |
|---|---|
| Renderização | Server-Side com `templ` |
| Interatividade | HTMX 2.x |
| Estado local | Alpine.js (apenas onde HTMX não basta) |
| CSS | Tailwind CSS via `tailwindcss-cli` |
| Ícones | Lucide (SVG inline) |
| Tabelas | `htmx` + paginação server-side |
| Charts | Chart.js (carregado sob demanda em dashboards) |

### 4.3 Persistência

| Componente | Uso |
|---|---|
| PostgreSQL 16 | Inventário, configs, RBAC, auditoria, jobs |
| TimescaleDB | Hypertables para telemetria (mesma instância PG) |
| Redis 7 | Cache + Redis Streams (event bus + filas) |

### 4.4 Operação

| Componente | Uso |
|---|---|
| Docker / Docker Compose | Orquestração local e produção |
| Traefik 3 | Reverse proxy com TLS automático (Let's Encrypt) |
| GitHub Actions | CI/CD |
| GHCR | Registry de imagens Docker |
| Prometheus + Grafana | Métricas |
| Loki + Promtail | Logs centralizados |
| Uptime Kuma | Status page interno |

---

## 5. Estrutura do Projeto

```
sentinel-acs/
├── cmd/
│   ├── server/                  # Binário web + API
│   │   └── main.go
│   ├── worker/                  # Binário do worker (provisionamento, alertas)
│   │   └── main.go
│   └── migrate/                 # CLI de migrations
│       └── main.go
├── internal/
│   ├── domain/                  # Camada de domínio (puro, sem deps)
│   │   ├── identity/
│   │   ├── inventory/
│   │   ├── templates/
│   │   ├── provisioning/
│   │   ├── telemetry/
│   │   ├── alerting/
│   │   ├── audit/
│   │   └── integration/
│   ├── application/             # Casos de uso
│   │   ├── identity/
│   │   ├── inventory/
│   │   ├── provisioning/
│   │   └── alerting/
│   ├── infrastructure/          # Adapters externos
│   │   ├── postgres/            # repositórios sqlc
│   │   │   ├── queries/         # arquivos .sql
│   │   │   └── gen/             # código gerado
│   │   ├── redis/
│   │   ├── genieacs/            # cliente NBI
│   │   ├── radius/
│   │   ├── notifier/
│   │   │   ├── whatsapp/
│   │   │   ├── telegram/
│   │   │   └── smtp/
│   │   └── erp/                 # Plugins de ERP
│   │       ├── plugin.go        # Interface comum
│   │       ├── registry.go      # Auto-registro
│   │       ├── voalle/
│   │       ├── ixc/             # placeholder
│   │       ├── mkauth/          # placeholder
│   │       └── sgp/             # placeholder
│   ├── transport/
│   │   ├── http/
│   │   │   ├── handlers/
│   │   │   ├── middleware/
│   │   │   ├── routes.go
│   │   │   └── server.go
│   │   └── api/                 # API REST pública (OpenAPI)
│   ├── views/                   # Arquivos .templ
│   │   ├── layouts/
│   │   ├── components/
│   │   └── pages/
│   │       ├── auth/
│   │       ├── devices/
│   │       ├── templates/
│   │       ├── provisioning/
│   │       ├── alerts/
│   │       └── admin/
│   └── platform/                # Cross-cutting
│       ├── config/
│       ├── logger/
│       ├── crypto/              # Cifragem de segredos
│       ├── eventbus/            # Wrapper Redis Streams
│       └── errors/
├── migrations/                  # SQL puro
│   ├── 001_init.up.sql
│   ├── 001_init.down.sql
│   └── ...
├── deploy/
│   ├── Dockerfile
│   ├── docker-compose.yml       # dev
│   ├── docker-compose.prod.yml
│   ├── traefik/
│   └── grafana/
├── .github/
│   └── workflows/
│       ├── ci.yml
│       └── deploy.yml
├── docs/
│   ├── adr/                     # Architecture Decision Records
│   ├── runbooks/
│   ├── api/                     # OpenAPI spec
│   └── diagrams/
├── scripts/
│   ├── dev-up.sh
│   ├── seed.sh
│   └── backup.sh
├── web/
│   ├── static/                  # Assets estáticos (htmx, alpine, css build)
│   └── tailwind.config.js
├── .air.toml                    # Hot reload em dev
├── .env.example
├── .gitignore
├── Makefile
├── go.mod
├── go.sum
├── README.md
└── PROJETO.md                   # Este documento
```

---

## 6. Modelo de Dados

### 6.1 Identidade & RBAC

```sql
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,        -- argon2id
  totp_secret TEXT,                   -- cifrado
  full_name TEXT NOT NULL,
  is_active BOOLEAN DEFAULT TRUE,
  last_login_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE roles (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT UNIQUE NOT NULL,
  description TEXT
);

CREATE TABLE permissions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  resource TEXT NOT NULL,             -- ex: 'device'
  action TEXT NOT NULL,               -- ex: 'update_wifi'
  UNIQUE(resource, action)
);

CREATE TABLE role_permissions (
  role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
  permission_id UUID REFERENCES permissions(id) ON DELETE CASCADE,
  PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE user_roles (
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
  scope_id UUID,                      -- pop_id ou NULL para global
  PRIMARY KEY (user_id, role_id, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'))
);

CREATE TABLE sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  ip INET,
  user_agent TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 6.2 Inventário

```sql
CREATE TABLE pops (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT UNIQUE NOT NULL,
  city TEXT,
  state TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE vendors (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT UNIQUE NOT NULL,
  slug TEXT UNIQUE NOT NULL          -- huawei, zte, intelbras
);

CREATE TABLE device_models (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vendor_id UUID REFERENCES vendors(id),
  model TEXT NOT NULL,
  tr_data_model TEXT NOT NULL,        -- 'tr098' | 'tr181'
  description TEXT,
  UNIQUE (vendor_id, model)
);

CREATE TABLE customers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  external_id TEXT,                   -- ID no ERP (Voalle, etc)
  external_source TEXT,               -- 'voalle', 'ixc', 'manual'
  full_name TEXT NOT NULL,
  document TEXT,                      -- CPF/CNPJ
  pppoe_login TEXT UNIQUE,
  plan_name TEXT,
  address TEXT,
  status TEXT DEFAULT 'active',       -- active | suspended | cancelled
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (external_source, external_id)
);

CREATE TABLE devices (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  genieacs_id TEXT UNIQUE NOT NULL,   -- _id no GenieACS
  serial_number TEXT,
  mac TEXT,
  oui TEXT,
  model_id UUID REFERENCES device_models(id),
  customer_id UUID REFERENCES customers(id),
  pop_id UUID REFERENCES pops(id),
  status TEXT DEFAULT 'unknown',      -- online | offline | never_seen
  firmware_version TEXT,
  ip_wan INET,
  last_inform_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_devices_customer ON devices(customer_id);
CREATE INDEX idx_devices_pop ON devices(pop_id);
CREATE INDEX idx_devices_status ON devices(status);
CREATE INDEX idx_devices_last_inform ON devices(last_inform_at);
```

### 6.3 Templates

```sql
CREATE TABLE config_profiles (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  description TEXT,
  vendor_id UUID REFERENCES vendors(id),  -- NULL = genérico
  model_id UUID REFERENCES device_models(id),  -- NULL = qualquer modelo do vendor
  version INT NOT NULL DEFAULT 1,
  is_active BOOLEAN DEFAULT TRUE,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE profile_parameters (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_id UUID REFERENCES config_profiles(id) ON DELETE CASCADE,
  canonical_key TEXT NOT NULL,        -- 'wifi.ssid.2g'
  tr_path TEXT NOT NULL,              -- 'InternetGatewayDevice.LANDevice.1...'
  value_template TEXT NOT NULL,       -- '{{customer.full_name}}_2G'
  data_type TEXT NOT NULL,            -- string | int | bool
  is_secret BOOLEAN DEFAULT FALSE,
  UNIQUE (profile_id, canonical_key)
);

CREATE TABLE customer_config_snapshots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
  parameters JSONB NOT NULL,          -- snapshot completo dos params
  source TEXT,                        -- 'pull' | 'push' | 'manual'
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_snapshots_device ON customer_config_snapshots(device_id, created_at DESC);
```

### 6.4 Provisionamento

```sql
CREATE TABLE provisioning_jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  device_id UUID REFERENCES devices(id),
  profile_id UUID REFERENCES config_profiles(id),
  requested_by UUID REFERENCES users(id),
  batch_id UUID,                       -- agrupa jobs de uma operação em massa
  status TEXT DEFAULT 'queued',        -- queued | running | done | failed | cancelled
  payload JSONB NOT NULL,              -- params resolvidos
  result JSONB,
  error_message TEXT,
  retry_count INT DEFAULT 0,
  scheduled_at TIMESTAMPTZ DEFAULT NOW(),
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);

CREATE INDEX idx_jobs_status ON provisioning_jobs(status, scheduled_at);
CREATE INDEX idx_jobs_batch ON provisioning_jobs(batch_id);
CREATE INDEX idx_jobs_device ON provisioning_jobs(device_id, created_at DESC);
```

### 6.5 Auditoria

```sql
CREATE TABLE audit_logs (
  id BIGSERIAL PRIMARY KEY,
  actor_id UUID,
  actor_type TEXT NOT NULL,            -- 'user' | 'system' | 'webhook' | 'integration'
  action TEXT NOT NULL,                -- 'device.update_wifi'
  resource_type TEXT NOT NULL,
  resource_id TEXT,
  before JSONB,
  after JSONB,
  ip INET,
  user_agent TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id, created_at DESC);
CREATE INDEX idx_audit_actor ON audit_logs(actor_id, created_at DESC);

-- Garantia de append-only via role separada (sem UPDATE/DELETE)
CREATE ROLE audit_writer;
GRANT INSERT, SELECT ON audit_logs TO audit_writer;
```

### 6.6 Alertas

```sql
CREATE TABLE alert_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  description TEXT,
  condition JSONB NOT NULL,            -- DSL declarativa
  severity TEXT NOT NULL,              -- info | warning | critical
  channels JSONB NOT NULL,             -- [{type:'whatsapp', target:'+55...'}]
  is_active BOOLEAN DEFAULT TRUE,
  cooldown_minutes INT DEFAULT 15,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE alerts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  rule_id UUID REFERENCES alert_rules(id),
  device_id UUID REFERENCES devices(id),
  fired_at TIMESTAMPTZ NOT NULL,
  resolved_at TIMESTAMPTZ,
  acknowledged_at TIMESTAMPTZ,
  acknowledged_by UUID REFERENCES users(id),
  payload JSONB
);

CREATE INDEX idx_alerts_active ON alerts(rule_id) WHERE resolved_at IS NULL;
```

### 6.7 Telemetria (TimescaleDB)

```sql
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE telemetry_wifi (
  time TIMESTAMPTZ NOT NULL,
  device_id UUID NOT NULL,
  ssid TEXT,
  band TEXT,                           -- '2.4G' | '5G'
  channel INT,
  connected_clients INT,
  tx_power INT
);
SELECT create_hypertable('telemetry_wifi', 'time');
SELECT add_retention_policy('telemetry_wifi', INTERVAL '90 days');

CREATE TABLE telemetry_wan (
  time TIMESTAMPTZ NOT NULL,
  device_id UUID NOT NULL,
  rx_bytes BIGINT,
  tx_bytes BIGINT,
  optical_rx_dbm NUMERIC(6,2),
  optical_tx_dbm NUMERIC(6,2)
);
SELECT create_hypertable('telemetry_wan', 'time');
SELECT add_retention_policy('telemetry_wan', INTERVAL '180 days');

CREATE TABLE telemetry_system (
  time TIMESTAMPTZ NOT NULL,
  device_id UUID NOT NULL,
  cpu_pct NUMERIC(5,2),
  mem_pct NUMERIC(5,2),
  uptime_seconds BIGINT
);
SELECT create_hypertable('telemetry_system', 'time');
SELECT add_retention_policy('telemetry_system', INTERVAL '30 days');

-- Continuous aggregates para dashboards rápidos
CREATE MATERIALIZED VIEW telemetry_wifi_hourly
WITH (timescaledb.continuous) AS
SELECT
  time_bucket('1 hour', time) AS bucket,
  device_id,
  AVG(connected_clients) AS avg_clients,
  MAX(connected_clients) AS max_clients
FROM telemetry_wifi
GROUP BY bucket, device_id;
```

---

## 7. Integração com GenieACS

### 7.1 Topologia

GenieACS expõe três portas:

| Porta | Serviço | Quem acessa |
|---|---|---|
| **7547** | CWMP (TR-069) | CPEs (rede pública via TLS) |
| **7557** | NBI (REST API) | SentinelACS apenas (rede privada) |
| **3000** | UI Nativa | Não usada (substituída pela do SentinelACS) |
| **7567** | FS (Files) | CPEs para download de firmware |

### 7.2 Cliente Go (interface)

```go
// internal/infrastructure/genieacs/client.go
package genieacs

type Client interface {
    // Devices
    GetDevice(ctx context.Context, deviceID string) (*Device, error)
    QueryDevices(ctx context.Context, query Query, opts QueryOptions) ([]Device, error)
    DeleteDevice(ctx context.Context, deviceID string) error

    // Tasks (operações assíncronas)
    SetParameterValues(ctx context.Context, deviceID string, params []Parameter) (TaskID, error)
    GetParameterValues(ctx context.Context, deviceID string, paths []string) (TaskID, error)
    Refresh(ctx context.Context, deviceID string, paths []string) (TaskID, error)
    Reboot(ctx context.Context, deviceID string) (TaskID, error)
    FactoryReset(ctx context.Context, deviceID string) (TaskID, error)
    Download(ctx context.Context, deviceID, fileType, fileName string) (TaskID, error)

    // Connection Request (acordar CPE imediatamente)
    ConnectionRequest(ctx context.Context, deviceID string) error

    // Presets, Provisions, Files, Virtual Parameters
    UpsertPreset(ctx context.Context, preset Preset) error
    UpsertProvision(ctx context.Context, name, script string) error
    UploadFile(ctx context.Context, file File) error
    UpsertVirtualParameter(ctx context.Context, name, script string) error

    // Faults
    GetFaults(ctx context.Context, deviceID string) ([]Fault, error)
}
```

### 7.3 Estratégia de Sincronização

- **Pull periódico** (cron, 1 min): query no NBI para devices com `_lastInform` recente, atualiza tabela `devices`.
- **Webhook GenieACS → SentinelACS**: ainda não suportado nativamente; alternativa é provision script que faz HTTP POST pro backend ao detectar inform.
- **Cache em Redis** com TTL de 30s para chamadas frequentes (detalhe de device).

### 7.4 Virtual Parameters Recomendados

Crie no GenieACS para abstrair diferenças TR-098 vs TR-181:

```js
// VirtualParameter: WiFi.SSID.2G
const tr098 = declare("InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID", {value: 1});
const tr181 = declare("Device.WiFi.SSID.1.SSID", {value: 1});
return { writable: true, value: tr098.value || tr181.value };
```

Lista de virtual params canônicos a criar no início:

- `WiFi.SSID.2G`, `WiFi.SSID.5G`
- `WiFi.Password.2G`, `WiFi.Password.5G`
- `WiFi.Channel.2G`, `WiFi.Channel.5G`
- `WiFi.ConnectedClients.2G`, `WiFi.ConnectedClients.5G`
- `WAN.PPPoE.Username`, `WAN.PPPoE.Password`
- `WAN.IP`, `WAN.OpticalRx`, `WAN.OpticalTx`
- `Device.Uptime`, `Device.Firmware`, `Device.SerialNumber`

### 7.5 Fluxo de Provisionamento

```
1. Usuário aplica profile X em device Y via UI
2. SentinelACS valida permissões (RBAC) e regras
3. Resolve template (substitui {{customer.name}} etc)
4. Cria registro em provisioning_jobs (status=queued)
5. Publica evento em Redis Stream "provisioning.requested"
6. Worker consome evento:
   a. Atualiza job (status=running)
   b. POST /devices/{id}/tasks no GenieACS NBI
   c. POST /devices/{id}/tasks?connection_request (força CPE acordar)
   d. Polling do task até done/fault (timeout configurável)
   e. Atualiza job (status=done|failed) + result
   f. Cria audit_log
   g. Publica evento "provisioning.completed"
7. Backend notifica frontend via SSE (atualiza UI sem reload)
```

---

## 8. Sistema de Templates

### 8.1 Modelo Conceitual

```
ConfigProfile "Wi-Fi Padrão Residencial"
├── canonical_key: "wifi.ssid.2g"
│   ├── tr_path (Huawei HG8245H): "InternetGatewayDevice.LANDevice.1.WLAN..."
│   └── tr_path (ZTE F670L): "Device.WiFi.SSID.1.SSID"
│   └── value_template: "{{customer.full_name | upper | first_word}}_2G"
├── canonical_key: "wifi.password.2g"
│   └── value_template: "{{customer.document | last_8_digits}}"
└── ...
```

### 8.2 Engine de Templates

- Linguagem: **Pongo2** (sintaxe Jinja2/Django, mais segura que `text/template` pra inputs externos).
- Filtros customizados: `upper`, `lower`, `last_n_digits`, `first_word`, `slugify`, `mask_phone`.
- Variáveis disponíveis: `customer`, `device`, `pop`, `now`.
- **Sandbox**: nenhum acesso a filesystem, network ou exec.

### 8.3 Versionamento

- Cada edição de profile incrementa `version` e cria entrada em `config_profiles_history`.
- Devices guardam o `profile_id` + `profile_version` aplicado.
- UI alerta: "127 devices estão na versão 3, profile atual é 5. Reaplicar?"
- Reaplicação **nunca é automática** — sempre requer ação humana.

### 8.4 Aplicação em Massa

- Filtros: por POP, vendor, modelo, customer plan, tag.
- Preview obrigatório: mostra N devices afetados antes de confirmar.
- **Throttle**: máximo de 100 jobs em paralelo por padrão, configurável.
- **Approval workflow** opcional: operações > 1000 devices exigem 2ª aprovação.

---

## 9. Módulo de Integrações de ERP (Plugins)

Esta seção é crítica: você quer **Voalle como primeiro ERP**, mas com arquitetura plugável para suportar IXC, MK-Auth, SGP e outros sem afetar o core.

### 9.1 Princípios de Design

1. **Isolamento total**: cada plugin é um pacote Go separado em `internal/infrastructure/erp/<vendor>`.
2. **Interface única**: todos os plugins implementam `ERPProvider`.
3. **Auto-registro**: plugins registram-se via `init()` em uma `registry`.
4. **Configuração por plugin**: cada um tem suas próprias credenciais e endpoints.
5. **Falha isolada**: erro num plugin não derruba o sistema; é registrado e retornado pra UI.
6. **Versionamento de schema**: cada plugin define seu próprio mapeamento ERP → modelo canônico.

### 9.2 Interface do Plugin

```go
// internal/infrastructure/erp/plugin.go
package erp

import "context"

type ProviderInfo struct {
    Slug        string  // 'voalle', 'ixc', 'mkauth'
    DisplayName string  // 'Voalle'
    Version     string  // '1.0.0'
    Author      string
    Capabilities []Capability  // o que o plugin suporta
}

type Capability string

const (
    CapSyncCustomers   Capability = "sync_customers"
    CapSyncContracts   Capability = "sync_contracts"
    CapWebhookIncoming Capability = "webhook_incoming"
    CapBlockCustomer   Capability = "block_customer"
    CapUnblockCustomer Capability = "unblock_customer"
)

type Customer struct {
    ExternalID  string
    FullName    string
    Document    string
    PPPoELogin  string
    PlanName    string
    Status      string  // active|suspended|cancelled
    Address     string
    Metadata    map[string]any
}

type SyncOptions struct {
    Since      *time.Time
    PageSize   int
    Pagination *Cursor
}

type SyncResult struct {
    Customers   []Customer
    NextCursor  *Cursor
    HasMore     bool
}

// ERPProvider — interface que TODO plugin implementa
type ERPProvider interface {
    Info() ProviderInfo
    HealthCheck(ctx context.Context) error

    // Operações de leitura (obrigatórias)
    SyncCustomers(ctx context.Context, opts SyncOptions) (*SyncResult, error)
    GetCustomerByID(ctx context.Context, externalID string) (*Customer, error)

    // Operações de escrita (opcionais — verificar Capabilities)
    BlockCustomer(ctx context.Context, externalID string, reason string) error
    UnblockCustomer(ctx context.Context, externalID string) error

    // Webhook handler (opcional)
    HandleWebhook(ctx context.Context, payload []byte, headers map[string]string) (*WebhookEvent, error)
}

type WebhookEvent struct {
    Type       string  // 'customer.cancelled', 'contract.created'
    ExternalID string
    Data       map[string]any
}
```

### 9.3 Registro de Plugins

```go
// internal/infrastructure/erp/registry.go
package erp

import (
    "fmt"
    "sync"
)

type Factory func(config map[string]any) (ERPProvider, error)

var (
    mu        sync.RWMutex
    factories = map[string]Factory{}
)

func Register(slug string, factory Factory) {
    mu.Lock()
    defer mu.Unlock()
    if _, exists := factories[slug]; exists {
        panic(fmt.Sprintf("erp: plugin %q já registrado", slug))
    }
    factories[slug] = factory
}

func New(slug string, config map[string]any) (ERPProvider, error) {
    mu.RLock()
    factory, ok := factories[slug]
    mu.RUnlock()
    if !ok {
        return nil, fmt.Errorf("erp: plugin %q não encontrado", slug)
    }
    return factory(config)
}

func List() []string {
    mu.RLock()
    defer mu.RUnlock()
    out := make([]string, 0, len(factories))
    for k := range factories {
        out = append(out, k)
    }
    return out
}
```

### 9.4 Exemplo: Plugin Voalle

```go
// internal/infrastructure/erp/voalle/plugin.go
package voalle

import (
    "context"
    "github.com/celinet/sentinel-acs/internal/infrastructure/erp"
)

func init() {
    erp.Register("voalle", New)
}

type Config struct {
    BaseURL      string
    ClientID     string
    ClientSecret string
    Timeout      time.Duration
}

type Provider struct {
    cfg    Config
    client *http.Client
    token  *oauthToken
}

func New(raw map[string]any) (erp.ERPProvider, error) {
    cfg, err := parseConfig(raw)
    if err != nil {
        return nil, err
    }
    return &Provider{
        cfg:    cfg,
        client: &http.Client{Timeout: cfg.Timeout},
    }, nil
}

func (p *Provider) Info() erp.ProviderInfo {
    return erp.ProviderInfo{
        Slug:        "voalle",
        DisplayName: "Voalle",
        Version:     "1.0.0",
        Author:      "Celinet",
        Capabilities: []erp.Capability{
            erp.CapSyncCustomers,
            erp.CapSyncContracts,
            erp.CapWebhookIncoming,
            erp.CapBlockCustomer,
            erp.CapUnblockCustomer,
        },
    }
}

func (p *Provider) SyncCustomers(ctx context.Context, opts erp.SyncOptions) (*erp.SyncResult, error) {
    // Implementação específica da API Voalle
    // GET /api/v1/customers?modified_since=...
    // Retorna []Customer mapeado pro modelo canônico
}

// ... demais métodos
```

### 9.5 Configuração Multi-Plugin

```yaml
# config.yaml
erp:
  active_plugin: voalle    # apenas um ativo de cada vez no MVP
  plugins:
    voalle:
      base_url: https://api.voalle.cliente.com.br
      client_id: ${VOALLE_CLIENT_ID}
      client_secret: ${VOALLE_CLIENT_SECRET}
      timeout: 30s
      sync_interval: 5m
    ixc:
      base_url: https://ixc.exemplo.com.br/webservice/v1
      token: ${IXC_TOKEN}
      timeout: 30s
```

> **Decisão arquitetural**: no MVP, apenas **um plugin ativo por vez** — simplifica drasticamente sincronização e evita conflitos de "qual ERP é fonte de verdade". Multi-ERP simultâneo entra em fase futura se necessário.

### 9.6 Webhooks Entrantes

Endpoint genérico: `POST /webhooks/erp/{slug}`

```go
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
    slug := chi.URLParam(r, "slug")
    provider, err := h.erpService.Get(slug)
    if err != nil {
        http.Error(w, "erp não configurado", 404)
        return
    }

    body, _ := io.ReadAll(r.Body)
    event, err := provider.HandleWebhook(r.Context(), body, headersToMap(r))
    if err != nil {
        http.Error(w, "payload inválido", 400)
        return
    }

    // Publica no event bus interno
    h.eventBus.Publish(r.Context(), "erp.event", event)
    w.WriteHeader(202)
}
```

### 9.7 Fluxos Suportados

| Evento ERP | Ação no SentinelACS |
|---|---|
| `customer.created` | Cria/atualiza `customers`, vincula ao device se PPPoE bater |
| `customer.cancelled` | Marca customer como `cancelled`, dispara job de bloqueio do CPE |
| `customer.suspended` | Aplica profile "suspenso" (ex: redireciona pra portal de pagamento) |
| `contract.plan_changed` | Reavalia profile aplicado, agenda reprovisionamento |

---

## 10. Telemetria e Alertas

### 10.1 Coleta

- Worker `telemetry-collector` roda a cada 5 min.
- Para cada device online, executa `Refresh` em batch nos paths canônicos.
- Após task done, lê valores e insere em hypertables.
- **Otimização**: Refresh em chunks de 200 devices, 5 chunks paralelos.

### 10.2 Engine de Alertas

DSL declarativa para regras (armazenada como JSONB):

```json
{
  "name": "POP com mais de 10% offline",
  "trigger": {
    "type": "aggregate",
    "metric": "device_status",
    "filter": {"pop_id": "{{pop_id}}", "status": "offline"},
    "aggregation": "count_pct",
    "operator": ">",
    "threshold": 10,
    "window": "5m"
  },
  "severity": "critical",
  "channels": [
    {"type": "whatsapp", "target": "+5579999990000"},
    {"type": "telegram", "chat_id": "-100123456"}
  ],
  "cooldown_minutes": 30
}
```

### 10.3 Canais de Notificação

| Canal | Implementação |
|---|---|
| WhatsApp | Evolution API (self-hosted) |
| Telegram | Bot API direto (`go-telegram/bot`) |
| E-mail | SMTP via `wneessen/go-mail` |
| Webhook | POST genérico configurável (futuro) |

---

## 11. Segurança

### 11.1 Princípios

- **Defense in depth**: múltiplas camadas independentes
- **Least privilege**: cada componente tem só as permissões mínimas
- **Secrets fora do código**: env vars + Docker secrets em produção
- **Audit everything**: toda ação sensível gera registro append-only

### 11.2 Controles Específicos

| Camada | Controle |
|---|---|
| Rede | GenieACS NBI (7557) NUNCA exposto publicamente — apenas rede Docker interna |
| Rede | CWMP (7547) com TLS + auth básica derivada do serial do CPE |
| Rede | UI exposta via Traefik com Let's Encrypt + HSTS |
| Aplicação | Senhas com Argon2id (não bcrypt) |
| Aplicação | TOTP opcional para usuários, obrigatório para admins |
| Aplicação | Sessões httpOnly + SameSite=strict + rotação ao login |
| Aplicação | CSRF token em todos os forms HTMX |
| Aplicação | Rate limit por user e por IP (token bucket via Redis) |
| Aplicação | Sanitização de templates Pongo2 (sandbox sem I/O) |
| Aplicação | Validação rigorosa de inputs com `validator` |
| Aplicação | CSP estrita, sem `unsafe-inline` |
| Dados | Segredos (senhas Wi-Fi, PPPoE) cifrados com `age` (chave fora do banco) |
| Dados | `audit_logs` com role write-only (sem UPDATE/DELETE) |
| Dados | Backup horário PG + diário MongoDB GenieACS, off-site cifrado |
| Operação | Aprovação 2-eyes para operações > 1000 devices |
| Operação | Logs estruturados com correlation_id, sem PII em texto puro |

### 11.3 Modelagem de Ameaças (resumo STRIDE)

| Ameaça | Mitigação |
|---|---|
| **S**poofing — operador falso | TOTP obrigatório para admins, sessão curta |
| **T**ampering — alteração de configs | Audit log append-only com diff before/after |
| **R**epudiation — operador nega ação | Audit log com IP, user_agent, timestamp |
| **I**nfo Disclosure — vazamento de senhas | Cifragem em repouso + máscara em UI/logs |
| **D**oS — provisionamento em massa malicioso | Rate limit + throttle por usuário |
| **E**oP — elevação de privilégio | RBAC com scope, sem rota com permissão hardcoded |

---

## 12. Roadmap em Fases

| Fase | Duração | Marco |
|---|---|---|
| **0 — Fundação** | 1-2 sem | `make dev` sobe stack completa local |
| **1 — Identidade & RBAC** | 1 sem | Admin cria usuário com permissões granulares |
| **2 — Inventário & GenieACS** | 2 sem | Dashboard mostra todos os CPEs com status |
| **3 — Templates & Provisionamento** | 2-3 sem | Trocar SSID de 100 CPEs em massa via UI |
| **4 — Telemetria & Dashboards** | 2 sem | Gráfico de clientes conectados das últimas 24h |
| **5 — Alertas & Notificações** | 1-2 sem | WhatsApp ao detectar POP com >10% offline |
| **6 — Plugin Voalle** | 2 sem | Customers sincronizados; cancelamento bloqueia CPE |
| **7 — RADIUS & PPPoE** | 1 sem | Visualização de sessão PPPoE ativa por device |
| **8 — Auditoria & Hardening** | 1 sem | Audit log com diff em todas ações sensíveis |
| **9 — CI/CD & Deploy** | paralelo | Push na main → produção em 5 min com rollback |

**Total estimado**: 13-16 semanas em tempo integral · 4-6 meses em paralelo com outras atividades.

### Próximas integrações (pós-MVP)

- Plugin IXC Provedor
- Plugin MK-Auth
- Plugin SGP
- Multi-ERP simultâneo
- App mobile (React Native ou PWA)
- Auto-remediation (alerta dispara provisionamento corretivo)

---

## 13. CI/CD e Deploy

### 13.1 Estratégia

- Branch `main` é a única que deploya em produção
- PRs obrigatórios com CI verde
- Tags semânticas (`v1.2.3`) geram releases no GitHub
- Imagens Docker em `ghcr.io/celinet/sentinel-acs` taggeadas com SHA + tag semântica

### 13.2 Pipeline CI (`.github/workflows/ci.yml`)

```yaml
name: CI
on:
  push:
    branches: [main, develop]
  pull_request:

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - uses: golangci/golangci-lint-action@v6

  templ-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: go install github.com/a-h/templ/cmd/templ@latest
      - run: templ generate
      - run: git diff --exit-code  # falha se templ generate gerou diff

  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: go test -race -coverprofile=cover.out ./...
      - uses: codecov/codecov-action@v4

  build:
    runs-on: ubuntu-latest
    needs: [lint, templ-check, test]
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          context: .
          file: deploy/Dockerfile
          push: true
          tags: |
            ghcr.io/celinet/sentinel-acs:${{ github.sha }}
            ghcr.io/celinet/sentinel-acs:latest
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

### 13.3 Pipeline Deploy (`.github/workflows/deploy.yml`)

```yaml
name: Deploy
on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: production
    steps:
      - uses: actions/checkout@v4

      - name: Deploy via SSH
        uses: appleboy/ssh-action@v1
        with:
          host: ${{ secrets.PROD_HOST }}
          username: ${{ secrets.PROD_USER }}
          key: ${{ secrets.PROD_SSH_KEY }}
          script: |
            cd /opt/sentinel-acs
            export IMAGE_TAG=${{ github.sha }}
            docker compose -f docker-compose.prod.yml pull app worker
            docker compose -f docker-compose.prod.yml up -d --no-deps app worker

            # Health check pós-deploy
            for i in {1..30}; do
              if curl -fs http://localhost:8080/healthz | grep -q "${{ github.sha }}"; then
                echo "Deploy OK"; exit 0
              fi
              sleep 2
            done
            echo "Deploy FALHOU — fazendo rollback"
            export IMAGE_TAG=$(cat .last-good-sha)
            docker compose -f docker-compose.prod.yml up -d --no-deps app worker
            exit 1

      - name: Notificar Telegram
        if: always()
        run: |
          curl -s -X POST https://api.telegram.org/bot${{ secrets.TG_TOKEN }}/sendMessage \
            -d chat_id=${{ secrets.TG_CHAT }} \
            -d text="Deploy ${{ job.status }} — ${{ github.sha }}"
```

### 13.4 Dockerfile Multi-stage

```dockerfile
# deploy/Dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go install github.com/a-h/templ/cmd/templ@latest && templ generate
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(git rev-parse HEAD)" \
    -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(git rev-parse HEAD)" \
    -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/server /usr/local/bin/
COPY --from=builder /out/worker /usr/local/bin/
COPY --from=builder /out/migrate /usr/local/bin/
COPY migrations /opt/migrations
COPY web/static /opt/static
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
```

### 13.5 docker-compose.prod.yml (resumo)

```yaml
version: '3.9'
services:
  traefik:
    image: traefik:v3
    # ... configuração padrão Traefik com Let's Encrypt

  app:
    image: ghcr.io/celinet/sentinel-acs:${IMAGE_TAG:-latest}
    env_file: .env.prod
    depends_on: [postgres, redis, genieacs]
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.app.rule=Host(`acs.celinet.com.br`)"
      - "traefik.http.routers.app.tls.certresolver=le"
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/healthz"]
      interval: 10s
    restart: unless-stopped

  worker:
    image: ghcr.io/celinet/sentinel-acs:${IMAGE_TAG:-latest}
    command: ["/usr/local/bin/worker"]
    env_file: .env.prod
    depends_on: [postgres, redis, genieacs]
    restart: unless-stopped

  postgres:
    image: timescale/timescaledb:latest-pg16
    volumes: [pgdata:/var/lib/postgresql/data]
    env_file: .env.prod
    restart: unless-stopped

  redis:
    image: redis:7-alpine
    command: redis-server --requirepass ${REDIS_PASSWORD}
    volumes: [redisdata:/data]
    restart: unless-stopped

  genieacs:
    image: drumsergio/genieacs:latest
    # ... configuração GenieACS com MongoDB

  mongo:
    image: mongo:7
    volumes: [mongodata:/data/db]

volumes: { pgdata: {}, redisdata: {}, mongodata: {} }
```

---

## 14. Observabilidade

### 14.1 Logs

- Formato: JSON estruturado via `slog`
- Campos obrigatórios: `time`, `level`, `msg`, `correlation_id`, `user_id` (quando aplicável)
- Coleta: Promtail → Loki
- Retenção: 30 dias hot, 6 meses cold (S3 compatível)

### 14.2 Métricas

- Endpoint `/metrics` (Prometheus)
- Métricas customizadas:
  - `sentinel_provisioning_jobs_total{status,vendor}`
  - `sentinel_genieacs_request_duration_seconds`
  - `sentinel_active_devices`
  - `sentinel_alert_firing{severity}`

### 14.3 Tracing

- OpenTelemetry com OTLP exporter
- Backend: Tempo ou Jaeger (decisão futura)
- Spans em: HTTP handlers, GenieACS calls, ERP plugin calls, DB queries pesadas

### 14.4 Alertas Operacionais (Alertmanager)

- App down > 1 min
- GenieACS NBI inacessível > 30s
- Worker lag > 5 min
- DB connections > 80% pool
- Disco do PG > 80%

---

## 15. Decisões em Aberto

Pendências a resolver durante implementação:

1. **Multi-tenancy real?** Hoje a premissa é "só Celinet". Se vai virar SaaS, precisa repensar isolamento de dados desde já.
2. **Estratégia de versionamento de templates** — como tratar devices "atrasados" em versões antigas?
3. **Aprovação 2-eyes em massa** — limite (ex: > 1000 devices) e papéis aprovadores.
4. **Backup off-site** — qual provedor (Backblaze B2, Wasabi, Hetzner Storage Box)?
5. **WhatsApp** — Evolution API confirmada ou WPPConnect? Decisão impacta integração.
6. **Política de retenção de telemetria** — 90 dias é suficiente? Algumas analises (sazonalidade) pedem 1 ano.
7. **Gestão de firmware** — escopo da fase futura: workflow de homologação + rollout gradual.

---

## 16. Glossário

| Termo | Definição |
|---|---|
| **ACS** | Auto Configuration Server — servidor TR-069 |
| **CPE** | Customer Premises Equipment — equipamento na casa do assinante |
| **CWMP** | CPE WAN Management Protocol (= TR-069) |
| **NBI** | Northbound Interface — API administrativa do GenieACS |
| **TR-069** | Padrão BBF para gestão remota de CPEs |
| **TR-098 / TR-181** | Data models específicos (TR-098 legado, TR-181 atual) |
| **Inform** | Mensagem CWMP do CPE pro ACS (heartbeat + eventos) |
| **Connection Request** | Mensagem do ACS pro CPE forçando sessão imediata |
| **Preset** | Regra declarativa do GenieACS aplicada a CPEs que atendem filtro |
| **Provision** | Script JS executado pelo GenieACS durante sessão CWMP |
| **Virtual Parameter** | Parâmetro calculado, abstrai diferenças TR-098 vs TR-181 |
| **POP** | Point of Presence — ponto de presença físico do provedor |
| **PPPoE** | Point-to-Point Protocol over Ethernet — autenticação na rede |
| **RBAC** | Role-Based Access Control |
| **RTO / RPO** | Recovery Time / Recovery Point Objective |

---

## Apêndice A — Referências

- [GenieACS Documentation](https://docs.genieacs.com/)
- [TR-069 Specification (Broadband Forum)](https://www.broadband-forum.org/technical/download/TR-069.pdf)
- [TR-181 Data Model](https://www.broadband-forum.org/technical/download/TR-181_Issue-2_Amendment-15.pdf)
- [HTMX Documentation](https://htmx.org/docs/)
- [Templ Documentation](https://templ.guide/)
- [sqlc](https://sqlc.dev/)
- [TimescaleDB](https://docs.timescale.com/)

---

*Documento vivo — atualizar a cada fase concluída. Última revisão: 01/05/2026 (v1.1 — renomeação CelinetACS → SentinelACS).*