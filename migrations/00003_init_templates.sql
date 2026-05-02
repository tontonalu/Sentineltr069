-- +goose Up
-- +goose StatementBegin

-- ──────────────── Config Profiles ────────────────
-- Um profile é um conjunto canônico de parâmetros aplicáveis a um vendor/modelo
-- (ou genérico, com vendor_id NULL). Versão incrementa a cada edição material.
CREATE TABLE config_profiles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    vendor_id   UUID REFERENCES vendors(id)       ON DELETE RESTRICT,
    model_id    UUID REFERENCES device_models(id) ON DELETE RESTRICT,
    version     INT  NOT NULL DEFAULT 1,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, vendor_id, model_id)
);

CREATE INDEX idx_profiles_vendor ON config_profiles(vendor_id);
CREATE INDEX idx_profiles_model  ON config_profiles(model_id);
CREATE INDEX idx_profiles_active ON config_profiles(is_active);

-- ──────────────── Profile Parameters ────────────────
-- canonical_key é a chave de negócio ('wifi.ssid.2g'); tr_path é o
-- parâmetro CWMP específico do modelo. value_template usa Pongo2.
CREATE TABLE profile_parameters (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id     UUID NOT NULL REFERENCES config_profiles(id) ON DELETE CASCADE,
    canonical_key  TEXT NOT NULL,
    tr_path        TEXT NOT NULL,
    value_template TEXT NOT NULL,
    data_type      TEXT NOT NULL,
    is_secret      BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order     INT NOT NULL DEFAULT 0,
    UNIQUE (profile_id, canonical_key),
    CHECK (data_type IN ('string', 'int', 'bool', 'unsignedInt', 'dateTime'))
);

CREATE INDEX idx_profile_params_profile ON profile_parameters(profile_id, sort_order);

-- ──────────────── Profile History ────────────────
-- Snapshot append-only: cada Save de profile-com-mudanças cria uma linha aqui
-- com a versão recém-incrementada. UI usa isso para diff de versões.
CREATE TABLE config_profiles_history (
    id          BIGSERIAL PRIMARY KEY,
    profile_id  UUID NOT NULL REFERENCES config_profiles(id) ON DELETE CASCADE,
    version     INT  NOT NULL,
    snapshot    JSONB NOT NULL,           -- profile + parameters serializados
    changed_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    change_note TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (profile_id, version)
);

CREATE INDEX idx_profile_history_profile ON config_profiles_history(profile_id, version DESC);

-- ──────────────── Customer Config Snapshots ────────────────
-- Snapshot dos params reais aplicados em um device — guarda o que foi enviado,
-- não o que veio do CPE. source distingue origem.
CREATE TABLE customer_config_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id       UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    profile_id      UUID REFERENCES config_profiles(id) ON DELETE SET NULL,
    profile_version INT,
    parameters      JSONB NOT NULL,
    source          TEXT NOT NULL,         -- 'push' | 'pull' | 'manual'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (source IN ('push', 'pull', 'manual'))
);

CREATE INDEX idx_snapshots_device ON customer_config_snapshots(device_id, created_at DESC);

-- ──────────────── Provisioning Jobs ────────────────
-- Cada SetParameterValues virtual gera um job. batch_id agrupa operações em
-- massa para acompanhamento agregado e cancelamento coletivo.
CREATE TABLE provisioning_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id     UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    profile_id    UUID REFERENCES config_profiles(id) ON DELETE SET NULL,
    requested_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    batch_id      UUID,
    status        TEXT NOT NULL DEFAULT 'queued',
    payload       JSONB NOT NULL,
    result        JSONB,
    genieacs_task_id TEXT,
    error_message TEXT,
    retry_count   INT NOT NULL DEFAULT 0,
    scheduled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled'))
);

CREATE INDEX idx_jobs_status_sched ON provisioning_jobs(status, scheduled_at);
CREATE INDEX idx_jobs_batch        ON provisioning_jobs(batch_id) WHERE batch_id IS NOT NULL;
CREATE INDEX idx_jobs_device       ON provisioning_jobs(device_id, created_at DESC);
CREATE INDEX idx_jobs_requested_by ON provisioning_jobs(requested_by);

-- ──────────────── Provisioning Batches ────────────────
-- Metadata da operação em massa (filtros aplicados, contagens, aprovação).
CREATE TABLE provisioning_batches (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id      UUID NOT NULL REFERENCES config_profiles(id) ON DELETE RESTRICT,
    profile_version INT  NOT NULL,
    requested_by    UUID NOT NULL REFERENCES users(id),
    filter_summary  TEXT NOT NULL,         -- legível: 'POP=Centro, vendor=Huawei'
    filter_payload  JSONB NOT NULL,        -- estruturado para reaplicação
    total_devices   INT  NOT NULL DEFAULT 0,
    queued          INT  NOT NULL DEFAULT 0,
    done            INT  NOT NULL DEFAULT 0,
    failed          INT  NOT NULL DEFAULT 0,
    cancelled       INT  NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'queued',
    approved_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled', 'awaiting_approval'))
);

CREATE INDEX idx_batches_status      ON provisioning_batches(status);
CREATE INDEX idx_batches_requested   ON provisioning_batches(requested_by, created_at DESC);

-- Triggers updated_at
CREATE TRIGGER profiles_updated_at BEFORE UPDATE ON config_profiles FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();

-- ──────────────── Permissões (Fase 3) ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('template', 'read',    'Listar e visualizar profiles de configuração'),
    ('template', 'manage',  'Criar/editar/desativar profiles e parâmetros'),
    ('provisioning', 'apply',     'Aplicar profile a 1 device'),
    ('provisioning', 'apply_bulk','Aplicar profile a múltiplos devices em lote'),
    ('provisioning', 'approve',   'Aprovar operações em massa que excedam o limite'),
    ('provisioning', 'read',      'Visualizar fila e histórico de jobs')
ON CONFLICT (resource, action) DO NOTHING;

-- superadmin recebe tudo de template/provisioning
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin'
   AND p.resource IN ('template', 'provisioning')
ON CONFLICT DO NOTHING;

-- operator recebe leitura + apply (não bulk, não approve, não manage)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND ((p.resource = 'template' AND p.action = 'read')
     OR (p.resource = 'provisioning' AND p.action IN ('read', 'apply')))
ON CONFLICT DO NOTHING;

-- viewer só vê
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'viewer'
   AND ((p.resource = 'template' AND p.action = 'read')
     OR (p.resource = 'provisioning' AND p.action = 'read'))
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS profiles_updated_at ON config_profiles;

DROP TABLE IF EXISTS provisioning_batches;
DROP TABLE IF EXISTS provisioning_jobs;
DROP TABLE IF EXISTS customer_config_snapshots;
DROP TABLE IF EXISTS config_profiles_history;
DROP TABLE IF EXISTS profile_parameters;
DROP TABLE IF EXISTS config_profiles;

DELETE FROM permissions WHERE
    (resource = 'template'     AND action IN ('read','manage')) OR
    (resource = 'provisioning' AND action IN ('apply','apply_bulk','approve','read'));

-- +goose StatementEnd
