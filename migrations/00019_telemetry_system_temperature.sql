-- +goose Up
-- +goose StatementBegin

-- Adiciona coluna temperature_c em telemetry_system. CPEs como V-SOL/Realtek
-- expõem a leitura em InternetGatewayDevice.DeviceInfo.TemperatureStatus.
-- TemperatureSensor.{i}.Value (graus C). Manter como NUMERIC pra capturar
-- variantes que reportam decimais (alguns vendors reportam 0.5° de resolução).
ALTER TABLE telemetry_system ADD COLUMN IF NOT EXISTS temperature_c NUMERIC(5,2);

-- +goose StatementEnd

-- O continuous aggregate precisa ser dropado e recriado para incluir a nova
-- coluna no SELECT. Os dados brutos seguem em telemetry_system; a policy
-- (start_offset=3h, schedule=30min) repopula o CAGG nas próximas execuções.
-- +goose StatementBegin
DROP MATERIALIZED VIEW IF EXISTS telemetry_system_hourly;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW telemetry_system_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time)        AS bucket,
    device_id,
    AVG(cpu_pct)                       AS avg_cpu,
    MAX(cpu_pct)                       AS max_cpu,
    AVG(mem_pct)                       AS avg_mem,
    AVG(temperature_c)                 AS avg_temperature_c,
    MAX(temperature_c)                 AS max_temperature_c,
    MAX(uptime_seconds)                AS uptime_max
FROM telemetry_system
GROUP BY bucket, device_id
WITH NO DATA;
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_continuous_aggregate_policy('telemetry_system_hourly',
    start_offset      => INTERVAL '3 hours',
    end_offset        => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes',
    if_not_exists     => TRUE);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP MATERIALIZED VIEW IF EXISTS telemetry_system_hourly;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW telemetry_system_hourly
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
SELECT add_continuous_aggregate_policy('telemetry_system_hourly',
    start_offset      => INTERVAL '3 hours',
    end_offset        => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes',
    if_not_exists     => TRUE);

ALTER TABLE telemetry_system DROP COLUMN IF EXISTS temperature_c;
-- +goose StatementEnd
