-- +goose Up
-- +goose StatementBegin

-- ──────────────── telemetry_ports ────────────────
-- Status físico das portas Ethernet (WAN/LAN1..N). Coleta vem de:
--   TR-098: InternetGatewayDevice.{LANDevice.1.LANEthernetInterfaceConfig.{i},
--                                   WANDevice.1.WANEthernetInterfaceConfig}
--   TR-181: Device.Ethernet.Interface.{i}
-- Útil para diagnosticar cabo desconectado, negociação 100M vs 1G, etc.
CREATE TABLE telemetry_ports (
    time       TIMESTAMPTZ NOT NULL,
    device_id  UUID        NOT NULL,
    port_name  TEXT        NOT NULL,         -- 'WAN' | 'LAN1' | 'LAN2' | ...
    status     TEXT        NOT NULL,         -- 'Up' | 'Down'
    speed_mbps INT,                          -- 10 | 100 | 1000 | 2500 | ...
    duplex     TEXT,                         -- 'Full' | 'Half' | ''
    CHECK (port_name <> ''),
    CHECK (status IN ('Up', 'Down'))
);

SELECT create_hypertable('telemetry_ports', 'time',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists => TRUE);

CREATE INDEX idx_telemetry_ports_device ON telemetry_ports (device_id, time DESC);

SELECT add_retention_policy('telemetry_ports', INTERVAL '30 days', if_not_exists => TRUE);

ALTER TABLE telemetry_ports SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'device_id',
    timescaledb.compress_orderby = 'time DESC'
);
SELECT add_compression_policy('telemetry_ports', INTERVAL '7 days', if_not_exists => TRUE);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS telemetry_ports;

-- +goose StatementEnd
