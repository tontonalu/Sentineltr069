// templates_repo — adapter Postgres para profiles, parâmetros e history.
//
// Convenções:
//   - Versionamento incremental é responsabilidade do service, não do repo.
//     Repo apenas persiste o que é entregue.
//   - SaveParameters substitui o conjunto inteiro (DELETE + INSERT em tx) —
//     simplifica a UI: editou tabela, salvou.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// ────────────────────── ProfileRepo ──────────────────────

type ProfileRepo struct{ pool Pool }

func NewProfileRepo(pool Pool) *ProfileRepo { return &ProfileRepo{pool: pool} }

func (r *ProfileRepo) Create(ctx context.Context, p *tmpl.Profile) error {
	const q = `
		INSERT INTO config_profiles
		    (id, name, description, vendor_id, model_id, version, is_active, is_homologated, created_by)
		VALUES (COALESCE($1, gen_random_uuid()), $2, NULLIF($3,''), $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`
	var idArg any
	if p.ID != uuid.Nil {
		idArg = p.ID
	}
	if p.Version == 0 {
		p.Version = 1
	}
	err := r.pool.QueryRow(ctx, q,
		idArg, p.Name, p.Description, p.VendorID, p.ModelID,
		p.Version, p.IsActive, p.IsHomologated, p.CreatedBy,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil && isUniqueViolation(err, "") {
		return tmpl.ErrProfileDuplicate
	}
	return err
}

func (r *ProfileRepo) Update(ctx context.Context, p *tmpl.Profile) error {
	const q = `
		UPDATE config_profiles SET
		    name = $2,
		    description = NULLIF($3,''),
		    vendor_id   = $4,
		    model_id    = $5,
		    version     = $6,
		    is_active   = $7
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		p.ID, p.Name, p.Description, p.VendorID, p.ModelID, p.Version, p.IsActive,
	)
	if err != nil {
		if isUniqueViolation(err, "") {
			return tmpl.ErrProfileDuplicate
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return tmpl.ErrProfileNotFound
	}
	return nil
}

func (r *ProfileRepo) GetByID(ctx context.Context, id uuid.UUID) (*tmpl.Profile, error) {
	const q = `
		SELECT id, name, COALESCE(description,''), vendor_id, model_id,
		       version, is_active, is_homologated, created_by, created_at, updated_at
		  FROM config_profiles WHERE id = $1`
	var p tmpl.Profile
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.VendorID, &p.ModelID,
		&p.Version, &p.IsActive, &p.IsHomologated, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tmpl.ErrProfileNotFound
	}
	return &p, err
}

// ListFilter — critérios de listagem na UI /templates.
// Vendor/Model nil = qualquer; ActiveOnly true = oculta is_active=false.
type ProfileListFilter struct {
	VendorID   *uuid.UUID
	ModelID    *uuid.UUID
	ActiveOnly bool
	Search     string
}

func (r *ProfileRepo) List(ctx context.Context, f ProfileListFilter) ([]tmpl.Profile, error) {
	q := `
		SELECT id, name, COALESCE(description,''), vendor_id, model_id,
		       version, is_active, is_homologated, created_by, created_at, updated_at
		  FROM config_profiles WHERE 1=1`
	args := []any{}
	if f.VendorID != nil {
		args = append(args, *f.VendorID)
		q += fmt.Sprintf(" AND vendor_id = $%d", len(args))
	}
	if f.ModelID != nil {
		args = append(args, *f.ModelID)
		q += fmt.Sprintf(" AND model_id = $%d", len(args))
	}
	if f.ActiveOnly {
		q += " AND is_active = TRUE"
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		q += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}
	q += " ORDER BY name"

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tmpl.Profile
	for rows.Next() {
		var p tmpl.Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.VendorID, &p.ModelID,
			&p.Version, &p.IsActive, &p.IsHomologated, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListByModel filtra profiles por model_id. Wrapper sobre List com filtro
// fixo — interface do service de templates expõe esta forma simples para
// não vazar o ProfileListFilter (que tem outras dimensões).
func (r *ProfileRepo) ListByModel(ctx context.Context, modelID uuid.UUID) ([]tmpl.Profile, error) {
	return r.List(ctx, ProfileListFilter{ModelID: &modelID})
}

func (r *ProfileRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE config_profiles SET is_active=$2 WHERE id=$1`, id, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tmpl.ErrProfileNotFound
	}
	return nil
}

// IncrementVersion bumps version + retorna o novo número. Usado pelo service
// quando detecta mudanças nos parâmetros.
func (r *ProfileRepo) IncrementVersion(ctx context.Context, id uuid.UUID) (int, error) {
	var v int
	err := r.pool.QueryRow(ctx,
		`UPDATE config_profiles SET version = version + 1 WHERE id=$1 RETURNING version`,
		id,
	).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, tmpl.ErrProfileNotFound
	}
	return v, err
}

// ────────────────────── ParameterRepo ──────────────────────

type ParameterRepo struct{ pool Pool }

func NewParameterRepo(pool Pool) *ParameterRepo { return &ParameterRepo{pool: pool} }

func (r *ParameterRepo) ListByProfile(ctx context.Context, profileID uuid.UUID) ([]tmpl.Parameter, error) {
	const q = `
		SELECT id, profile_id, canonical_key, tr_path, value_template,
		       data_type, is_secret, sort_order
		  FROM profile_parameters
		 WHERE profile_id = $1
		 ORDER BY sort_order, canonical_key`
	rows, err := r.pool.Query(ctx, q, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tmpl.Parameter
	for rows.Next() {
		var p tmpl.Parameter
		var dt string
		if err := rows.Scan(&p.ID, &p.ProfileID, &p.CanonicalKey, &p.TRPath,
			&p.ValueTemplate, &dt, &p.IsSecret, &p.SortOrder); err != nil {
			return nil, err
		}
		p.DataType = tmpl.DataType(dt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Replace substitui o conjunto de parâmetros de um profile em uma transação.
// Se any param falha, rollback total — UI sempre vê o conjunto coerente.
func (r *ParameterRepo) Replace(ctx context.Context, profileID uuid.UUID, params []tmpl.Parameter) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM profile_parameters WHERE profile_id = $1`, profileID); err != nil {
		return err
	}
	for i := range params {
		p := &params[i]
		if !p.DataType.Valid() {
			return tmpl.ErrInvalidDataType
		}
		if p.SortOrder == 0 {
			p.SortOrder = i + 1
		}
		const q = `
			INSERT INTO profile_parameters
			    (id, profile_id, canonical_key, tr_path, value_template, data_type, is_secret, sort_order)
			VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`
		var idArg any
		if p.ID != uuid.Nil {
			idArg = p.ID
		}
		if err := tx.QueryRow(ctx, q,
			idArg, profileID, p.CanonicalKey, p.TRPath, p.ValueTemplate,
			string(p.DataType), p.IsSecret, p.SortOrder,
		).Scan(&p.ID); err != nil {
			return err
		}
		p.ProfileID = profileID
	}
	return tx.Commit(ctx)
}

// ────────────────────── HistoryRepo ──────────────────────

type ProfileHistoryRepo struct{ pool Pool }

func NewProfileHistoryRepo(pool Pool) *ProfileHistoryRepo { return &ProfileHistoryRepo{pool: pool} }

func (r *ProfileHistoryRepo) Append(ctx context.Context, e *tmpl.HistoryEntry) error {
	const q = `
		INSERT INTO config_profiles_history (profile_id, version, snapshot, changed_by, change_note)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		RETURNING id, created_at`
	return r.pool.QueryRow(ctx, q,
		e.ProfileID, e.Version, e.Snapshot, e.ChangedBy, e.ChangeNote,
	).Scan(&e.ID, &e.CreatedAt)
}

func (r *ProfileHistoryRepo) ListByProfile(ctx context.Context, profileID uuid.UUID, limit int) ([]tmpl.HistoryEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	const q = `
		SELECT id, profile_id, version, snapshot, changed_by, COALESCE(change_note,''), created_at
		  FROM config_profiles_history
		 WHERE profile_id = $1
		 ORDER BY version DESC
		 LIMIT $2`
	rows, err := r.pool.Query(ctx, q, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tmpl.HistoryEntry
	for rows.Next() {
		var e tmpl.HistoryEntry
		if err := rows.Scan(&e.ID, &e.ProfileID, &e.Version, &e.Snapshot,
			&e.ChangedBy, &e.ChangeNote, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
