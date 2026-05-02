// provisioning_repo — Postgres adapters de jobs e batches.
//
// Worker pega jobs com SELECT … FOR UPDATE SKIP LOCKED — concorrência segura
// com múltiplos workers no futuro sem hot-spotting.
package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
)

// ────────────────────── JobRepo ──────────────────────

type JobRepo struct{ pool Pool }

func NewJobRepo(pool Pool) *JobRepo { return &JobRepo{pool: pool} }

func (r *JobRepo) Create(ctx context.Context, j *prov.Job) error {
	const q = `
		INSERT INTO provisioning_jobs
		    (id, device_id, profile_id, requested_by, batch_id, status, payload, scheduled_at)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5, $6, $7, COALESCE($8, NOW()))
		RETURNING id, created_at`
	var idArg any
	if j.ID != uuid.Nil {
		idArg = j.ID
	}
	if j.Status == "" {
		j.Status = prov.JobQueued
	}
	var sched any
	if !j.ScheduledAt.IsZero() {
		sched = j.ScheduledAt
	}
	return r.pool.QueryRow(ctx, q,
		idArg, j.DeviceID, j.ProfileID, j.RequestedBy, j.BatchID,
		string(j.Status), j.Payload, sched,
	).Scan(&j.ID, &j.CreatedAt)
}

func (r *JobRepo) GetByID(ctx context.Context, id uuid.UUID) (*prov.Job, error) {
	const q = `
		SELECT id, device_id, profile_id, requested_by, batch_id, status,
		       payload, COALESCE(result, 'null'::jsonb), COALESCE(genieacs_task_id,''),
		       COALESCE(error_message,''), retry_count,
		       scheduled_at, started_at, finished_at, created_at
		  FROM provisioning_jobs WHERE id = $1`
	var j prov.Job
	var st string
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&j.ID, &j.DeviceID, &j.ProfileID, &j.RequestedBy, &j.BatchID, &st,
		&j.Payload, &j.Result, &j.GenieACSTaskID, &j.ErrorMessage, &j.RetryCount,
		&j.ScheduledAt, &j.StartedAt, &j.FinishedAt, &j.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, prov.ErrJobNotFound
	}
	j.Status = prov.JobStatus(st)
	return &j, err
}

// ClaimBatch pega até `limit` jobs queued cujo scheduled_at <= now,
// marca como running e retorna pro worker. Usa SKIP LOCKED para concorrência.
func (r *JobRepo) ClaimBatch(ctx context.Context, limit int) ([]prov.Job, error) {
	if limit <= 0 {
		limit = 10
	}
	const q = `
		WITH picked AS (
		    SELECT id FROM provisioning_jobs
		     WHERE status = 'queued' AND scheduled_at <= NOW()
		     ORDER BY scheduled_at
		     LIMIT $1
		     FOR UPDATE SKIP LOCKED
		)
		UPDATE provisioning_jobs j
		   SET status = 'running', started_at = NOW()
		  FROM picked
		 WHERE j.id = picked.id
		RETURNING j.id, j.device_id, j.profile_id, j.requested_by, j.batch_id,
		          j.status, j.payload, j.retry_count, j.scheduled_at,
		          j.started_at, j.created_at`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prov.Job
	for rows.Next() {
		var j prov.Job
		var st string
		if err := rows.Scan(&j.ID, &j.DeviceID, &j.ProfileID, &j.RequestedBy, &j.BatchID,
			&st, &j.Payload, &j.RetryCount, &j.ScheduledAt, &j.StartedAt, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.Status = prov.JobStatus(st)
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkDone — sucesso. taskID = id da task GenieACS criada.
func (r *JobRepo) MarkDone(ctx context.Context, id uuid.UUID, taskID string, result []byte) error {
	const q = `
		UPDATE provisioning_jobs
		   SET status = 'done',
		       genieacs_task_id = NULLIF($2,''),
		       result = $3,
		       finished_at = NOW()
		 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id, taskID, result)
	return err
}

// MarkFailed — erro durante exec. retry true → volta pra queued com retry_count+1.
func (r *JobRepo) MarkFailed(ctx context.Context, id uuid.UUID, msg string, retry bool) error {
	if retry {
		_, err := r.pool.Exec(ctx,
			`UPDATE provisioning_jobs
			    SET status = 'queued',
			        error_message = $2,
			        retry_count = retry_count + 1,
			        scheduled_at = NOW() + (INTERVAL '30 seconds' * (retry_count + 1)),
			        started_at = NULL
			  WHERE id = $1`,
			id, msg,
		)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE provisioning_jobs
		    SET status = 'failed', error_message = $2, finished_at = NOW()
		  WHERE id = $1`,
		id, msg,
	)
	return err
}

// Cancel — só faz sentido em queued.
func (r *JobRepo) Cancel(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE provisioning_jobs SET status='cancelled', finished_at=NOW() WHERE id=$1 AND status='queued'`,
		id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return prov.ErrJobNotFound
	}
	return nil
}

// JobListFilter — UI /provisioning/jobs.
type JobListFilter struct {
	Status   string
	BatchID  *uuid.UUID
	DeviceID *uuid.UUID
	Since    *time.Time
	Limit    int
}

func (r *JobRepo) List(ctx context.Context, f JobListFilter) ([]prov.Job, error) {
	q := `
		SELECT id, device_id, profile_id, requested_by, batch_id, status,
		       payload, COALESCE(result,'null'::jsonb), COALESCE(genieacs_task_id,''),
		       COALESCE(error_message,''), retry_count,
		       scheduled_at, started_at, finished_at, created_at
		  FROM provisioning_jobs WHERE 1=1`
	args := []any{}
	idx := func() int { return len(args) }
	if f.Status != "" {
		args = append(args, f.Status)
		q += " AND status = $" + itoa(idx())
	}
	if f.BatchID != nil {
		args = append(args, *f.BatchID)
		q += " AND batch_id = $" + itoa(idx())
	}
	if f.DeviceID != nil {
		args = append(args, *f.DeviceID)
		q += " AND device_id = $" + itoa(idx())
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		q += " AND created_at >= $" + itoa(idx())
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	q += " ORDER BY created_at DESC LIMIT $" + itoa(idx())

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prov.Job
	for rows.Next() {
		var j prov.Job
		var st string
		if err := rows.Scan(&j.ID, &j.DeviceID, &j.ProfileID, &j.RequestedBy, &j.BatchID,
			&st, &j.Payload, &j.Result, &j.GenieACSTaskID, &j.ErrorMessage, &j.RetryCount,
			&j.ScheduledAt, &j.StartedAt, &j.FinishedAt, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.Status = prov.JobStatus(st)
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountByStateSince retorna {state -> count} de jobs criados após `since`.
// Usado pelo dashboard para "Jobs nas últimas 24h".
func (r *JobRepo) CountByStateSince(ctx context.Context, since time.Time) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT status, COUNT(*) FROM provisioning_jobs WHERE created_at >= $1 GROUP BY status`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int, 5)
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ────────────────────── BatchRepo ──────────────────────

type BatchRepo struct{ pool Pool }

func NewBatchRepo(pool Pool) *BatchRepo { return &BatchRepo{pool: pool} }

func (r *BatchRepo) Create(ctx context.Context, b *prov.Batch) error {
	const q = `
		INSERT INTO provisioning_batches
		    (id, profile_id, profile_version, requested_by, filter_summary, filter_payload,
		     total_devices, queued, status)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $7, $8)
		RETURNING id, created_at`
	var idArg any
	if b.ID != uuid.Nil {
		idArg = b.ID
	}
	if b.Status == "" {
		b.Status = prov.BatchQueued
	}
	return r.pool.QueryRow(ctx, q,
		idArg, b.ProfileID, b.ProfileVersion, b.RequestedBy,
		b.FilterSummary, b.FilterPayload, b.TotalDevices, string(b.Status),
	).Scan(&b.ID, &b.CreatedAt)
}

func (r *BatchRepo) GetByID(ctx context.Context, id uuid.UUID) (*prov.Batch, error) {
	const q = `
		SELECT id, profile_id, profile_version, requested_by, filter_summary, filter_payload,
		       total_devices, queued, done, failed, cancelled, status,
		       approved_by, approved_at, created_at, finished_at
		  FROM provisioning_batches WHERE id = $1`
	var b prov.Batch
	var st string
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&b.ID, &b.ProfileID, &b.ProfileVersion, &b.RequestedBy,
		&b.FilterSummary, &b.FilterPayload,
		&b.TotalDevices, &b.Queued, &b.Done, &b.Failed, &b.Cancelled, &st,
		&b.ApprovedBy, &b.ApprovedAt, &b.CreatedAt, &b.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, prov.ErrBatchNotFound
	}
	b.Status = prov.BatchStatus(st)
	return &b, err
}

// RecountFromJobs atualiza contadores agregados a partir das linhas de jobs —
// fonte de verdade. Worker chama após cada job terminal.
func (r *BatchRepo) RecountFromJobs(ctx context.Context, batchID uuid.UUID) error {
	const q = `
		UPDATE provisioning_batches b SET
		    queued    = c.queued,
		    done      = c.done,
		    failed    = c.failed,
		    cancelled = c.cancelled,
		    status = CASE
		        WHEN c.queued = 0 AND c.failed = 0 AND c.cancelled = 0 AND c.done > 0 THEN 'done'
		        WHEN c.queued = 0 AND c.done = 0 AND c.failed > 0                      THEN 'failed'
		        WHEN c.queued = 0 AND c.cancelled = c.total                            THEN 'cancelled'
		        WHEN c.queued = 0                                                       THEN 'done'
		        ELSE 'running'
		    END,
		    finished_at = CASE WHEN c.queued = 0 THEN NOW() ELSE NULL END
		  FROM (
		    SELECT
		        COUNT(*) FILTER (WHERE status = 'queued')    AS queued,
		        COUNT(*) FILTER (WHERE status = 'done')      AS done,
		        COUNT(*) FILTER (WHERE status = 'failed')    AS failed,
		        COUNT(*) FILTER (WHERE status = 'cancelled') AS cancelled,
		        COUNT(*)                                     AS total
		      FROM provisioning_jobs WHERE batch_id = $1
		  ) c
		 WHERE b.id = $1`
	_, err := r.pool.Exec(ctx, q, batchID)
	return err
}

func (r *BatchRepo) Approve(ctx context.Context, batchID, approver uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE provisioning_batches
		    SET status='queued', approved_by=$2, approved_at=NOW()
		  WHERE id=$1 AND status='awaiting_approval'`,
		batchID, approver,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return prov.ErrBatchClosed
	}
	return nil
}

// ApproveAndReleaseJobs também muda jobs pendentes do batch para queued
// efetivo (eles foram criados em status 'queued' mesmo, então é no-op ou
// futuro hook para agendar com gap).
func (r *BatchRepo) List(ctx context.Context, requestedBy *uuid.UUID, limit int) ([]prov.Batch, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
		SELECT id, profile_id, profile_version, requested_by, filter_summary, filter_payload,
		       total_devices, queued, done, failed, cancelled, status,
		       approved_by, approved_at, created_at, finished_at
		  FROM provisioning_batches`
	args := []any{}
	if requestedBy != nil {
		args = append(args, *requestedBy)
		q += " WHERE requested_by = $1"
	}
	args = append(args, limit)
	q += " ORDER BY created_at DESC LIMIT $" + itoa(len(args))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prov.Batch
	for rows.Next() {
		var b prov.Batch
		var st string
		if err := rows.Scan(&b.ID, &b.ProfileID, &b.ProfileVersion, &b.RequestedBy,
			&b.FilterSummary, &b.FilterPayload,
			&b.TotalDevices, &b.Queued, &b.Done, &b.Failed, &b.Cancelled, &st,
			&b.ApprovedBy, &b.ApprovedAt, &b.CreatedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		b.Status = prov.BatchStatus(st)
		out = append(out, b)
	}
	return out, rows.Err()
}

// CountByStatus retorna {status -> count} para batches. Usado pelo dashboard
// para destacar batches em "awaiting_approval" e em execução.
func (r *BatchRepo) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT status, COUNT(*) FROM provisioning_batches GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int, 6)
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ────────────────────── helpers ──────────────────────

func itoa(n int) string { return strconv.Itoa(n) }
