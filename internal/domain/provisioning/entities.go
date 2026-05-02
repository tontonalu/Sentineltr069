// Package provisioning modela jobs e batches de aplicação de profiles.
//
// Um Job aplica 1 profile a 1 device. Um Batch agrupa jobs criados por uma
// operação em massa, com filtros + contagens agregadas para acompanhamento.
package provisioning

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// JobStatus — máquina de estados do job individual.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobDone      JobStatus = "done"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// IsTerminal — done|failed|cancelled. Worker não retoma jobs em estado terminal.
func (s JobStatus) IsTerminal() bool {
	return s == JobDone || s == JobFailed || s == JobCancelled
}

// BatchStatus — máquina de estados do lote.
// awaiting_approval bloqueia a fila até alguém com permissão aprovar.
type BatchStatus string

const (
	BatchQueued            BatchStatus = "queued"
	BatchRunning           BatchStatus = "running"
	BatchDone              BatchStatus = "done"
	BatchFailed            BatchStatus = "failed"
	BatchCancelled         BatchStatus = "cancelled"
	BatchAwaitingApproval  BatchStatus = "awaiting_approval"
)

// Job — unidade atômica de provisionamento. Payload guarda os params já
// renderizados (ResolvedParameter serializado), garantindo idempotência se
// o profile mudar antes do job rodar.
type Job struct {
	ID             uuid.UUID
	DeviceID       uuid.UUID
	ProfileID      *uuid.UUID
	RequestedBy    *uuid.UUID
	BatchID        *uuid.UUID
	Status         JobStatus
	Payload        json.RawMessage
	Result         json.RawMessage
	GenieACSTaskID string
	ErrorMessage   string
	RetryCount     int
	ScheduledAt    time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time
}

// Batch — operação em massa. FilterPayload guarda o critério (vendor, modelo,
// pop, tags, customer plan etc.) para reaplicação ou auditoria.
type Batch struct {
	ID             uuid.UUID
	ProfileID      uuid.UUID
	ProfileVersion int
	RequestedBy    uuid.UUID
	FilterSummary  string
	FilterPayload  json.RawMessage
	TotalDevices   int
	Queued         int
	Done           int
	Failed         int
	Cancelled      int
	Status         BatchStatus
	ApprovedBy     *uuid.UUID
	ApprovedAt     *time.Time
	CreatedAt      time.Time
	FinishedAt     *time.Time
}

// FilterPayload — espelho estruturado do que o usuário escolheu na UI
// antes de "aplicar em massa". Persistimos no batch para reaplicação.
type FilterPayload struct {
	POPIDs       []uuid.UUID `json:"pop_ids,omitempty"`
	VendorIDs    []uuid.UUID `json:"vendor_ids,omitempty"`
	ModelIDs     []uuid.UUID `json:"model_ids,omitempty"`
	CustomerPlan string      `json:"customer_plan,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Status       string      `json:"status,omitempty"`
}

// ApprovalThreshold — acima desse número, batch entra em awaiting_approval.
// Definido aqui porque é regra de negócio, não config infra.
const ApprovalThreshold = 1000

// MaxParallelJobs — limite default de jobs simultâneos por worker.
const MaxParallelJobs = 100
