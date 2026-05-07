-- +goose Up
-- +goose StatementBegin

-- ──────────────── telemetry_hosts ────────────────
-- 1 linha por (device, host) por tick — coleta o que o CPE expõe via
-- InternetGatewayDevice.LANDevice.1.Hosts.Host.{i} (TR-098) ou
-- Device.Hosts.Host.{i} (TR-181). Permite mostrar a tabela de
-- "Dispositivos Conectados" e seu histórico (entra/sai da rede).
CREATE TABLE telemetry_hosts (
    time             TIMESTAMPTZ NOT NULL,
    device_id        UUID        NOT NULL,
    mac_address      TEXT        NOT NULL,
    hostname         TEXT,
    ip_address       TEXT,
    address_source   TEXT,                 -- 'DHCP' | 'Static' | ''
    layer1_interface TEXT,                 -- 'Ethernet' | 'WiFi-2.4G' | 'WiFi-5G'
    active_seconds   BIGINT,               -- LeaseTimeRemaining/AssociationTime
    signal_dbm       INT,                  -- só faz sentido para WiFi
    CHECK (mac_address <> '')
);

SELECT create_hypertable('telemetry_hosts', 'time',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists => TRUE);

-- Index principal: queries do tipo "últimos hosts deste device".
CREATE INDEX idx_telemetry_hosts_device ON telemetry_hosts (device_id, time DESC);
-- Index secundário: lookups por MAC (alertas de "dispositivo desconhecido").
CREATE INDEX idx_telemetry_hosts_mac ON telemetry_hosts (device_id, mac_address, time DESC);

SELECT add_retention_policy('telemetry_hosts', INTERVAL '30 days', if_not_exists => TRUE);

ALTER TABLE telemetry_hosts SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'device_id',
    timescaledb.compress_orderby = 'time DESC'
);
SELECT add_compression_policy('telemetry_hosts', INTERVAL '7 days', if_not_exists => TRUE);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS telemetry_hosts;

-- +goose StatementEnd
