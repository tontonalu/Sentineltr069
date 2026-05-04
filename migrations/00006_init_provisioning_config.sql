-- +goose Up
-- +goose StatementBegin

-- ──────────────── TR-069 / CWMP — Configuração de Provisionamento ────────────────
-- Tabela singleton (CHECK id=1) que guarda a configuração canônica usada pra
-- provisionar todo CPE que faz Inform: URL pública do ACS (porta CWMP 7547),
-- intervalo de Inform, credenciais default de Connection Request, e o nome
-- do preset/provision que o syncer cria/atualiza no GenieACS via NBI.
CREATE TABLE tr069_provisioning_config (
    id                 SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    cwmp_url           TEXT        NOT NULL DEFAULT '',
    inform_interval_s  INTEGER     NOT NULL DEFAULT 300
        CHECK (inform_interval_s BETWEEN 60 AND 86400),
    default_cr_user    TEXT        NOT NULL DEFAULT '',
    default_cr_pass    TEXT        NOT NULL DEFAULT '',
    preset_name        TEXT        NOT NULL DEFAULT 'sentinel-defaults',
    last_synced_at     TIMESTAMPTZ,
    last_sync_error    TEXT        NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by         UUID        REFERENCES users(id) ON DELETE SET NULL
);

-- Linha singleton — INSERT idempotente.
INSERT INTO tr069_provisioning_config (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

CREATE TRIGGER tr069_provisioning_config_updated_at
    BEFORE UPDATE ON tr069_provisioning_config
    FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();

-- ──────────────── Permissões ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('provisioning_config', 'read',   'Ler configuração TR-069/CWMP'),
    ('provisioning_config', 'manage', 'Editar config TR-069 e sincronizar com ACS upstream')
ON CONFLICT (resource, action) DO NOTHING;

-- Superadmin recebe ambas; operator recebe só read.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin' AND p.resource = 'provisioning_config'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator' AND p.resource = 'provisioning_config' AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS tr069_provisioning_config_updated_at ON tr069_provisioning_config;
DROP TABLE IF EXISTS tr069_provisioning_config;

DELETE FROM permissions WHERE resource = 'provisioning_config';

-- +goose StatementEnd
