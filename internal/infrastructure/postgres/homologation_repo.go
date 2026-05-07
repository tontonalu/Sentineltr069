// homologation_repo — adapter Postgres para sessões, mappings, catálogo de
// canonical_keys e registros de homologação por modelo.
//
// Convenções:
//   - O service de homologação ([internal/application/homologation]) decide
//     quando bumpar status e quando renderizar o profile final; o repo apenas
//     persiste o que recebe.
//   - tree_snapshot é JSONB cru (Device.Raw do GenieACS) — caller serializa.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// ────────────────────── SessionRepo ──────────────────────

type HomologationSessionRepo struct{ pool Pool }

func NewHomologationSessionRepo(pool Pool) *HomologationSessionRepo {
	return &HomologationSessionRepo{pool: pool}
}

func (r *HomologationSessionRepo) Save(ctx context.Context, s *hom.Session) error {
	if !s.Status.Valid() {
		return hom.ErrInvalidStatus
	}
	const q = `
		INSERT INTO homologation_sessions
		    (id, lab_device_id, model_id, status, created_by, tree_snapshot,
		     notes, started_at, finished_at, generated_profile_id)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5, $6,
		        COALESCE($7,''), COALESCE($8, NOW()), $9, $10)
		ON CONFLICT (id) DO UPDATE SET
		    status = EXCLUDED.status,
		    notes = EXCLUDED.notes,
		    finished_at = EXCLUDED.finished_at,
		    generated_profile_id = EXCLUDED.generated_profile_id
		RETURNING id, started_at`
	var idArg any
	if s.ID != uuid.Nil {
		idArg = s.ID
	}
	var startedAt any
	if !s.StartedAt.IsZero() {
		startedAt = s.StartedAt
	}
	err := r.pool.QueryRow(ctx, q,
		idArg, s.LabDeviceID, s.ModelID, string(s.Status), s.CreatedBy,
		s.TreeSnapshot, s.Notes, startedAt, s.FinishedAt, s.GeneratedProfileID,
	).Scan(&s.ID, &s.StartedAt)
	if err != nil && isUniqueViolation(err, "idx_hom_sessions_active_per_device") {
		return hom.ErrSessionAlreadyActive
	}
	return err
}

func (r *HomologationSessionRepo) GetByID(ctx context.Context, id uuid.UUID) (*hom.Session, error) {
	const q = `
		SELECT id, lab_device_id, model_id, status, created_by, tree_snapshot,
		       COALESCE(notes,''), started_at, finished_at, generated_profile_id
		  FROM homologation_sessions WHERE id = $1`
	var s hom.Session
	var status string
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&s.ID, &s.LabDeviceID, &s.ModelID, &status, &s.CreatedBy, &s.TreeSnapshot,
		&s.Notes, &s.StartedAt, &s.FinishedAt, &s.GeneratedProfileID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hom.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Status = hom.SessionStatus(status)
	return &s, nil
}

func (r *HomologationSessionRepo) List(ctx context.Context, f hom.SessionFilter) ([]hom.Session, error) {
	q := `
		SELECT id, lab_device_id, model_id, status, created_by, NULL::jsonb,
		       COALESCE(notes,''), started_at, finished_at, generated_profile_id
		  FROM homologation_sessions WHERE 1=1`
	args := []any{}
	if f.Status != nil {
		args = append(args, string(*f.Status))
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if f.LabDeviceID != nil {
		args = append(args, *f.LabDeviceID)
		q += fmt.Sprintf(" AND lab_device_id = $%d", len(args))
	}
	if f.ModelID != nil {
		args = append(args, *f.ModelID)
		q += fmt.Sprintf(" AND model_id = $%d", len(args))
	}
	if f.CreatedBy != nil {
		args = append(args, *f.CreatedBy)
		q += fmt.Sprintf(" AND created_by = $%d", len(args))
	}
	q += " ORDER BY started_at DESC"
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if f.Offset > 0 {
		args = append(args, f.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hom.Session
	for rows.Next() {
		var s hom.Session
		var status string
		if err := rows.Scan(&s.ID, &s.LabDeviceID, &s.ModelID, &status, &s.CreatedBy, &s.TreeSnapshot,
			&s.Notes, &s.StartedAt, &s.FinishedAt, &s.GeneratedProfileID); err != nil {
			return nil, err
		}
		s.Status = hom.SessionStatus(status)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *HomologationSessionRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status hom.SessionStatus) error {
	if !status.Valid() {
		return hom.ErrInvalidStatus
	}
	q := `UPDATE homologation_sessions SET status = $2`
	if status == hom.SessionCompleted || status == hom.SessionAbandoned {
		q += `, finished_at = COALESCE(finished_at, NOW())`
	}
	q += ` WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, string(status))
	if err != nil {
		if isUniqueViolation(err, "idx_hom_sessions_active_per_device") {
			return hom.ErrSessionAlreadyActive
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrSessionNotFound
	}
	return nil
}

func (r *HomologationSessionRepo) UpdateTreeSnapshot(ctx context.Context, id uuid.UUID, snapshot []byte) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE homologation_sessions SET tree_snapshot = $2 WHERE id = $1`,
		id, snapshot)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrSessionNotFound
	}
	return nil
}

func (r *HomologationSessionRepo) SetGeneratedProfile(ctx context.Context, id uuid.UUID, profileID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE homologation_sessions SET generated_profile_id = $2 WHERE id = $1`,
		id, profileID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrSessionNotFound
	}
	return nil
}

// PurgeOldSnapshots zera tree_snapshot de sessões finalizadas antes de `before`.
// Mantém metadados (status, datas, generated_profile_id) e mappings — auditoria
// continua intacta. Tipicamente rodado pelo worker em tick diário.
func (r *HomologationSessionRepo) PurgeOldSnapshots(ctx context.Context, before time.Time) (int, error) {
	const q = `
		UPDATE homologation_sessions
		   SET tree_snapshot = NULL
		 WHERE tree_snapshot IS NOT NULL
		   AND status IN ('completed','abandoned')
		   AND finished_at IS NOT NULL
		   AND finished_at < $1`
	tag, err := r.pool.Exec(ctx, q, before)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ResetStuckProbing recupera sessões presas em `probing` no boot do servidor.
// Cenário típico: server foi reiniciado durante um Probe, a goroutine morre,
// status fica órfão no banco. Sem essa recovery, a sessão fica indefinidamente
// em probing e o índice único parcial bloqueia novas tentativas no mesmo device.
//
// Heurística do destino: se há snapshot persistido a sessão volta para
// `testing` (operador pode continuar com a árvore disponível); senão, `draft`
// (operador clica "Sondar" pra começar de novo).
func (r *HomologationSessionRepo) ResetStuckProbing(ctx context.Context) (int, error) {
	const q = `
		UPDATE homologation_sessions
		   SET status = CASE
		       WHEN tree_snapshot IS NOT NULL THEN 'testing'
		       ELSE 'draft'
		   END
		 WHERE status = 'probing'`
	tag, err := r.pool.Exec(ctx, q)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *HomologationSessionRepo) ActiveByDevice(ctx context.Context, deviceID uuid.UUID) (*hom.Session, error) {
	const q = `
		SELECT id, lab_device_id, model_id, status, created_by, NULL::jsonb,
		       COALESCE(notes,''), started_at, finished_at, generated_profile_id
		  FROM homologation_sessions
		 WHERE lab_device_id = $1
		   AND status IN ('draft','probing','testing')
		 LIMIT 1`
	var s hom.Session
	var status string
	err := r.pool.QueryRow(ctx, q, deviceID).Scan(
		&s.ID, &s.LabDeviceID, &s.ModelID, &status, &s.CreatedBy, &s.TreeSnapshot,
		&s.Notes, &s.StartedAt, &s.FinishedAt, &s.GeneratedProfileID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.Status = hom.SessionStatus(status)
	return &s, nil
}

// ────────────────────── MappingRepo ──────────────────────

type HomologationMappingRepo struct{ pool Pool }

func NewHomologationMappingRepo(pool Pool) *HomologationMappingRepo {
	return &HomologationMappingRepo{pool: pool}
}

func (r *HomologationMappingRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]hom.Mapping, error) {
	const q = `
		SELECT id, session_id, canonical_key, tr_path, value_template, data_type,
		       is_secret, sort_order, read_status, write_status,
		       read_value, write_test_value, last_error, tested_at
		  FROM homologation_mappings
		 WHERE session_id = $1
		 ORDER BY sort_order, canonical_key`
	rows, err := r.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hom.Mapping
	for rows.Next() {
		m, err := scanMapping(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *HomologationMappingRepo) ListByProfile(ctx context.Context, profileID uuid.UUID) ([]hom.Mapping, error) {
	const q = `
		SELECT m.id, m.session_id, m.canonical_key, m.tr_path, m.value_template,
		       m.data_type, m.is_secret, m.sort_order, m.read_status, m.write_status,
		       m.read_value, m.write_test_value, m.last_error, m.tested_at
		  FROM homologation_mappings m
		  JOIN homologation_sessions s ON s.id = m.session_id
		 WHERE s.generated_profile_id = $1
		 ORDER BY m.sort_order, m.canonical_key`
	rows, err := r.pool.Query(ctx, q, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hom.Mapping
	for rows.Next() {
		m, err := scanMapping(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *HomologationMappingRepo) GetByID(ctx context.Context, id uuid.UUID) (*hom.Mapping, error) {
	const q = `
		SELECT id, session_id, canonical_key, tr_path, value_template, data_type,
		       is_secret, sort_order, read_status, write_status,
		       read_value, write_test_value, last_error, tested_at
		  FROM homologation_mappings WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	m, err := scanMappingRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hom.ErrMappingNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *HomologationMappingRepo) Create(ctx context.Context, m *hom.Mapping) error {
	if !m.DataType.Valid() {
		return tmpl.ErrInvalidDataType
	}
	if !m.ReadStatus.Valid() {
		m.ReadStatus = hom.TestPending
	}
	if !m.WriteStatus.Valid() {
		m.WriteStatus = hom.TestPending
	}
	const q = `
		INSERT INTO homologation_mappings
		    (id, session_id, canonical_key, tr_path, value_template, data_type,
		     is_secret, sort_order, read_status, write_status)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, COALESCE($5,''), $6, $7, $8, $9, $10)
		RETURNING id`
	var idArg any
	if m.ID != uuid.Nil {
		idArg = m.ID
	}
	err := r.pool.QueryRow(ctx, q,
		idArg, m.SessionID, m.CanonicalKey, m.TRPath, m.ValueTemplate,
		string(m.DataType), m.IsSecret, m.SortOrder,
		string(m.ReadStatus), string(m.WriteStatus),
	).Scan(&m.ID)
	if err != nil && isUniqueViolation(err, "") {
		return hom.ErrMappingDuplicate
	}
	return err
}

func (r *HomologationMappingRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM homologation_mappings WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrMappingNotFound
	}
	return nil
}

func (r *HomologationMappingRepo) UpdateTemplate(ctx context.Context, id uuid.UUID, valueTemplate string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE homologation_mappings SET value_template = $2 WHERE id = $1`,
		id, valueTemplate)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrMappingNotFound
	}
	return nil
}

func (r *HomologationMappingRepo) UpdateReadResult(ctx context.Context, id uuid.UUID, status hom.TestStatus, readValue, errMsg string) error {
	if !status.Valid() {
		return hom.ErrInvalidStatus
	}
	const q = `
		UPDATE homologation_mappings
		   SET read_status = $2,
		       read_value  = NULLIF($3,''),
		       last_error  = NULLIF($4,''),
		       tested_at   = NOW()
		 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, string(status), readValue, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrMappingNotFound
	}
	return nil
}

func (r *HomologationMappingRepo) UpdateWriteResult(ctx context.Context, id uuid.UUID, status hom.TestStatus, testValue, errMsg string) error {
	if !status.Valid() {
		return hom.ErrInvalidStatus
	}
	const q = `
		UPDATE homologation_mappings
		   SET write_status     = $2,
		       write_test_value = NULLIF($3,''),
		       last_error       = NULLIF($4,''),
		       tested_at        = NOW()
		 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, string(status), testValue, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrMappingNotFound
	}
	return nil
}

// ────────────────────── CanonicalKeyRepo ──────────────────────

type CanonicalKeyRepo struct{ pool Pool }

func NewCanonicalKeyRepo(pool Pool) *CanonicalKeyRepo { return &CanonicalKeyRepo{pool: pool} }

func (r *CanonicalKeyRepo) List(ctx context.Context, category string) ([]hom.CanonicalKey, error) {
	q := `
		SELECT id, key, label_pt, COALESCE(description,''), category,
		       suggested_data_type, default_is_secret,
		       hint_paths_tr098, hint_paths_tr181, created_at, updated_at
		  FROM canonical_keys`
	args := []any{}
	if category != "" {
		args = append(args, category)
		q += " WHERE category = $1"
	}
	q += " ORDER BY category, key"
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hom.CanonicalKey
	for rows.Next() {
		k, err := scanCanonicalKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *CanonicalKeyRepo) GetByKey(ctx context.Context, key string) (*hom.CanonicalKey, error) {
	const q = `
		SELECT id, key, label_pt, COALESCE(description,''), category,
		       suggested_data_type, default_is_secret,
		       hint_paths_tr098, hint_paths_tr181, created_at, updated_at
		  FROM canonical_keys WHERE key = $1`
	row := r.pool.QueryRow(ctx, q, key)
	k, err := scanCanonicalKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hom.ErrCanonicalKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *CanonicalKeyRepo) GetByID(ctx context.Context, id uuid.UUID) (*hom.CanonicalKey, error) {
	const q = `
		SELECT id, key, label_pt, COALESCE(description,''), category,
		       suggested_data_type, default_is_secret,
		       hint_paths_tr098, hint_paths_tr181, created_at, updated_at
		  FROM canonical_keys WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	k, err := scanCanonicalKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hom.ErrCanonicalKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *CanonicalKeyRepo) Create(ctx context.Context, k *hom.CanonicalKey) error {
	if !k.SuggestedDataType.Valid() {
		return tmpl.ErrInvalidDataType
	}
	const q = `
		INSERT INTO canonical_keys
		    (id, key, label_pt, description, category, suggested_data_type,
		     default_is_secret, hint_paths_tr098, hint_paths_tr181)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, COALESCE($4,''), $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`
	var idArg any
	if k.ID != uuid.Nil {
		idArg = k.ID
	}
	return r.pool.QueryRow(ctx, q,
		idArg, k.Key, k.LabelPT, k.Description, k.Category, string(k.SuggestedDataType),
		k.DefaultIsSecret, k.HintPathsTR098, k.HintPathsTR181,
	).Scan(&k.ID, &k.CreatedAt, &k.UpdatedAt)
}

func (r *CanonicalKeyRepo) Update(ctx context.Context, k *hom.CanonicalKey) error {
	if !k.SuggestedDataType.Valid() {
		return tmpl.ErrInvalidDataType
	}
	const q = `
		UPDATE canonical_keys SET
		    label_pt = $2,
		    description = COALESCE($3,''),
		    category = $4,
		    suggested_data_type = $5,
		    default_is_secret = $6,
		    hint_paths_tr098 = $7,
		    hint_paths_tr181 = $8
		 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		k.ID, k.LabelPT, k.Description, k.Category, string(k.SuggestedDataType),
		k.DefaultIsSecret, k.HintPathsTR098, k.HintPathsTR181,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrCanonicalKeyNotFound
	}
	return nil
}

func (r *CanonicalKeyRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM canonical_keys WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrCanonicalKeyNotFound
	}
	return nil
}

// ────────────────────── ModelHomologationRepo ──────────────────────

type ModelHomologationRepo struct{ pool Pool }

func NewModelHomologationRepo(pool Pool) *ModelHomologationRepo {
	return &ModelHomologationRepo{pool: pool}
}

func (r *ModelHomologationRepo) Create(ctx context.Context, h *hom.ModelHomologation) error {
	if !h.Status.Valid() {
		h.Status = hom.StatusHomologated
	}
	const q = `
		INSERT INTO model_homologations
		    (id, model_id, profile_id, session_id, homologated_by, status)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5, $6)
		RETURNING id, homologated_at`
	var idArg any
	if h.ID != uuid.Nil {
		idArg = h.ID
	}
	return r.pool.QueryRow(ctx, q,
		idArg, h.ModelID, h.ProfileID, h.SessionID, h.HomologatedBy, string(h.Status),
	).Scan(&h.ID, &h.HomologatedAt)
}

func (r *ModelHomologationRepo) IsHomologated(ctx context.Context, modelID, profileID uuid.UUID) (bool, error) {
	const q = `
		SELECT 1 FROM model_homologations
		 WHERE model_id = $1 AND profile_id = $2 AND status = 'homologated'
		 LIMIT 1`
	var n int
	err := r.pool.QueryRow(ctx, q, modelID, profileID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *ModelHomologationRepo) ListByModel(ctx context.Context, modelID uuid.UUID) ([]hom.ModelHomologation, error) {
	return r.listBy(ctx, "model_id", modelID)
}

func (r *ModelHomologationRepo) ListByProfile(ctx context.Context, profileID uuid.UUID) ([]hom.ModelHomologation, error) {
	return r.listBy(ctx, "profile_id", profileID)
}

func (r *ModelHomologationRepo) listBy(ctx context.Context, col string, val uuid.UUID) ([]hom.ModelHomologation, error) {
	q := fmt.Sprintf(`
		SELECT id, model_id, profile_id, session_id, homologated_by,
		       homologated_at, status, deprecated_at, COALESCE(deprecated_reason,'')
		  FROM model_homologations
		 WHERE %s = $1
		 ORDER BY homologated_at DESC`, col)
	rows, err := r.pool.Query(ctx, q, val)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hom.ModelHomologation
	for rows.Next() {
		var h hom.ModelHomologation
		var status string
		if err := rows.Scan(&h.ID, &h.ModelID, &h.ProfileID, &h.SessionID, &h.HomologatedBy,
			&h.HomologatedAt, &status, &h.DeprecatedAt, &h.DeprecatedReason); err != nil {
			return nil, err
		}
		h.Status = hom.HomologationStatus(status)
		out = append(out, h)
	}
	return out, rows.Err()
}

// FindActiveByModel — pega a última homologação ATIVA para um modelo.
// Como o índice único parcial idx_model_hom_active garante apenas 1 par
// (model, profile) ativo, em tese deveria haver no máximo 1 — mas se o
// operador homologar 2 profiles diferentes para o mesmo modelo (ex.: básico
// vs. avançado), pegamos a mais recente.
func (r *ModelHomologationRepo) FindActiveByModel(ctx context.Context, modelID uuid.UUID) (*hom.ModelHomologation, error) {
	const q = `
		SELECT id, model_id, profile_id, session_id, homologated_by,
		       homologated_at, status, deprecated_at, COALESCE(deprecated_reason,'')
		  FROM model_homologations
		 WHERE model_id = $1 AND status = 'homologated'
		 ORDER BY homologated_at DESC
		 LIMIT 1`
	var h hom.ModelHomologation
	var status string
	err := r.pool.QueryRow(ctx, q, modelID).Scan(
		&h.ID, &h.ModelID, &h.ProfileID, &h.SessionID, &h.HomologatedBy,
		&h.HomologatedAt, &status, &h.DeprecatedAt, &h.DeprecatedReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hom.ErrModelHomologationNotFound
	}
	if err != nil {
		return nil, err
	}
	h.Status = hom.HomologationStatus(status)
	return &h, nil
}

func (r *ModelHomologationRepo) Deprecate(ctx context.Context, id uuid.UUID, reason string) error {
	const q = `
		UPDATE model_homologations
		   SET status = 'deprecated', deprecated_at = NOW(), deprecated_reason = NULLIF($2,'')
		 WHERE id = $1 AND status = 'homologated'`
	tag, err := r.pool.Exec(ctx, q, id, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return hom.ErrModelHomologationNotFound
	}
	return nil
}

// ────────────────────── helpers de scan ──────────────────────
// Reutilizamos rowScanner declarado em alerting_repo.go — interface comum
// entre pgx.Row (single) e pgx.Rows (cursor).

func scanMapping(s rowScanner) (hom.Mapping, error) {
	var m hom.Mapping
	var dt, rs, ws string
	err := s.Scan(&m.ID, &m.SessionID, &m.CanonicalKey, &m.TRPath, &m.ValueTemplate,
		&dt, &m.IsSecret, &m.SortOrder, &rs, &ws,
		&m.ReadValue, &m.WriteTestValue, &m.LastError, &m.TestedAt)
	if err != nil {
		return m, err
	}
	m.DataType = tmpl.DataType(dt)
	m.ReadStatus = hom.TestStatus(rs)
	m.WriteStatus = hom.TestStatus(ws)
	return m, nil
}

func scanMappingRow(row pgx.Row) (hom.Mapping, error) { return scanMapping(row) }

func scanCanonicalKey(s rowScanner) (hom.CanonicalKey, error) {
	var k hom.CanonicalKey
	var dt string
	err := s.Scan(&k.ID, &k.Key, &k.LabelPT, &k.Description, &k.Category,
		&dt, &k.DefaultIsSecret, &k.HintPathsTR098, &k.HintPathsTR181,
		&k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return k, err
	}
	k.SuggestedDataType = tmpl.DataType(dt)
	return k, nil
}

func scanCanonicalKeyRow(row pgx.Row) (hom.CanonicalKey, error) { return scanCanonicalKey(row) }
