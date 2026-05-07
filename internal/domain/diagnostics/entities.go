// Package diagnostics modela os testes remotos disparáveis em CPEs via TR-069
// (IPPingDiagnostics, TraceRouteDiagnostics). O domínio é deliberadamente
// pequeno: 1 entidade `Diagnostic` carrega o ciclo de vida completo
// (requested → running → complete | error | timeout).
//
// Decisões:
//
//   - request/result são map[string]any (JSONB no Postgres). Ping e traceroute
//     têm shapes diferentes; mapas evitam migrations a cada novo tipo.
//
//   - O ciclo é POLLING-based: setParameterValues dispara o teste no CPE,
//     mas o GenieACS não nos avisa quando termina. O worker tem que ler a
//     árvore periodicamente até encontrar DiagnosticsState=Complete. Por
//     isso o `Deadline` — fail-safe pra não deixar a linha travada em
//     "running" pra sempre quando o CPE sumiu da rede.
package diagnostics

import (
	"time"

	"github.com/google/uuid"
)

// Type — categorias suportadas. Quando adicionar uma nova (ex.: NSLookup),
// atualize também o CHECK em migrations/00020_diagnostics.sql.
type Type string

const (
	TypePing       Type = "ping"
	TypeTraceroute Type = "traceroute"
)

// Status do diagnostic. Estados terminais: complete, error, timeout.
type Status string

const (
	StatusRequested Status = "requested" // linha criada, ainda não enviada ao CPE
	StatusRunning   Status = "running"   // setParameterValues OK, aguardando resultado
	StatusComplete  Status = "complete"  // CPE devolveu DiagnosticsState=Complete
	StatusError     Status = "error"     // GenieACS rejeitou ou CPE devolveu Error_*
	StatusTimeout   Status = "timeout"   // poller passou de Deadline sem resposta
)

// Terminal devolve true se o estado já não muda mais.
func (s Status) Terminal() bool {
	switch s {
	case StatusComplete, StatusError, StatusTimeout:
		return true
	}
	return false
}

// Diagnostic — registro completo de uma execução. Mapeia 1-pra-1 com a
// linha em `diagnostics`. Result é populado quando Status=Complete; caso
// contrário fica nil e Error pode trazer mensagem do CPE/GenieACS.
type Diagnostic struct {
	ID          uuid.UUID
	DeviceID    uuid.UUID
	Type        Type
	Status      Status
	Request     map[string]any
	Result      map[string]any
	Error       string
	RequestedBy *uuid.UUID
	RequestedAt time.Time
	CompletedAt *time.Time
	Deadline    time.Time
}

// PingRequest extrai os campos canônicos de Diagnostic.Request quando
// Type=ping. Os defaults aqui (3 repetições, 64 bytes, 5s) cobrem o caso
// de usuário deixar o form com valores em branco — espelha o comportamento
// típico de "ping host" em terminal.
type PingRequest struct {
	Host         string
	Count        int // NumberOfRepetitions
	SizeBytes    int // DataBlockSize
	TimeoutMS    int // Timeout
	Interface    string // Interface — opcional
}

// TracerouteRequest — espelho do PingRequest para TR-069 TraceRouteDiagnostics.
type TracerouteRequest struct {
	Host       string
	MaxHops    int
	SizeBytes  int
	TimeoutMS  int // por hop
	Interface  string
}

// PingResult — campos do resultado padrão IPPingDiagnostics.
//
//	SuccessCount         IPPingDiagnostics.SuccessCount
//	FailureCount         IPPingDiagnostics.FailureCount
//	AverageResponseTime  IPPingDiagnostics.AverageResponseTime
//	MinimumResponseTime  IPPingDiagnostics.MinimumResponseTime
//	MaximumResponseTime  IPPingDiagnostics.MaximumResponseTime
type PingResult struct {
	SuccessCount    int
	FailureCount    int
	AvgResponseMS   int
	MinResponseMS   int
	MaxResponseMS   int
}

// TracerouteHop — uma linha do output. Hop começa em 1.
type TracerouteHop struct {
	Hop      int
	Host     string
	HostName string
	RTTMs    []int
}

// TracerouteResult — payload completo. ResponseTime é a média do hop final
// quando RouteHopsNumberOfEntries < MaxHops e o destino respondeu.
type TracerouteResult struct {
	ResponseTime int
	Hops         []TracerouteHop
}
