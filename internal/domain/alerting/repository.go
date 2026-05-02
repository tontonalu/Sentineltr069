package alerting

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RuleRepository — CRUD de regras. List filtra por ativo/inativo.
type RuleRepository interface {
	Create(ctx context.Context, r *Rule) error
	Update(ctx context.Context, r *Rule) error
	GetByID(ctx context.Context, id uuid.UUID) (*Rule, error)
	List(ctx context.Context, activeOnly bool) ([]Rule, error)
	SetActive(ctx context.Context, id uuid.UUID, active bool) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// AlertRepository — incidentes. Engine cria, UI ack/resolve.
type AlertRepository interface {
	Create(ctx context.Context, a *Alert) error
	GetByID(ctx context.Context, id uuid.UUID) (*Alert, error)

	// ListActive — alertas em aberto (resolved_at IS NULL), ordem fired_at desc.
	ListActive(ctx context.Context, limit int) ([]Alert, error)
	ListByRule(ctx context.Context, ruleID uuid.UUID, limit int) ([]Alert, error)

	Acknowledge(ctx context.Context, alertID, by uuid.UUID) error
	Resolve(ctx context.Context, alertID uuid.UUID) error
	ResolveByRuleAndDevice(ctx context.Context, ruleID uuid.UUID, deviceID *uuid.UUID) error

	// LastFiredForRule — usado pelo engine para checar cooldown.
	// Devolve o fired_at da última instância da regra (qualquer device);
	// retorna zero-time se nunca disparou.
	LastFiredForRule(ctx context.Context, ruleID uuid.UUID) (time.Time, error)

	// HasActiveForRuleDevice — true se já existe alerta aberto da mesma
	// (rule, device). Engine evita criar duplicado enquanto não resolvido.
	HasActiveForRuleDevice(ctx context.Context, ruleID uuid.UUID, deviceID *uuid.UUID) (bool, error)
}

// NotificationRepository — append-only de envios.
type NotificationRepository interface {
	Append(ctx context.Context, n *Notification) error
	ListByAlert(ctx context.Context, alertID uuid.UUID) ([]Notification, error)
}
