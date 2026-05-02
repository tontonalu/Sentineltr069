-- +goose Up
-- +goose StatementBegin

-- TimescaleDB é EXTENSION compartilhada — deve estar instalada na imagem
-- do Postgres (ver deploy/docker-compose.yml: timescale/timescaledb-ha:pg16).
-- Goose roda como dono do banco, então CREATE EXTENSION funciona sem
-- superuser via dockerfile do Timescale.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ──────────────── telemetry_wifi ────────────────
-- 1 linha por (device, ssid, band) por inform — granularidade de 5 min
-- alinhada com o tick do collector.
CREATE TABLE telemetry_wifi (
    time              TIMESTAMPTZ      NOT NULL,
    device_id         UUID             NOT NULL,
    ssid              TEXT,
    band              TEXT,                          -- '2.4G' | '5G'
    channel           INT,
    connected_clients INT,
    tx_power          INT,
    CHECK (band IS NULL OR band IN ('2.4G', '5G'))
);

SELECT create_hypertable('telemetry_wifi', 'time',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists => TRUE);

CREATE INDEX idx_telemetry_wifi_device ON telemetry_wifi (device_id, time DESC);

SELECT add_retention_policy('telemetry_wifi', INTERVAL '90 days', if_not_exists => TRUE);

-- Compressão deixa série antiga ~10× menor (Timescale recomenda).
ALTER TABLE telemetry_wifi SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'device_id',
    timescaledb.compress_orderby = 'time DESC'
);
SELECT add_compression_policy('telemetry_wifi', INTERVAL '14 days', if_not_exists => TRUE);

-- ──────────────── telemetry_wan ────────────────
-- Métricas do uplink: tráfego acumulado + sinal óptico (quando disponível
-- via virtual param vendor-specific — vide §7.4 do doc principal).
CREATE TABLE telemetry_wan (
    time           TIMESTAMPTZ NOT NULL,
    device_id      UUID        NOT NULL,
    rx_bytes       BIGINT,
    tx_bytes       BIGINT,
    optical_rx_dbm NUMERIC(6,2),
    optical_tx_dbm NUMERIC(6,2)
);

SELECT create_hypertable('telemetry_wan', 'time',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists => TRUE);

CREATE INDEX idx_telemetry_wan_device ON telemetry_wan (device_id, time DESC);
SELECT add_retention_policy('telemetry_wan', INTERVAL '180 days', if_not_exists => TRUE);

ALTER TABLE telemetry_wan SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'device_id',
    timescaledb.compress_orderby = 'time DESC'
);
SELECT add_compression_policy('telemetry_wan', INTERVAL '14 days', if_not_exists => TRUE);

-- ──────────────── telemetry_system ────────────────
-- CPU/Mem/Uptime — útil pra diagnóstico de CPE travado.
CREATE TABLE telemetry_system (
    time            TIMESTAMPTZ NOT NULL,
    device_id       UUID        NOT NULL,
    cpu_pct         NUMERIC(5,2),
    mem_pct         NUMERIC(5,2),
    uptime_seconds  BIGINT
);

SELECT create_hypertable('telemetry_system', 'time',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists => TRUE);

CREATE INDEX idx_telemetry_system_device ON telemetry_system (device_id, time DESC);
SELECT add_retention_policy('telemetry_system', INTERVAL '30 days', if_not_exists => TRUE);

ALTER TABLE telemetry_system SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'device_id',
    timescaledb.compress_orderby = 'time DESC'
);
SELECT add_compression_policy('telemetry_system', INTERVAL '7 days', if_not_exists => TRUE);

-- ──────────────── Permissões (Fase 4) ────────────────
INSERT INTO permissions (resource, action, description) VALUES
    ('telemetry', 'read', 'Visualizar gráficos históricos e dashboards')
ON CONFLICT (resource, action) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name IN ('superadmin', 'operator', 'viewer')
   AND p.resource = 'telemetry' AND p.action = 'read'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- Continuous aggregates ficam em statement separado porque exigem AUTOCOMMIT
-- (não podem rodar dentro da transação implícita do goose).
-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_wifi_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time)        AS bucket,
    device_id,
    band,
    AVG(connected_clients)::INT        AS avg_clients,
    MAX(connected_clients)             AS max_clients,
    AVG(tx_power)::INT                 AS avg_tx_power
FROM telemetry_wifi
GROUP BY bucket, device_id, band
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_wan_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time)        AS bucket,
    device_id,
    MAX(rx_bytes) - MIN(rx_bytes)      AS rx_delta,
    MAX(tx_bytes) - MIN(tx_bytes)      AS tx_delta,
    AVG(optical_rx_dbm)                AS avg_rx_dbm,
    AVG(optical_tx_dbm)                AS avg_tx_dbm
FROM telemetry_wan
GROUP BY bucket, device_id
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_system_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time)        AS bucket,
    device_id,
    AVG(cpu_pct)                       AS avg_cpu,
    MAX(cpu_pct)                       AS max_cpu,
    AVG(mem_pct)                       AS avg_mem,
    MAX(uptime_seconds)                AS uptime_max
FROM telemetry_system
GROUP BY bucket, device_id
WITH NO DATA;
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_continuous_aggregate_policy('telemetry_wifi_hourly',
    start_offset      => INTERVAL '3 hours',
    end_offset        => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes',
    if_not_exists     => TRUE);

SELECT add_continuous_aggregate_policy('telemetry_wan_hourly',
    start_offset      => INTERVAL '3 hours',
    end_offset        => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes',
    if_not_exists     => TRUE);

SELECT add_continuous_aggregate_policy('telemetry_system_hourly',
    start_offset      => INTERVAL '3 hours',
    end_offset        => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes',
    if_not_exists     => TRUE);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP MATERIALIZED VIEW IF EXISTS telemetry_system_hourly;
DROP MATERIALIZED VIEW IF EXISTS telemetry_wan_hourly;
DROP MATERIALIZED VIEW IF EXISTS telemetry_wifi_hourly;

DROP TABLE IF EXISTS telemetry_system;
DROP TABLE IF EXISTS telemetry_wan;
DROP TABLE IF EXISTS telemetry_wifi;

DELETE FROM permissions WHERE resource = 'telemetry' AND action = 'read';

-- Não removemos a extension — pode ser compartilhada com outros serviços.

-- +goose StatementEnd
