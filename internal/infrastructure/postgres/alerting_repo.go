// alerting_repo — Postgres adapters de regras, alertas e notificações.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
)

// ──────────────── RuleRepo ────────────────

type RuleRepo struct{ pool Pool }

func NewRuleRepo(pool Pool) *RuleRepo { return &RuleRepo{pool: pool} }

func (r *RuleRepo) Create(ctx context.Context, rl *domain.Rule) error {
	condJSON, err := domain.MarshalCondition(rl.Condition)
	if err != nil {
		return err
	}
	chanJSON, err := json.Marshal(rl.Channels)
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO alert_rules
		    (id, name, description, condition, severity, channels, is_active, cooldown_minutes, created_by)
		VALUES (COALESCE($1, gen_random_uuid()), $2, NULLIF($3,''), $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`
	var idArg any
	if rl.ID != uuid.Nil {
		idArg = rl.ID
	}
	err = r.pool.QueryRow(ctx, q,
		idArg, rl.Name, rl.Description, condJSON,
		string(rl.Severity), chanJSON, rl.IsActive, rl.CooldownMinutes, rl.CreatedBy,
	).Scan(&rl.ID, &rl.CreatedAt, &rl.UpdatedAt)
	if err != nil && isUniqueViolation(err, "") {
		return domain.ErrInvalidRule
	}
	rl.ConditionRaw = condJSON
	return err
}

func (r *RuleRepo) Update(ctx context.Context, rl *domain.Rule) error {
	condJSON, err := domain.MarshalCondition(rl.Condition)
	if err != nil {
		return err
	}
	chanJSON, err := json.Marshal(rl.Channels)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE alert_rules SET
		    name = $2, description = NULLIF($3,''), condition = $4,
		    severity = $5, channels = $6, is_active = $7, cooldown_minutes = $8
		WHERE id = $1`,
		rl.ID, rl.Name, rl.Description, condJSON,
		string(rl.Severity), chanJSON, rl.IsActive, rl.CooldownMinutes,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRuleNotFound
	}
	rl.ConditionRaw = condJSON
	return nil
}

func (r *RuleRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Rule, error) {
	const q = `
		SELECT id, name, COALESCE(description,''), condition, severity, channels,
		       is_active, cooldown_minutes, created_by, created_at, updated_at
		  FROM alert_rules WHERE id = $1`
	rl, err := r.scanRule(ctx, q, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrRuleNotFound
	}
	return rl, err
}

func (r *RuleRepo) List(ctx context.Context, activeOnly bool) ([]domain.Rule, error) {
	q := `
		SELECT id, name, COALESCE(description,''), condition, severity, channels,
		       is_active, cooldown_minutes, created_by, created_at, updated_at
		  FROM alert_rules`
	if activeOnly {
		q += " WHERE is_active = TRUE"
	}
	q += " ORDER BY name"

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Rule
	for rows.Next() {
		rl, err := scanRuleRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rl)
	}
	return out, rows.Err()
}

func (r *RuleRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE alert_rules SET is_active=$2 WHERE id=$1`, id, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRuleNotFound
	}
	return nil
}

func (r *RuleRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM alert_rules WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRuleNotFound
	}
	return nil
}

func (r *RuleRepo) scanRule(ctx context.Context, q string, args ...any) (*domain.Rule, error) {
	row := r.pool.QueryRow(ctx, q, args...)
	return scanRuleRow(row)
}

// rowScanner — abstraí pgx.Row e pgx.Rows para reutilizar em GetByID/List.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRuleRow(row rowScanner) (*domain.Rule, error) {
	var (
		rl       domain.Rule
		condRaw  []byte
		chanRaw  []byte
		severity string
	)
	err := row.Scan(
		&rl.ID, &rl.Name, &rl.Description, &condRaw, &severity, &chanRaw,
		&rl.IsActive, &rl.CooldownMinutes, &rl.CreatedBy, &rl.CreatedAt, &rl.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	rl.Severity = domain.Severity(severity)
	rl.ConditionRaw = condRaw
	if cond, err := domain.UnmarshalCondition(condRaw); err == nil {
		rl.Condition = cond
	}
	if len(chanRaw) > 0 {
		_ = json.Unmarshal(chanRaw, &rl.Channels)
	}
	return &rl, nil
}

// ──────────────── AlertRepo ────────────────

type AlertRepo struct{ pool Pool }

func NewAlertRepo(pool Pool) *AlertRepo { return &AlertRepo{pool: pool} }

func (r *AlertRepo) Create(ctx context.Context, a *domain.Alert) error {
	if len(a.Payload) == 0 {
		a.Payload = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO alerts (id, rule_id, device_id, severity, fired_at, payload)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, COALESCE($5, NOW()), $6)
		RETURNING id, fired_at`
	var idArg any
	if a.ID != uuid.Nil {
		idArg = a.ID
	}
	var firedArg any
	if !a.FiredAt.IsZero() {
		firedArg = a.FiredAt
	}
	return r.pool.QueryRow(ctx, q,
		idArg, a.RuleID, a.DeviceID, string(a.Severity), firedArg, a.Payload,
	).Scan(&a.ID, &a.FiredAt)
}

func (r *AlertRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Alert, error) {
	const q = `
		SELECT id, rule_id, device_id, severity, fired_at, resolved_at,
		       acknowledged_at, acknowledged_by, COALESCE(payload, '{}'::jsonb)
		  FROM alerts WHERE id = $1`
	var a domain.Alert
	var sev string
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&a.ID, &a.RuleID, &a.DeviceID, &sev, &a.FiredAt, &a.ResolvedAt,
		&a.AcknowledgedAt, &a.AcknowledgedBy, &a.Payload,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAlertNotFound
	}
	a.Severity = domain.Severity(sev)
	return &a, err
}

func (r *AlertRepo) ListActive(ctx context.Context, limit int) ([]domain.Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, rule_id, device_id, severity, fired_at, resolved_at,
		       acknowledged_at, acknowledged_by, COALESCE(payload, '{}'::jsonb)
		  FROM alerts
		 WHERE resolved_at IS NULL
		 ORDER BY fired_at DESC
		 LIMIT $1`
	return r.scanAlerts(ctx, q, limit)
}

func (r *AlertRepo) ListByRule(ctx context.Context, ruleID uuid.UUID, limit int) ([]domain.Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	const q = `
		SELECT id, rule_id, device_id, severity, fired_at, resolved_at,
		       acknowledged_at, acknowledged_by, COALESCE(payload, '{}'::jsonb)
		  FROM alerts WHERE rule_id = $1
		 ORDER BY fired_at DESC LIMIT $2`
	return r.scanAlerts(ctx, q, ruleID, limit)
}

func (r *AlertRepo) Acknowledge(ctx context.Context, alertID, by uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE alerts SET acknowledged_at = NOW(), acknowledged_by = $2
		  WHERE id = $1 AND acknowledged_at IS NULL`,
		alertID, by,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAlertNotFound
	}
	return nil
}

func (r *AlertRepo) Resolve(ctx context.Context, alertID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE alerts SET resolved_at = NOW() WHERE id = $1 AND resolved_at IS NULL`,
		alertID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAlertNotFound
	}
	return nil
}

func (r *AlertRepo) ResolveByRuleAndDevice(ctx context.Context, ruleID uuid.UUID, deviceID *uuid.UUID) error {
	q := `UPDATE alerts SET resolved_at = NOW() WHERE rule_id = $1 AND resolved_at IS NULL`
	args := []any{ruleID}
	if deviceID != nil {
		q += " AND device_id = $2"
		args = append(args, *deviceID)
	} else {
		q += " AND device_id IS NULL"
	}
	_, err := r.pool.Exec(ctx, q, args...)
	return err
}

func (r *AlertRepo) LastFiredForRule(ctx context.Context, ruleID uuid.UUID) (time.Time, error) {
	var t *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT MAX(fired_at) FROM alerts WHERE rule_id = $1`,
		ruleID,
	).Scan(&t)
	if err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

func (r *AlertRepo) HasActiveForRuleDevice(ctx context.Context, ruleID uuid.UUID, deviceID *uuid.UUID) (bool, error) {
	q := `SELECT 1 FROM alerts WHERE rule_id = $1 AND resolved_at IS NULL`
	args := []any{ruleID}
	if deviceID != nil {
		q += " AND device_id = $2"
		args = append(args, *deviceID)
	} else {
		q += " AND device_id IS NULL"
	}
	q += " LIMIT 1"
	var x int
	err := r.pool.QueryRow(ctx, q, args...).Scan(&x)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *AlertRepo) scanAlerts(ctx context.Context, q string, args ...any) ([]domain.Alert, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Alert
	for rows.Next() {
		var a domain.Alert
		var sev string
		if err := rows.Scan(&a.ID, &a.RuleID, &a.DeviceID, &sev, &a.FiredAt,
			&a.ResolvedAt, &a.AcknowledgedAt, &a.AcknowledgedBy, &a.Payload); err != nil {
			return nil, err
		}
		a.Severity = domain.Severity(sev)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ──────────────── NotificationRepo ────────────────

type NotificationRepo struct{ pool Pool }

func NewNotificationRepo(pool Pool) *NotificationRepo { return &NotificationRepo{pool: pool} }

func (r *NotificationRepo) Append(ctx context.Context, n *domain.Notification) error {
	const q = `
		INSERT INTO notifications (alert_id, channel_type, channel_target, status, error_message)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		ON CONFLICT (alert_id, channel_type, channel_target) DO UPDATE
		   SET status = EXCLUDED.status, error_message = EXCLUDED.error_message, sent_at = NOW()
		RETURNING id, sent_at`
	return r.pool.QueryRow(ctx, q,
		n.AlertID, string(n.ChannelType), n.ChannelTarget, string(n.Status), n.ErrorMessage,
	).Scan(&n.ID, &n.SentAt)
}

func (r *NotificationRepo) ListByAlert(ctx context.Context, alertID uuid.UUID) ([]domain.Notification, error) {
	const q = `
		SELECT id, alert_id, channel_type, channel_target, status,
		       COALESCE(error_message,''), sent_at
		  FROM notifications WHERE alert_id = $1 ORDER BY sent_at`
	rows, err := r.pool.Query(ctx, q, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		var ct, st string
		if err := rows.Scan(&n.ID, &n.AlertID, &ct, &n.ChannelTarget, &st, &n.ErrorMessage, &n.SentAt); err != nil {
			return nil, err
		}
		n.ChannelType = domain.ChannelType(ct)
		n.Status = domain.NotificationStatus(st)
		out = append(out, n)
	}
	return out, rows.Err()
}
