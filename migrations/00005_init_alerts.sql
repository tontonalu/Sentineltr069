-- +goose Up
-- +goose StatementBegin

-- ──────────────── alert_rules ────────────────
-- Regras declarativas. condition é JSONB com a DSL canônica (ver
-- internal/domain/alerting/dsl.go). channels é array de destinos.
CREATE TABLE alert_rules (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL UNIQUE,
    description      TEXT,
    condition        JSONB NOT NULL,
    severity         TEXT  NOT NULL DEFAULT 'warning',
    channels         JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    cooldown_minutes INT     NOT NULL DEFAULT 15,
    created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (severity IN ('info', 'warning', 'critical')),
    CHECK (cooldown_minutes >= 0 AND cooldown_minutes <= 1440)
);

CREATE INDEX idx_alert_rules_active ON alert_rules(is_active) WHERE is_active = TRUE;

CREATE TRIGGER alert_rules_updated_at BEFORE UPDATE ON alert_rules
    FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();

-- ──────────────── alerts (incidentes) ────────────────
-- Um alert é uma "instância" de regra disparada. Pode ser por device
-- específico ou agregado (device_id NULL).
CREATE TABLE alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id         UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    device_id       UUID REFERENCES devices(id) ON DELETE SET NULL,
    severity        TEXT NOT NULL,
    fired_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by UUID REFERENCES users(id) ON DELETE SET NULL,
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    CHECK (severity IN ('info', 'warning', 'critical'))
);

CREATE INDEX idx_alerts_rule_active   ON alerts(rule_id)          WHERE resolved_at IS NULL;
CREATE INDEX idx_alerts_device_active ON alerts(device_id)        WHERE resolved_at IS NULL AND device_id IS NOT NULL;
CREATE INDEX idx_alerts_fired_at      ON alerts(fired_at DESC);

-- ──────────────── notifications (entregas) ────────────────
-- Append-only — registra cada tentativa de envio para debugging/audit.
-- Idempotência via (alert_id, channel_type, channel_target) — caso o engine
-- reavalie a mesma regra antes do cooldown expirar (não deveria, mas defesa).
CREATE TABLE notifications (
    id             BIGSERIAL PRIMARY KEY,
    alert_id       UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    channel_type   TEXT NOT NULL,           -- 'whatsapp' | 'telegram' | 'smtp'
    channel_target TEXT NOT NULL,           -- número, chat_id ou email
    status         TEXT NOT NULL,           -- 'sent' | 'failed' | 'dropped'
    error_message  TEXT,
    sent_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (alert_id, channel_type, channel_target)
);

CREATE INDEX idx_notifications_alert ON notifications(alert_id);

-- ──────────────── Permissões (Fase 5) ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('alert', 'read',          'Visualizar regras e alertas ativos'),
    ('alert', 'manage',        'Criar/editar/desativar regras de alerta'),
    ('alert', 'acknowledge',   'Reconhecer alertas (ack) e marcar como resolvido')
ON CONFLICT (resource, action) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin' AND p.resource = 'alert'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND p.resource = 'alert'
   AND p.action IN ('read', 'acknowledge')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'viewer' AND p.resource = 'alert' AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS alert_rules_updated_at ON alert_rules;

DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS alert_rules;

DELETE FROM permissions WHERE resource = 'alert' AND action IN ('read','manage','acknowledge');

-- +goose StatementEnd
