-- +goose Up
-- +goose StatementBegin

-- ACL de IPs autorizados a falar com o ACS upstream na porta TR-069/CWMP.
-- A enforcement no kernel (iptables) virá numa fase posterior — esta migration
-- só persiste a lista para que o NOC possa cadastrar com antecedência sem
-- risco de lockout.
CREATE TABLE tr069_acl_cidrs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cidr        CIDR        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    created_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tr069_acl_cidrs_created ON tr069_acl_cidrs(created_at DESC);

-- ──────────────── Permissões ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('tr069_acl', 'read',   'Ler lista de CIDR autorizados na porta TR-069'),
    ('tr069_acl', 'manage', 'Adicionar/remover CIDR autorizados na porta TR-069')
ON CONFLICT (resource, action) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin' AND p.resource = 'tr069_acl'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator' AND p.resource = 'tr069_acl' AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- ──────────────── Seeds iniciais ────────────────
-- Faixas iniciais informadas pelo operador (rede interna + CGNAT dos clientes).
-- Importante seedar antes do enforcement subir, senão deny-all bloqueia
-- legítimos.
INSERT INTO tr069_acl_cidrs (cidr, description) VALUES
    ('177.72.176.0/21', 'Rede interna Celinet'),
    ('100.64.0.0/10',   'CGNAT — clientes')
ON CONFLICT (cidr) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS tr069_acl_cidrs;
DELETE FROM permissions WHERE resource = 'tr069_acl';

-- +goose StatementEnd
