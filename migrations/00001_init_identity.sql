-- +goose Up
-- +goose StatementBegin

-- pgcrypto fornece gen_random_uuid().
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ──────────────── Usuários ────────────────
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    password_hash   TEXT NOT NULL,                  -- argon2id (ver internal/platform/crypto)
    totp_secret     TEXT,                           -- cifrado com age (chave fora do banco)
    totp_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    full_name       TEXT NOT NULL,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ──────────────── Roles & Permissions ────────────────
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT UNIQUE NOT NULL,
    description TEXT,
    is_system   BOOLEAN NOT NULL DEFAULT FALSE,    -- não pode ser deletado pela UI
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource    TEXT NOT NULL,                      -- ex: 'device'
    action      TEXT NOT NULL,                      -- ex: 'update_wifi'
    description TEXT,
    UNIQUE (resource, action)
);

CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- user_roles com escopo opcional (pop_id ou NULL para global).
-- Usamos zero-UUID como "global" para que o PK funcione sem COALESCE.
CREATE TABLE user_roles (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    scope_id   UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    granted_by UUID REFERENCES users(id),
    PRIMARY KEY (user_id, role_id, scope_id)
);

CREATE INDEX idx_user_roles_user  ON user_roles(user_id);
CREATE INDEX idx_user_roles_scope ON user_roles(scope_id) WHERE scope_id <> '00000000-0000-0000-0000-000000000000'::uuid;

-- ──────────────── Sessions ────────────────
CREATE TABLE sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip          INET,
    user_agent  TEXT,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_user    ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at) WHERE revoked_at IS NULL;

-- ──────────────── Seeds: roles e permissões padrão ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('user',     'read',         'Listar e visualizar usuários'),
    ('user',     'write',        'Criar, editar e desativar usuários'),
    ('role',     'manage',       'Gerenciar papéis e permissões'),
    ('device',   'read',         'Listar e visualizar dispositivos'),
    ('device',   'reboot',       'Solicitar reboot remoto de CPE'),
    ('device',   'update_wifi',  'Aplicar mudanças de Wi-Fi'),
    ('device',   'update_pppoe', 'Aplicar mudanças de PPPoE'),
    ('template', 'read',         'Listar templates de configuração'),
    ('template', 'write',        'Criar e editar templates'),
    ('template', 'apply',        'Aplicar template em devices'),
    ('audit',    'read',         'Visualizar trilha de auditoria'),
    ('integration', 'manage',    'Configurar integrações de ERP/RADIUS')
ON CONFLICT (resource, action) DO NOTHING;

INSERT INTO roles (name, description, is_system) VALUES
    ('superadmin', 'Acesso total ao sistema',                                TRUE),
    ('operator',   'Operações em devices e aplicação de templates',          TRUE),
    ('viewer',     'Somente leitura — dashboards, devices, audit',           TRUE)
ON CONFLICT (name) DO NOTHING;

-- superadmin: todas as permissões
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p WHERE r.name = 'superadmin'
ON CONFLICT DO NOTHING;

-- operator: leitura + ações em device e template (mas não manage role/audit)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND ((p.resource = 'device') OR (p.resource = 'template') OR (p.resource = 'user' AND p.action = 'read'))
ON CONFLICT DO NOTHING;

-- viewer: tudo que é resource.read
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'viewer' AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;

-- +goose StatementEnd
