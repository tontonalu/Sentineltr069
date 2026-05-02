package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// JobRunnerRepo — superset do que o executor precisa do JobRepo do PG.
type JobRunnerRepo interface {
	JobRepository
	GetByID(ctx context.Context, id uuid.UUID) (*prov.Job, error)
	ClaimBatch(ctx context.Context, limit int) ([]prov.Job, error)
	MarkDone(ctx context.Context, id uuid.UUID, taskID string, result []byte) error
	MarkFailed(ctx context.Context, id uuid.UUID, msg string, retry bool) error
}

// BatchUpdater — recálculo agregado após job terminal.
type BatchUpdater interface {
	RecountFromJobs(ctx context.Context, batchID uuid.UUID) error
}

// GenieACSWriter — interface do que o executor chama no NBI.
type GenieACSWriter interface {
	SetParameterValues(ctx context.Context, deviceID string, params []genieacs.Parameter) (genieacs.TaskID, error)
}

// Executor consome jobs claimed do JobRepo e os executa contra o GenieACS.
//
// Política de retry:
//   - Erro de rede / timeout → retry até MaxRetries.
//   - Erro de validação (params inválidos) → fail imediato.
//   - Após terminal, RecountFromJobs no batch (se houver).
type Executor struct {
	jobs     JobRunnerRepo
	batches  BatchUpdater
	devices  DeviceResolver
	genie    GenieACSWriter
	maxRetry int
}

// DeviceResolver — mantém pequena para o executor (apenas genieacs_id).
type DeviceResolver interface {
	ResolveGenieACSID(ctx context.Context, internalID uuid.UUID) (string, error)
}

func NewExecutor(jobs JobRunnerRepo, batches BatchUpdater, devices DeviceResolver, genie GenieACSWriter) *Executor {
	return &Executor{jobs: jobs, batches: batches, devices: devices, genie: genie, maxRetry: 3}
}

// SetMaxRetries ajusta o limite. Default 3 — mais que isso e o problema é
// estrutural (CPE offline há horas, etc.).
func (e *Executor) SetMaxRetries(n int) {
	if n >= 0 {
		e.maxRetry = n
	}
}

// RunOnce reclama até `limit` jobs e os executa. Retorna quantidade processada.
// Worker chama em loop com ticker + push via stream.
func (e *Executor) RunOnce(ctx context.Context, limit int) (int, error) {
	claimed, err := e.jobs.ClaimBatch(ctx, limit)
	if err != nil {
		return 0, err
	}
	for i := range claimed {
		job := &claimed[i]
		if err := e.execute(ctx, job); err != nil {
			retry := job.RetryCount+1 <= e.maxRetry && isRetryable(err)
			_ = e.jobs.MarkFailed(ctx, job.ID, err.Error(), retry)
		}
		if job.BatchID != nil {
			_ = e.batches.RecountFromJobs(ctx, *job.BatchID)
		}
	}
	return len(claimed), nil
}

// RunByID executa um job específico (caminho hot via stream notify).
// Idempotente: se o job já não está em queued/running, no-op.
func (e *Executor) RunByID(ctx context.Context, id uuid.UUID) error {
	job, err := e.jobs.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if job.Status.IsTerminal() {
		return nil
	}
	if err := e.execute(ctx, job); err != nil {
		retry := job.RetryCount+1 <= e.maxRetry && isRetryable(err)
		_ = e.jobs.MarkFailed(ctx, job.ID, err.Error(), retry)
		if job.BatchID != nil {
			_ = e.batches.RecountFromJobs(ctx, *job.BatchID)
		}
		return err
	}
	if job.BatchID != nil {
		_ = e.batches.RecountFromJobs(ctx, *job.BatchID)
	}
	return nil
}

// ──────────────── internal ────────────────

func (e *Executor) execute(ctx context.Context, job *prov.Job) error {
	envelope, err := DecodePayload(job.Payload)
	if err != nil {
		return fmt.Errorf("payload corrompido: %w", err)
	}
	gid, err := e.devices.ResolveGenieACSID(ctx, job.DeviceID)
	if err != nil {
		return fmt.Errorf("resolve device: %w", err)
	}
	params := make([]genieacs.Parameter, 0, len(envelope.Parameters))
	for _, p := range envelope.Parameters {
		params = append(params, genieacs.Parameter{
			Path:  p.TRPath,
			Value: p.Value,
			Type:  p.DataType.XSD(),
		})
	}
	taskID, err := e.genie.SetParameterValues(ctx, gid, params)
	if err != nil {
		return err
	}
	result, _ := json.Marshal(map[string]any{
		"task_id":     string(taskID),
		"applied_at":  time.Now().UTC().Format(time.RFC3339),
		"param_count": len(params),
	})
	return e.jobs.MarkDone(ctx, job.ID, string(taskID), result)
}

// isRetryable — heurística simples: API errors com status 5xx ou erros
// genéricos de rede/timeout são retentáveis. 4xx é problema de payload.
func isRetryable(err error) bool {
	apiErr, ok := err.(*genieacs.APIError)
	if !ok {
		return true // network/timeout/etc.
	}
	return apiErr.Status >= 500
}
