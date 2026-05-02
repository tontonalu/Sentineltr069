// telemetry_repo — adapter Postgres/TimescaleDB das séries temporais.
//
// Convenções:
//   - Inserts usam pgx.CopyFrom para volumes grandes (collector grava 200+ samples
//     por iteração).
//   - Queries de janela curta (<=24h) batem na hypertable bruta.
//   - Queries de 7d/30d batem nas materialized views `_hourly`.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

type TelemetryRepo struct{ pool Pool }

func NewTelemetryRepo(pool Pool) *TelemetryRepo { return &TelemetryRepo{pool: pool} }

// ──────────── Inserts ────────────

func (r *TelemetryRepo) InsertWifi(ctx context.Context, samples []tele.WifiSample) error {
	if len(samples) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, []any{
			s.Time, s.DeviceID, nullIfEmpty(s.SSID), nullIfEmpty(s.Band),
			intPtr(s.Channel), intPtr(s.ConnectedClients), intPtr(s.TxPower),
		})
	}
	_, err := r.pool.CopyFrom(ctx,
		pgx.Identifier{"telemetry_wifi"},
		[]string{"time", "device_id", "ssid", "band", "channel", "connected_clients", "tx_power"},
		pgx.CopyFromRows(rows),
	)
	return err
}

func (r *TelemetryRepo) InsertWan(ctx context.Context, samples []tele.WanSample) error {
	if len(samples) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, []any{
			s.Time, s.DeviceID,
			int64Ptr(s.RxBytes), int64Ptr(s.TxBytes),
			float64Ptr(s.OpticalRxDBM), float64Ptr(s.OpticalTxDBM),
		})
	}
	_, err := r.pool.CopyFrom(ctx,
		pgx.Identifier{"telemetry_wan"},
		[]string{"time", "device_id", "rx_bytes", "tx_bytes", "optical_rx_dbm", "optical_tx_dbm"},
		pgx.CopyFromRows(rows),
	)
	return err
}

func (r *TelemetryRepo) InsertSystem(ctx context.Context, samples []tele.SystemSample) error {
	if len(samples) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, []any{
			s.Time, s.DeviceID,
			float64Ptr(s.CPUPct), float64Ptr(s.MemPct), int64Ptr(s.UptimeSeconds),
		})
	}
	_, err := r.pool.CopyFrom(ctx,
		pgx.Identifier{"telemetry_system"},
		[]string{"time", "device_id", "cpu_pct", "mem_pct", "uptime_seconds"},
		pgx.CopyFromRows(rows),
	)
	return err
}

// ──────────── Queries raw ────────────

func (r *TelemetryRepo) QueryWifiRaw(ctx context.Context, deviceID uuid.UUID, rg tele.Range) ([]tele.WifiSample, error) {
	const q = `
		SELECT time, device_id, COALESCE(ssid,''), COALESCE(band,''),
		       channel, connected_clients, tx_power
		  FROM telemetry_wifi
		 WHERE device_id = $1 AND time BETWEEN $2 AND $3
		 ORDER BY time`
	rows, err := r.pool.Query(ctx, q, deviceID, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tele.WifiSample
	for rows.Next() {
		var s tele.WifiSample
		if err := rows.Scan(&s.Time, &s.DeviceID, &s.SSID, &s.Band,
			&s.Channel, &s.ConnectedClients, &s.TxPower); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *TelemetryRepo) QueryWanRaw(ctx context.Context, deviceID uuid.UUID, rg tele.Range) ([]tele.WanSample, error) {
	const q = `
		SELECT time, device_id, rx_bytes, tx_bytes, optical_rx_dbm, optical_tx_dbm
		  FROM telemetry_wan
		 WHERE device_id = $1 AND time BETWEEN $2 AND $3
		 ORDER BY time`
	rows, err := r.pool.Query(ctx, q, deviceID, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tele.WanSample
	for rows.Next() {
		var s tele.WanSample
		if err := rows.Scan(&s.Time, &s.DeviceID, &s.RxBytes, &s.TxBytes,
			&s.OpticalRxDBM, &s.OpticalTxDBM); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *TelemetryRepo) QuerySystemRaw(ctx context.Context, deviceID uuid.UUID, rg tele.Range) ([]tele.SystemSample, error) {
	const q = `
		SELECT time, device_id, cpu_pct, mem_pct, uptime_seconds
		  FROM telemetry_system
		 WHERE device_id = $1 AND time BETWEEN $2 AND $3
		 ORDER BY time`
	rows, err := r.pool.Query(ctx, q, deviceID, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tele.SystemSample
	for rows.Next() {
		var s tele.SystemSample
		if err := rows.Scan(&s.Time, &s.DeviceID, &s.CPUPct, &s.MemPct, &s.UptimeSeconds); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ──────────── Queries hourly (continuous aggregates) ────────────

func (r *TelemetryRepo) QueryWifiHourly(ctx context.Context, deviceID uuid.UUID, rg tele.Range) ([]tele.HourlyWifiPoint, error) {
	const q = `
		SELECT bucket, COALESCE(band,''), avg_clients, max_clients, avg_tx_power
		  FROM telemetry_wifi_hourly
		 WHERE device_id = $1 AND bucket BETWEEN $2 AND $3
		 ORDER BY bucket`
	rows, err := r.pool.Query(ctx, q, deviceID, rg.From, rg.To)
	if err != nil {
		return nil, classifyTimescaleError(err)
	}
	defer rows.Close()
	var out []tele.HourlyWifiPoint
	for rows.Next() {
		var p tele.HourlyWifiPoint
		var avgTx, maxClients *int
		if err := rows.Scan(&p.Bucket, &p.Band, &p.AvgClients, &maxClients, &avgTx); err != nil {
			return nil, err
		}
		if maxClients != nil {
			p.MaxClients = *maxClients
		}
		if avgTx != nil {
			p.AvgTxPower = *avgTx
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *TelemetryRepo) QueryWanHourly(ctx context.Context, deviceID uuid.UUID, rg tele.Range) ([]tele.HourlyWanPoint, error) {
	const q = `
		SELECT bucket, COALESCE(rx_delta,0), COALESCE(tx_delta,0), avg_rx_dbm, avg_tx_dbm
		  FROM telemetry_wan_hourly
		 WHERE device_id = $1 AND bucket BETWEEN $2 AND $3
		 ORDER BY bucket`
	rows, err := r.pool.Query(ctx, q, deviceID, rg.From, rg.To)
	if err != nil {
		return nil, classifyTimescaleError(err)
	}
	defer rows.Close()
	var out []tele.HourlyWanPoint
	for rows.Next() {
		var p tele.HourlyWanPoint
		if err := rows.Scan(&p.Bucket, &p.RxDelta, &p.TxDelta, &p.AvgRxDBM, &p.AvgTxDBM); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *TelemetryRepo) QuerySystemHourly(ctx context.Context, deviceID uuid.UUID, rg tele.Range) ([]tele.HourlySystemPoint, error) {
	const q = `
		SELECT bucket, avg_cpu, max_cpu, avg_mem, uptime_max
		  FROM telemetry_system_hourly
		 WHERE device_id = $1 AND bucket BETWEEN $2 AND $3
		 ORDER BY bucket`
	rows, err := r.pool.Query(ctx, q, deviceID, rg.From, rg.To)
	if err != nil {
		return nil, classifyTimescaleError(err)
	}
	defer rows.Close()
	var out []tele.HourlySystemPoint
	for rows.Next() {
		var p tele.HourlySystemPoint
		if err := rows.Scan(&p.Bucket, &p.AvgCPU, &p.MaxCPU, &p.AvgMem, &p.UptimeMax); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ──────────── helpers ────────────

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func intPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func int64Ptr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func float64Ptr(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ErrTimescaleMissing — disparado quando uma query bate em hypertable/CA
// que não existe (Timescale não instalado ou migração não rodou). UI pode
// tratar separadamente para mostrar mensagem útil.
var ErrTimescaleMissing = errors.New("postgres: hypertable/continuous aggregate ausente — TimescaleDB instalado?")

func classifyTimescaleError(err error) error {
	if err == nil {
		return nil
	}
	// Postgres SQLSTATE 42P01 = relation does not exist.
	// Mantemos o erro original encapsulado para facilitar log.
	if isUndefinedTable(err) {
		return fmt.Errorf("%w: %v", ErrTimescaleMissing, err)
	}
	return err
}

// isUndefinedTable detecta o code 42P01 sem importar pgconn aqui —
// reaproveita o helper existente em errors.go via type assertion.
func isUndefinedTable(err error) bool {
	type pgErrLike interface{ SQLState() string }
	var pe pgErrLike
	if errors.As(err, &pe) {
		return pe.SQLState() == "42P01"
	}
	return false
}

