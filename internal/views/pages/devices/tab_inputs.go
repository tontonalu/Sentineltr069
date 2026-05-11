// tab_inputs — structs de entrada das abas da página /devices/{id}.
//
// Mantemos os tipos em arquivo .go (não .templ) para que possam ser
// compartilhados livremente entre handlers e os templs irmãos.
package devices

import (
	"time"

	"github.com/google/uuid"

	devapp "github.com/celinet/sentinel-acs/internal/application/devices"
	diagdom "github.com/celinet/sentinel-acs/internal/domain/diagnostics"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

// TabInput — payload da aba "Dispositivo" (e default).
//
// Vendor/Model/Customer/POP são lookups rasos para enriquecer a
// Identificação com nome do fabricante, plano do cliente e PPPoE login —
// dados que fazem mais sentido na aba do que campos TR-069 redundantes.
type TabInput struct {
	Device    domain.Device
	Vendor    *domain.Vendor
	Model     *domain.DeviceModel
	Customer  *domain.Customer
	POP       *domain.POP
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
	TempSeries   []SeriesPoint

	// KPIs de cabeçalho — mostram o último ponto disponível ao invés de
	// agregados, pra dar feedback imediato de "está coletando agora".
	LatestCPUPct        *float64
	LatestMemPct        *float64
	LatestTemperatureC  *float64
	LatestWifiClients   *int

	// Permissão pra renderizar o botão de refresh manual (gate na UI;
	// handler também valida).
	CanRefreshTelemetry bool
}

// RefreshResultInput — fragmento HTMX devolvido pelo POST refresh-telemetry.
// HTMX troca o conteúdo do contêiner do botão por essa mensagem.
type RefreshResultInput struct {
	DeviceID    uuid.UUID
	OK          bool
	Message     string
	NextRetryIn time.Duration // só preenchido quando OK=false por cooldown
}

// DiagInput — aba Diagnósticos. List traz histórico recente; ativos vão
// pra cima e fazem polling automático até virar terminal.
type DiagInput struct {
	Device      domain.Device
	History     []diagdom.Diagnostic
	CanDiagnose bool
}

// DiagnosticRowInput — fragmento de uma linha de diagnostic, usado tanto
// na lista inicial quanto no polling auto-refresh.
type DiagnosticRowInput struct {
	DeviceID   uuid.UUID
	Diagnostic diagdom.Diagnostic
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

// FieldBatchInput — banner do POST batch /devices/{id}/fields. Renderizado
// no topo do pane re-trocado pelo HTMX.
//
// Quando Err != nil exibe erro vermelho. Quando JobID != uuid.Nil, link
// pro job criado. Skipped lista canonical_keys ignoradas (regra "senha em
// branco preserva o valor atual") — informação útil para o operador saber
// que clicar Salvar não apagou nenhuma senha.
type FieldBatchInput struct {
	DeviceID uuid.UUID
	JobID    uuid.UUID
	Err      error
	Results  []devapp.FieldUpdateResult
}
