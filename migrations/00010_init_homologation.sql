-- +goose Up
-- +goose StatementBegin

-- ──────────────── Canonical Keys ────────────────
-- Catálogo padronizado de chaves de negócio (canonical_key) reusáveis entre
-- profiles. Cada chave tem hints de paths TR-098 e TR-181 conhecidos para
-- alimentar o auto-mapeamento do wizard.
CREATE TABLE canonical_keys (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key                 TEXT UNIQUE NOT NULL,
    label_pt            TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    category            TEXT NOT NULL,
    suggested_data_type TEXT NOT NULL,
    default_is_secret   BOOLEAN NOT NULL DEFAULT FALSE,
    hint_paths_tr098    TEXT[] NOT NULL DEFAULT '{}',
    hint_paths_tr181    TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (suggested_data_type IN ('string', 'int', 'bool', 'unsignedInt', 'dateTime')),
    CHECK (category IN ('wifi', 'wan', 'lan', 'mgmt', 'device', 'voice', 'other'))
);

CREATE INDEX idx_canonical_keys_category ON canonical_keys(category);

CREATE TRIGGER canonical_keys_updated_at BEFORE UPDATE ON canonical_keys
    FOR EACH ROW EXECUTE FUNCTION sentinel_touch_updated_at();

-- ──────────────── Devices: flag de laboratório ────────────────
-- Marcar 1-2 CPEs como lab evita testes destrutivos em devices de cliente.
-- Apenas labs podem virar lab_device de uma homologation_session.
ALTER TABLE devices ADD COLUMN is_homologation_lab BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX idx_devices_lab ON devices(is_homologation_lab) WHERE is_homologation_lab = TRUE;

-- ──────────────── Homologation Sessions ────────────────
-- Agregado de curta duração: representa o "carrinho" do wizard. Operador
-- abre uma sessão, sonda a árvore TR-069, marca paths interessantes, testa,
-- e ao confirmar materializa um config_profile.
CREATE TABLE homologation_sessions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lab_device_id        UUID NOT NULL REFERENCES devices(id) ON DELETE RESTRICT,
    model_id             UUID NOT NULL REFERENCES device_models(id) ON DELETE RESTRICT,
    status               TEXT NOT NULL DEFAULT 'draft',
    created_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    tree_snapshot        JSONB,                 -- Device.Raw do GenieACS após Probe
    notes                TEXT NOT NULL DEFAULT '',
    started_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at          TIMESTAMPTZ,
    generated_profile_id UUID REFERENCES config_profiles(id) ON DELETE SET NULL,
    CHECK (status IN ('draft', 'probing', 'testing', 'completed', 'abandoned'))
);

CREATE INDEX idx_hom_sessions_status      ON homologation_sessions(status);
CREATE INDEX idx_hom_sessions_lab_device  ON homologation_sessions(lab_device_id);
CREATE INDEX idx_hom_sessions_created_by  ON homologation_sessions(created_by, started_at DESC);

-- Lock otimista: só uma sessão ativa por device.
CREATE UNIQUE INDEX idx_hom_sessions_active_per_device
    ON homologation_sessions(lab_device_id)
    WHERE status IN ('draft', 'probing', 'testing');

-- ──────────────── Homologation Mappings ────────────────
-- Cada linha é um (canonical_key escolhido) ↔ (tr_path no device de lab).
-- Carrega o resultado dos testes de read/write para auditoria e filtragem
-- no Complete (só passam para o profile os que tiveram read OK).
CREATE TABLE homologation_mappings (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id       UUID NOT NULL REFERENCES homologation_sessions(id) ON DELETE CASCADE,
    canonical_key    TEXT NOT NULL,
    tr_path          TEXT NOT NULL,
    value_template   TEXT NOT NULL DEFAULT '',
    data_type        TEXT NOT NULL,
    is_secret        BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order       INT NOT NULL DEFAULT 0,
    read_status      TEXT NOT NULL DEFAULT 'pending',
    write_status     TEXT NOT NULL DEFAULT 'pending',
    read_value       TEXT,
    write_test_value TEXT,
    last_error       TEXT,
    tested_at        TIMESTAMPTZ,
    UNIQUE (session_id, canonical_key),
    CHECK (data_type IN ('string', 'int', 'bool', 'unsignedInt', 'dateTime')),
    CHECK (read_status IN ('pending', 'ok', 'fail', 'skipped')),
    CHECK (write_status IN ('pending', 'ok', 'fail', 'skipped'))
);

CREATE INDEX idx_hom_mappings_session ON homologation_mappings(session_id, sort_order);

-- ──────────────── Model Homologations ────────────────
-- Registro auditável de "este profile foi homologado para este modelo".
-- Permite múltiplos profiles homologados por modelo (WiFi, Voice, Mgmt).
-- O gate em ApplyBulk consulta essa tabela.
CREATE TABLE model_homologations (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id           UUID NOT NULL REFERENCES device_models(id) ON DELETE CASCADE,
    profile_id         UUID NOT NULL REFERENCES config_profiles(id) ON DELETE CASCADE,
    session_id         UUID REFERENCES homologation_sessions(id) ON DELETE SET NULL,
    homologated_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    homologated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status             TEXT NOT NULL DEFAULT 'homologated',
    deprecated_at      TIMESTAMPTZ,
    deprecated_reason  TEXT,
    CHECK (status IN ('homologated', 'deprecated'))
);

CREATE INDEX idx_model_hom_model   ON model_homologations(model_id);
CREATE INDEX idx_model_hom_profile ON model_homologations(profile_id);
-- Garante uma única homologação ATIVA por par (model, profile).
CREATE UNIQUE INDEX idx_model_hom_active
    ON model_homologations(model_id, profile_id)
    WHERE status = 'homologated';

-- ──────────────── Permissões ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('homologation', 'read',    'Listar e visualizar sessões e modelos homologados'),
    ('homologation', 'run',     'Iniciar sessão de homologação e testar parâmetros'),
    ('homologation', 'approve', 'Concluir sessão e gravar homologação para o modelo')
ON CONFLICT (resource, action) DO NOTHING;

-- superadmin recebe tudo de homologation
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin' AND p.resource = 'homologation'
ON CONFLICT DO NOTHING;

-- operator recebe read + run (não approve — separação de função)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND p.resource = 'homologation'
   AND p.action IN ('read', 'run')
ON CONFLICT DO NOTHING;

-- viewer só lê
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'viewer'
   AND p.resource = 'homologation'
   AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- ──────────────── Seed do catálogo de canonical_keys ────────────────
-- Conjunto inicial cobrindo os parâmetros mais comuns em CPEs brasileiros.
-- Hints baseados em modelos Huawei / ZTE / Intelbras de referência.
INSERT INTO canonical_keys (key, label_pt, description, category, suggested_data_type, default_is_secret, hint_paths_tr098, hint_paths_tr181) VALUES
    ('wifi.ssid.2g', 'SSID Wi-Fi 2.4GHz', 'Nome da rede Wi-Fi na faixa 2.4GHz', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID'],
        ARRAY['Device.WiFi.SSID.1.SSID']),
    ('wifi.password.2g', 'Senha Wi-Fi 2.4GHz', 'Pre-shared key WPA/WPA2 da rede 2.4GHz', 'wifi', 'string', TRUE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.PreSharedKey.1.KeyPassphrase',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.KeyPassphrase'],
        ARRAY['Device.WiFi.AccessPoint.1.Security.KeyPassphrase']),
    ('wifi.ssid.5g', 'SSID Wi-Fi 5GHz', 'Nome da rede Wi-Fi na faixa 5GHz', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.SSID'],
        ARRAY['Device.WiFi.SSID.5.SSID', 'Device.WiFi.SSID.2.SSID']),
    ('wifi.password.5g', 'Senha Wi-Fi 5GHz', 'Pre-shared key WPA/WPA2 da rede 5GHz', 'wifi', 'string', TRUE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.PreSharedKey.1.KeyPassphrase',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.KeyPassphrase'],
        ARRAY['Device.WiFi.AccessPoint.5.Security.KeyPassphrase']),
    ('wifi.channel.2g', 'Canal Wi-Fi 2.4GHz', 'Canal de rádio (1-13) ou 0=auto', 'wifi', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel'],
        ARRAY['Device.WiFi.Radio.1.Channel']),
    ('wifi.channel.5g', 'Canal Wi-Fi 5GHz', 'Canal de rádio 5GHz ou 0=auto', 'wifi', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.Channel'],
        ARRAY['Device.WiFi.Radio.2.Channel']),
    ('wifi.enabled.2g', 'Wi-Fi 2.4GHz ativo', 'Liga/desliga rádio 2.4GHz', 'wifi', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Enable'],
        ARRAY['Device.WiFi.Radio.1.Enable']),
    ('wifi.enabled.5g', 'Wi-Fi 5GHz ativo', 'Liga/desliga rádio 5GHz', 'wifi', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.Enable'],
        ARRAY['Device.WiFi.Radio.2.Enable']),
    ('pppoe.username', 'Usuário PPPoE', 'Login do PPPoE WAN', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username'],
        ARRAY['Device.PPP.Interface.1.Username']),
    ('pppoe.password', 'Senha PPPoE', 'Senha do PPPoE WAN', 'wan', 'string', TRUE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password'],
        ARRAY['Device.PPP.Interface.1.Password']),
    ('wan.ip', 'IP WAN', 'Endereço IPv4 atual do WAN', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress',
              'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress'],
        ARRAY['Device.IP.Interface.1.IPv4Address.1.IPAddress']),
    ('lan.dhcp.range.start', 'DHCP — IP inicial', 'Primeiro IP do pool DHCP da LAN', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.MinAddress'],
        ARRAY['Device.DHCPv4.Server.Pool.1.MinAddress']),
    ('lan.dhcp.range.end', 'DHCP — IP final', 'Último IP do pool DHCP da LAN', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.MaxAddress'],
        ARRAY['Device.DHCPv4.Server.Pool.1.MaxAddress']),
    ('lan.gateway', 'Gateway LAN', 'IP do gateway entregue pela LAN', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPRouters'],
        ARRAY['Device.DHCPv4.Server.Pool.1.IPRouters']),
    ('mgmt.acs.url', 'URL do ACS', 'URL do servidor TR-069 (CWMP)', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.URL'],
        ARRAY['Device.ManagementServer.URL']),
    ('mgmt.acs.username', 'Usuário ACS', 'Login que o CPE envia ao ACS', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.Username'],
        ARRAY['Device.ManagementServer.Username']),
    ('mgmt.acs.password', 'Senha ACS', 'Senha que o CPE envia ao ACS', 'mgmt', 'string', TRUE,
        ARRAY['InternetGatewayDevice.ManagementServer.Password'],
        ARRAY['Device.ManagementServer.Password']),
    ('mgmt.acs.periodic_interval', 'Intervalo de Inform (s)', 'Periodic Inform Interval em segundos', 'mgmt', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.PeriodicInformInterval'],
        ARRAY['Device.ManagementServer.PeriodicInformInterval']),
    ('mgmt.acs.periodic_enable', 'Inform periódico ativo', 'Habilita Inform periódico', 'mgmt', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.PeriodicInformEnable'],
        ARRAY['Device.ManagementServer.PeriodicInformEnable']),
    ('mgmt.cr.url', 'URL Connection Request', 'URL que o ACS chama para acordar o CPE', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.ConnectionRequestURL'],
        ARRAY['Device.ManagementServer.ConnectionRequestURL']),
    ('mgmt.cr.username', 'Usuário Connection Request', 'Login que o ACS usa no CR', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.ConnectionRequestUsername'],
        ARRAY['Device.ManagementServer.ConnectionRequestUsername']),
    ('mgmt.cr.password', 'Senha Connection Request', 'Senha que o ACS usa no CR', 'mgmt', 'string', TRUE,
        ARRAY['InternetGatewayDevice.ManagementServer.ConnectionRequestPassword'],
        ARRAY['Device.ManagementServer.ConnectionRequestPassword']),
    ('device.firmware.version', 'Versão de firmware', 'Versão de software corrente', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.SoftwareVersion'],
        ARRAY['Device.DeviceInfo.SoftwareVersion']),
    ('device.uptime', 'Uptime (s)', 'Segundos desde o último boot', 'device', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.UpTime'],
        ARRAY['Device.DeviceInfo.UpTime']),
    ('device.serial', 'Serial Number', 'Número de série de fábrica', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.SerialNumber'],
        ARRAY['Device.DeviceInfo.SerialNumber']),
    ('device.manufacturer', 'Fabricante', 'Manufacturer reportado pelo CPE', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.Manufacturer'],
        ARRAY['Device.DeviceInfo.Manufacturer']),
    ('device.model', 'Modelo', 'ProductClass / model name', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.ProductClass', 'InternetGatewayDevice.DeviceInfo.ModelName'],
        ARRAY['Device.DeviceInfo.ProductClass', 'Device.DeviceInfo.ModelName']),
    ('voice.sip.proxy', 'Proxy SIP', 'Endereço do proxy SIP para VoIP', 'voice', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.SIP.ProxyServer'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.SIP.ProxyServer']),
    ('voice.sip.username', 'Usuário SIP', 'Account name SIP', 'voice', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.Line.1.SIP.AuthUserName'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.Line.1.SIP.AuthUserName']),
    ('voice.sip.password', 'Senha SIP', 'Senha de autenticação SIP', 'voice', 'string', TRUE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.Line.1.SIP.AuthPassword'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.Line.1.SIP.AuthPassword'])
ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS canonical_keys_updated_at ON canonical_keys;

DROP TABLE IF EXISTS model_homologations;
DROP TABLE IF EXISTS homologation_mappings;
DROP TABLE IF EXISTS homologation_sessions;
DROP TABLE IF EXISTS canonical_keys;

ALTER TABLE devices DROP COLUMN IF EXISTS is_homologation_lab;

DELETE FROM permissions WHERE resource = 'homologation';

-- +goose StatementEnd
