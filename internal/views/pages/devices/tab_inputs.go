// tab_inputs — structs de entrada das abas da página /devices/{id}.
//
// Mantemos os tipos em arquivo .go (não .templ) para que possam ser
// compartilhados livremente entre handlers e os templs irmãos.
package devices

import (
	"github.com/google/uuid"

	devapp "github.com/celinet/sentinel-acs/internal/application/devices"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

// TabInput — payload da aba "Dispositivo" (e default).
type TabInput struct {
	Device    domain.Device
	View      *devapp.DeviceProfileView
	CSRFToken string
	CanEdit   bool
}

// CategoryInput — abas que listam fields de uma categoria do profile.
type CategoryInput struct {
	Device    domain.Device
	View      *devapp.DeviceProfileView
	CSRFToken string
	CanEdit   bool
	Category  string // "wan" | "wifi" | "lan" | "voice" | ...
	Title     string // "Internet" | "Wireless" | ...
}

// HostsInput — aba de Dispositivos Conectados (LAN hosts).
type HostsInput struct {
	Device domain.Device
	Hosts  []tele.HostSample
}

// PortsInput — aba Status das Portas.
type PortsInput struct {
	Device domain.Device
	Ports  []tele.PortSample
}

// StatsInput — aba Estatísticas (mini-charts SVG).
// Reutiliza SeriesPoint já definida em history.templ para alimentar o
// helper dualLineSVG sem cópia.
type StatsInput struct {
	Device       domain.Device
	Range        string // "24h" | "7d" | "30d"
	WifiSeries   []SeriesPoint
	WifiSeries5G []SeriesPoint
	WanRxSeries  []SeriesPoint
	WanTxSeries  []SeriesPoint
	CPUSeries    []SeriesPoint
	MemSeries    []SeriesPoint
}

// DiagInput — aba Diagnósticos (placeholders por enquanto).
type DiagInput struct {
	Device domain.Device
}

// FieldRowInput — fragmento que representa 1 linha de configuração
// editável. Usado tanto na renderização inicial quanto no swap após save.
type FieldRowInput struct {
	DeviceID    uuid.UUID
	Field       devapp.FieldView
	CSRFToken   string
	CanEdit     bool
	EnqueuedJob uuid.UUID // não-zero quando vem do POST UpdateField
}

// FieldErrorInput — fragmento exibido quando UpdateField falha.
type FieldErrorInput struct {
	CanonicalKey string
	Message      string
}
