// Package telemetry contém as entidades canônicas das séries temporais.
//
// Os 3 domínios são separados intencionalmente — schemas diferentes,
// retenções diferentes, queries diferentes. Continuous aggregates `_hourly`
// existem como views materializadas para dashboards rápidos.
package telemetry

import (
	"time"

	"github.com/google/uuid"
)

// Band canônica para Wi-Fi. Strings literais batem com o CHECK do banco.
const (
	Band24G = "2.4G"
	Band5G  = "5G"
)

// WifiSample — 1 ponto de uma rede SSID.
// Campos opcionais usam pointer para distinguir "0" de "ausente".
type WifiSample struct {
	Time             time.Time
	DeviceID         uuid.UUID
	SSID             string
	Band             string
	Channel          *int
	ConnectedClients *int
	TxPower          *int
}

// WanSample — métricas do uplink. Optical_*_dbm são vendor-specific
// (precisam de virtual param) — opcionais.
type WanSample struct {
	Time         time.Time
	DeviceID     uuid.UUID
	RxBytes      *int64
	TxBytes      *int64
	OpticalRxDBM *float64
	OpticalTxDBM *float64
}

// SystemSample — CPU/memória/uptime do CPE.
type SystemSample struct {
	Time          time.Time
	DeviceID      uuid.UUID
	CPUPct        *float64
	MemPct        *float64
	UptimeSeconds *int64
}

// HasAnyMetric true se ao menos um sinal real foi coletado.
// Band sozinho não conta (é label, não métrica) — descarta samples
// "sintéticos" do parser quando o device não retornou nada útil.
func (s WifiSample) HasAnyMetric() bool {
	return s.Channel != nil || s.ConnectedClients != nil || s.TxPower != nil || s.SSID != ""
}

func (s WanSample) HasAnyMetric() bool {
	return s.RxBytes != nil || s.TxBytes != nil ||
		s.OpticalRxDBM != nil || s.OpticalTxDBM != nil
}

func (s SystemSample) HasAnyMetric() bool {
	return s.CPUPct != nil || s.MemPct != nil || s.UptimeSeconds != nil
}

// Range — intervalo para queries de série temporal.
type Range struct {
	From time.Time
	To   time.Time
}

// Last24h, Last7d, Last30d — atalhos para o front-end.
func Last24h(now time.Time) Range { return Range{From: now.Add(-24 * time.Hour), To: now} }
func Last7d(now time.Time) Range  { return Range{From: now.Add(-7 * 24 * time.Hour), To: now} }
func Last30d(now time.Time) Range { return Range{From: now.Add(-30 * 24 * time.Hour), To: now} }

// HourlyWifiPoint — linha de telemetry_wifi_hourly. Usado nos gráficos.
type HourlyWifiPoint struct {
	Bucket     time.Time
	Band       string
	AvgClients int
	MaxClients int
	AvgTxPower int
}

type HourlyWanPoint struct {
	Bucket   time.Time
	RxDelta  int64
	TxDelta  int64
	AvgRxDBM *float64
	AvgTxDBM *float64
}

type HourlySystemPoint struct {
	Bucket    time.Time
	AvgCPU    *float64
	MaxCPU    *float64
	AvgMem    *float64
	UptimeMax *int64
}

// HostSample — 1 dispositivo conectado à LAN do CPE em um tick.
// Origem: InternetGatewayDevice.LANDevice.1.Hosts.Host.{i} (TR-098) ou
// Device.Hosts.Host.{i} (TR-181). MACAddress é a chave natural.
type HostSample struct {
	Time            time.Time
	DeviceID        uuid.UUID
	MACAddress      string
	Hostname        string
	IPAddress       string
	AddressSource   string // "DHCP" | "Static" | ""
	Layer1Interface string // "Ethernet" | "WiFi-2.4G" | "WiFi-5G" | ""
	ActiveSeconds   *int64 // LeaseTimeRemaining quando aplicável
	SignalDBM       *int   // só faz sentido para WiFi
}

// HasAnyMetric — MAC é o mínimo. Sem MAC não dá pra dedupar.
func (h HostSample) HasAnyMetric() bool { return h.MACAddress != "" }

// PortSample — status físico de uma porta Ethernet/WAN do CPE.
type PortSample struct {
	Time      time.Time
	DeviceID  uuid.UUID
	PortName  string // "WAN" | "LAN1" | "LAN2" | ...
	Status    string // "Up" | "Down"
	SpeedMbps *int   // 10 / 100 / 1000 / ...
	Duplex    string // "Full" | "Half" | ""
}

// HasAnyMetric — port_name + status válido obrigatórios pelo CHECK do banco.
func (p PortSample) HasAnyMetric() bool {
	return p.PortName != "" && (p.Status == "Up" || p.Status == "Down")
}
