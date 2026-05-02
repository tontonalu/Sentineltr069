// Package dashboard renderiza a página inicial autenticada (rota "/").
// Agrupa contadores de devices, alertas, jobs, batches e healthchecks em uma
// única view. Cada widget é gateado por permissão pelo handler.
package dashboard

import (
	"time"

	alerting "github.com/celinet/sentinel-acs/internal/domain/alerting"
	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
)

// DashboardData é o pacote de dados consumido por dashboard.Page. Cada bloco
// pode ser zero-valued se o usuário não tem permissão para vê-lo (o template
// checa Permissions antes de renderizar).
type DashboardData struct {
	UserName     string
	Permissions  PermissionFlags
	DeviceCounts DeviceCounts
	AlertCounts  AlertCounts
	JobCounts    JobCounts
	BatchCounts  BatchCounts
	RecentAlerts []alerting.Alert
	RecentJobs   []prov.Job
	Health       []HealthRow
	GeneratedAt  time.Time
}

// PermissionFlags decide quais widgets renderizar. Calculado pelo handler a
// partir de mw.UserHasPermission(ctx, ...).
type PermissionFlags struct {
	CanReadDevices bool
	CanReadAlerts  bool
	CanReadJobs    bool
	CanAckAlerts   bool
}

// AnyVisible — true se ao menos um widget pode aparecer; senão renderizamos
// um EmptyState explicando que o usuário não tem permissões.
func (p PermissionFlags) AnyVisible() bool {
	return p.CanReadDevices || p.CanReadAlerts || p.CanReadJobs
}

type DeviceCounts struct {
	Online    int
	Offline   int
	NeverSeen int
	Unknown   int
	Total     int
}

type AlertCounts struct {
	Critical int
	Warning  int
	Info     int
	Total    int
}

type JobCounts struct {
	Queued    int
	Running   int
	Failed    int
	Done      int
	Cancelled int
	Total     int // últimas 24h
}

type BatchCounts struct {
	AwaitingApproval int
	Running          int
	Queued           int
	Total            int
}

// HealthRow representa uma linha do card "Saúde do sistema". Mirror leve do
// que healthz.go monta — repetido aqui para evitar dependência circular.
//
// Name usa rótulos genéricos ("Banco de dados", "Cache", "ACS upstream") em
// vez de nomes de produto, para reduzir fingerprinting de stack pelo usuário
// final. Decisão duplicada em handlers/dashboard.go.runHealthChecks.
type HealthRow struct {
	Name    string
	Status  string // "ok" | "error" | "skipped"
	Latency string // "12ms"
	Error   string
}
