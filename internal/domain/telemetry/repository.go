package telemetry

import (
	"context"

	"github.com/google/uuid"
)

// Repository — acesso às hypertables. Inserts são em batch por design:
// um único device pode emitir N samples (várias redes Wi-Fi, p.ex.) e o
// collector empilha tudo de um chunk antes de gravar.
type Repository interface {
	InsertWifi(ctx context.Context, samples []WifiSample) error
	InsertWan(ctx context.Context, samples []WanSample) error
	InsertSystem(ctx context.Context, samples []SystemSample) error

	// Queries para o front-end. Para janelas <=24h usamos a tabela bruta;
	// para 7d/30d usamos as continuous aggregates `_hourly`.
	QueryWifiRaw(ctx context.Context, deviceID uuid.UUID, r Range) ([]WifiSample, error)
	QueryWifiHourly(ctx context.Context, deviceID uuid.UUID, r Range) ([]HourlyWifiPoint, error)

	QueryWanRaw(ctx context.Context, deviceID uuid.UUID, r Range) ([]WanSample, error)
	QueryWanHourly(ctx context.Context, deviceID uuid.UUID, r Range) ([]HourlyWanPoint, error)

	QuerySystemRaw(ctx context.Context, deviceID uuid.UUID, r Range) ([]SystemSample, error)
	QuerySystemHourly(ctx context.Context, deviceID uuid.UUID, r Range) ([]HourlySystemPoint, error)

	// Hosts (dispositivos conectados na LAN do CPE).
	InsertHosts(ctx context.Context, samples []HostSample) error
	// LatestHostsByDevice devolve, para o device, o último ponto de cada
	// (mac_address) dentro da janela `since` — janela típica: 15 min para
	// representar "online agora", 24h para "vistos hoje".
	LatestHostsByDevice(ctx context.Context, deviceID uuid.UUID, since Range) ([]HostSample, error)

	// Ports (status físico das portas Ethernet/WAN).
	InsertPorts(ctx context.Context, samples []PortSample) error
	LatestPortsByDevice(ctx context.Context, deviceID uuid.UUID, since Range) ([]PortSample, error)
}
