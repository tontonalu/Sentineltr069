// diagnostics_repo — adapter Postgres da fila de diagnostics.
//
// As colunas request/result são JSONB pra acomodar shape diferente por tipo
// (ping vs traceroute) sem migrations. O repo só serializa/deserializa via
// encoding/json na borda — domínio recebe map[string]any.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	diag "github.com/celinet/sentinel-acs/internal/domain/diagnostics"
)

type DiagnosticsRepo struct{ pool Pool }

func NewDiagnosticsRepo(pool Pool) *DiagnosticsRepo { return &DiagnosticsRepo{pool: pool} }

func (r *DiagnosticsRepo) Create(ctx context.Context, d *diag.Diagnostic) error {
	reqJSON, err := json.Marshal(d.Request)
	if err != nil {
		return fmt.Errorf("diagnostics: encode request: %w", err)
	}
	const q = `
		INSERT INTO diagnostics
		    (id, device_id, type, status, request, requested_by, requested_at, deadline)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5, $6, COALESCE($7, NOW()), $8)
		RETURNING id, requested_at`
	var idArg any
	if d.ID != uuid.Nil {
		idArg = d.ID
	}
	var reqAtArg any
	if !d.RequestedAt.IsZero() {
		reqAtArg = d.RequestedAt
	}
	if d.Status == "" {
		d.Status = diag.StatusRequested
	}
	return r.pool.QueryRow(ctx, q,
		idArg, d.DeviceID, string(d.Type), string(d.Status),
		reqJSON, d.RequestedBy, reqAtArg, d.Deadline,
	).Scan(&d.ID, &d.RequestedAt)
}

func (r *DiagnosticsRepo) GetByID(ctx context.Context, id uuid.UUID) (*diag.Diagnostic, error) {
	const q = `
		SELECT id, device_id, type, status, request, result, COALESCE(error,''),
		       requested_by, requested_at, completed_at, deadline
		  FROM diagnostics WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	d, err := scanDiagnostic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, diag.ErrNotFound
	}
	return d, err
}

// UpdateStatus marca status sem mexer no result. error pode ser vazio quando
// só estamos transicionando requested → running.
func (r *DiagnosticsRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status diag.Status, errMsg string) error {
	var completedArg any
	if status.Terminal() {
		completedArg = time.Now().UTC()
	}
	const q = `
		UPDATE diagnostics
		   SET status = $2,
		       error = NULLIF($3,''),
		       completed_at = COALESCE($4, completed_at)
		 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, string(status), errMsg, completedArg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return diag.ErrNotFound
	}
	return nil
}

// UpdateResult escreve o payload final + status terminal. Sempre marca
// completed_at — quando isso é chamado o ciclo acabou.
func (r *DiagnosticsRepo) UpdateResult(ctx context.Context, id uuid.UUID, status diag.Status, result map[string]any) error {
	resJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("diagnostics: encode result: %w", err)
	}
	const q = `
		UPDATE diagnostics
		   SET status = $2,
		       result = $3,
		       completed_at = NOW(),
		       error = NULL
		 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, string(status), resJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return diag.ErrNotFound
	}
	return nil
}

func (r *DiagnosticsRepo) ListByDevice(ctx context.Context, deviceID uuid.UUID, limit int) ([]diag.Diagnostic, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	const q = `
		SELECT id, device_id, type, status, request, result, COALESCE(error,''),
		       requested_by, requested_at, completed_at, deadline
		  FROM diagnostics
		 WHERE device_id = $1
		 ORDER BY requested_at DESC
		 LIMIT $2`
	rows, err := r.pool.Query(ctx, q, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []diag.Diagnostic
	for rows.Next() {
		d, err := scanDiagnostic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListActive devolve diagnostics em status requested ou running. Usado pelo
// poller do worker. Limit defensivo: tick não deve processar todo o backlog
// se acumular.
func (r *DiagnosticsRepo) ListActive(ctx context.Context, limit int) ([]diag.Diagnostic, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT id, device_id, type, status, request, result, COALESCE(error,''),
		       requested_by, requested_at, completed_at, deadline
		  FROM diagnostics
		 WHERE status IN ('requested', 'running')
		 ORDER BY requested_at
		 LIMIT $1`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []diag.Diagnostic
	for rows.Next() {
		d, err := scanDiagnostic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ──────────── helpers ────────────

func scanDiagnostic(s rowScanner) (*diag.Diagnostic, error) {
	var d diag.Diagnostic
	var typeStr, statusStr string
	var reqJSON, resJSON []byte
	if err := s.Scan(&d.ID, &d.DeviceID, &typeStr, &statusStr,
		&reqJSON, &resJSON, &d.Error,
		&d.RequestedBy, &d.RequestedAt, &d.CompletedAt, &d.Deadline); err != nil {
		return nil, err
	}
	d.Type = diag.Type(typeStr)
	d.Status = diag.Status(statusStr)
	if len(reqJSON) > 0 {
		_ = json.Unmarshal(reqJSON, &d.Request)
	}
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &d.Result)
	}
	return &d, nil
}
