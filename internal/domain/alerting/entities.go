// Package alerting modela regras + incidentes + canais de notificação.
//
// O coração da fase 5 é a DSL JSONB persistida em alert_rules.condition —
// validada via Validate() antes do save. Ver dsl.go.
package alerting

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Severity — escala de severidade de um alerta.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func (s Severity) Valid() bool {
	return s == SeverityInfo || s == SeverityWarning || s == SeverityCritical
}

// ChannelType — destinos suportados. Nem todos podem estar configurados;
// o adapter retorna ErrChannelDisabled e o engine pula.
type ChannelType string

const (
	ChannelWhatsApp ChannelType = "whatsapp"
	ChannelTelegram ChannelType = "telegram"
	ChannelSMTP     ChannelType = "smtp"
)

func (c ChannelType) Valid() bool {
	return c == ChannelWhatsApp || c == ChannelTelegram || c == ChannelSMTP
}

// Channel — destino concreto. Para SMTP, Target é "user@dominio.com";
// para WhatsApp, "+5579999990000"; para Telegram, chat_id ("-100123...").
type Channel struct {
	Type   ChannelType `json:"type"`
	Target string      `json:"target"`
}

// Rule — regra de avaliação. Condition é a DSL parseada (ver dsl.go).
type Rule struct {
	ID              uuid.UUID
	Name            string
	Description     string
	Condition       Condition
	ConditionRaw    json.RawMessage // espelho do JSON persistido
	Severity        Severity
	Channels        []Channel
	IsActive        bool
	CooldownMinutes int
	CreatedBy       *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Alert — incidente já disparado. payload guarda contexto (qual device,
// qual valor extrapolou, etc.) — útil pra UI e templates de mensagem.
type Alert struct {
	ID             uuid.UUID
	RuleID         uuid.UUID
	DeviceID       *uuid.UUID
	Severity       Severity
	FiredAt        time.Time
	ResolvedAt     *time.Time
	AcknowledgedAt *time.Time
	AcknowledgedBy *uuid.UUID
	Payload        json.RawMessage
}

// IsActive — alerta ainda aberto (não resolvido).
func (a Alert) IsActive() bool { return a.ResolvedAt == nil }

// IsAcknowledged — operador já reconheceu (mas pode estar não resolvido).
func (a Alert) IsAcknowledged() bool { return a.AcknowledgedAt != nil }

// Notification — registro append-only de cada envio.
type Notification struct {
	ID            int64
	AlertID       uuid.UUID
	ChannelType   ChannelType
	ChannelTarget string
	Status        NotificationStatus
	ErrorMessage  string
	SentAt        time.Time
}

type NotificationStatus string

const (
	NotificationSent    NotificationStatus = "sent"
	NotificationFailed  NotificationStatus = "failed"
	NotificationDropped NotificationStatus = "dropped" // canal desabilitado, target inválido, etc.
)
