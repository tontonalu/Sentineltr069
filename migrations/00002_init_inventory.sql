-- +goose Up
-- +goose StatementBegin

-- ──────────────── POPs ────────────────
-- Point of Presence — facilita scoping de devices/usuários por filial.
CREATE TABLE pops (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT UNIQUE NOT NULL,
    city       TEXT,
    state      TEXT,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ──────────────── Vendors & Models ────────────────
CREATE TABLE vendors (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       TEXT UNIQUE NOT NULL,                -- huawei, zte, intelbras...
    name       TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE device_models (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id     UUID NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
    model         TEXT NOT NULL,
    tr_data_model TEXT NOT NULL,                    -- 'tr098' | 'tr181'
    description   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (vendor_id, model),
    CHECK (tr_data_model IN ('tr098', 'tr181'))
);

CREATE INDEX idx_device_models_vendor ON device_models(vendor_id);

-- ──────────────── Customers ────────────────
-- external_id + external_source identificam o registro no ERP de origem.
-- pppoe_login é a chave funcional para casar device ↔ customer durante sync.
CREATE TABLE customers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id     TEXT,
    external_source TEXT,                            -- 'voalle' | 'ixc' | 'manual'
    full_name       TEXT NOT NULL,
    document        TEXT,                            -- CPF/CNPJ
    pppoe_login     TEXT UNIQUE,
    plan_name       TEXT,
    address         TEXT,
    status          TEXT NOT NULL DEFAULT 'active',  -- active | suspended | cancelled
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (external_source, external_id),
    CHECK (status IN ('active', 'suspended', 'cancelled'))
);

CREATE INDEX idx_customers_external ON customers(external_source, external_id);
CREATE INDEX idx_customers_status   ON customers(status);

-- ──────────────── Devices (CPEs) ────────────────
CREATE TABLE devices (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    genieacs_id      TEXT UNIQUE NOT NULL,            -- _id no GenieACS
    serial_number    TEXT,
    mac              TEXT,
    oui              TEXT,
    model_id         UUID REFERENCES device_models(id) ON DELETE SET NULL,
    customer_id      UUID REFERENCES customers(id)    ON DELETE SET NULL,
    pop_id           UUID REFERENCES pops(id)         ON DELETE SET NULL,
    status           TEXT NOT NULL DEFAULT 'unknown', -- online | offline | never_seen | unknown
    firmware_version TEXT,
    ip_wan           INET,
    last_inform_at   TIMESTAMPTZ,
    last_boot_at     TIMESTAMPTZ,
    tags             TEXT[] NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status IN ('online', 'offline', 'never_seen', 'unknown'))
);

CREATE INDEX idx_devices_customer    ON devices(customer_id);
CREATE INDEX idx_devices_pop         ON devices(pop_id);
CREATE INDEX idx_devices_model       ON devices(model_id);
CREATE INDEX idx_devices_status      ON devices(status);
CREATE INDEX idx_devices_last_inform ON devices(last_inform_at DESC);
CREATE INDEX idx_devices_serial      ON devices(serial_number) WHERE serial_number IS NOT NULL;
CREATE INDEX idx_devices_mac         ON devices(mac)           WHERE mac IS NOT NULL;
CREATE INDEX idx_devices_tags        ON devices USING GIN (tags);

-- Trigger pra updated_at em mudanças.
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sentinel_touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pops_updated_at        BEFORE UPDATE ON pops      FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();
CREATE TRIGGER customers_updated_at   BEFORE UPDATE ON customers FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();
CREATE TRIGGER devices_updated_at     BEFORE UPDATE ON devices   FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();

-- ──────────────── Seed: vendors comuns no Brasil ────────────────
-- Mais entram conforme novos modelos forem homologados.
INSERT INTO vendors (slug, name) VALUES
    ('huawei',     'Huawei'),
    ('zte',        'ZTE'),
    ('intelbras',  'Intelbras'),
    ('fiberhome',  'FiberHome'),
    ('nokia',      'Nokia'),
    ('parks',      'Parks'),
    ('greatek',    'Greatek'),
    ('tplink',     'TP-Link'),
    ('phyhome',    'PhyHome'),
    ('datacom',    'Datacom')
ON CONFLICT (slug) DO NOTHING;

-- ──────────────── Permissões adicionais (Fase 2) ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('pop',      'manage',         'Criar e editar POPs'),
    ('vendor',   'manage',         'Gerenciar fabricantes e modelos'),
    ('customer', 'read',           'Listar e visualizar clientes'),
    ('customer', 'manage',         'Editar dados de cliente (raro — geralmente vem do ERP)'),
    ('device',   'connection_req', 'Disparar Connection Request manual')
ON CONFLICT (resource, action) DO NOTHING;

-- Concede as novas permissões ao superadmin (já existente da migration 001).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin'
   AND p.resource IN ('pop', 'vendor', 'customer')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin' AND p.resource = 'device' AND p.action = 'connection_req'
ON CONFLICT DO NOTHING;

-- operator também ganha customer.read e device.connection_req.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND ((p.resource = 'customer' AND p.action = 'read')
     OR (p.resource = 'device'   AND p.action = 'connection_req'))
ON CONFLICT DO NOTHING;

-- viewer ganha customer.read.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'viewer' AND p.resource = 'customer' AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS devices_updated_at   ON devices;
DROP TRIGGER IF EXISTS customers_updated_at ON customers;
DROP TRIGGER IF EXISTS pops_updated_at      ON pops;
DROP FUNCTION IF EXISTS sentinel_touch_updated_at();

DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS device_models;
DROP TABLE IF EXISTS vendors;
DROP TABLE IF EXISTS pops;

-- Remove apenas as permissões adicionadas nesta migration (não tocamos roles do 001).
DELETE FROM permissions WHERE
    (resource = 'pop'      AND action = 'manage')         OR
    (resource = 'vendor'   AND action = 'manage')         OR
    (resource = 'customer' AND action IN ('read', 'manage')) OR
    (resource = 'device'   AND action = 'connection_req');

-- +goose StatementEnd
