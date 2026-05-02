// Package alerting implementa o engine de avaliação de regras.
//
// Tick é chamado periodicamente pelo worker (1 min). Para cada regra ativa:
//  1. Carrega métricas conforme metric+filter+window.
//  2. Aplica aggregation.
//  3. Compara com threshold via operator.
//  4. Se TRUE e cooldown expirado e não há alerta ativo idêntico → cria alerta + notifica.
//
// Notification dispatch é synchronous dentro do Tick — para 100s de regras
// é trivial; quando virar bottleneck, mover para goroutines com fan-out.
package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// Notifier — entrega de mensagem por um canal específico. Adapters em
// internal/infrastructure/notifier/ implementam.
type Notifier interface {
	Type() domain.ChannelType
	Send(ctx context.Context, target, subject, body string) error
}

// MetricSource — provê os valores agregados que o engine compara.
// Cada Metric pode ser servida por uma fonte diferente (PG inventory,
// PG telemetry, ...). Implementação default em sources.go combina tudo.
type MetricSource interface {
	Evaluate(ctx context.Context, c domain.Condition, now time.Time) (*MetricResult, error)
}

// MetricResult — saída da agregação.
type MetricResult struct {
	Value     float64           // resultado da agregação (count, count_pct, avg, ...)
	DeviceID  *uuid.UUID        // populado em type=single ou quando count == 1
	Sample    map[string]any    // contexto adicional para payload do alerta
}

// Engine orquestra avaliação. Repos são injetados via DI; faz testes fáceis.
type Engine struct {
	rules         domain.RuleRepository
	alerts        domain.AlertRepository
	notifications domain.NotificationRepository
	source        MetricSource
	notifiers     map[domain.ChannelType]Notifier
}

func NewEngine(
	rules domain.RuleRepository,
	alerts domain.AlertRepository,
	notifications domain.NotificationRepository,
	source MetricSource,
	notifiers ...Notifier,
) *Engine {
	m := make(map[domain.ChannelType]Notifier, len(notifiers))
	for _, n := range notifiers {
		if n == nil {
			continue
		}
		m[n.Type()] = n
	}
	return &Engine{
		rules: rules, alerts: alerts, notifications: notifications,
		source: source, notifiers: m,
	}
}

// TickResult — métricas de uma rodada.
type TickResult struct {
	RulesEvaluated  int
	AlertsFired     int
	AlertsResolved  int
	NotificationsOK int
	Errors          int
	Duration        time.Duration
}

// Tick avalia todas as regras ativas. Erros parciais por regra não abortam
// o loop — o engine não pode parar globalmente porque uma regra está mal
// configurada.
func (e *Engine) Tick(ctx context.Context) (*TickResult, error) {
	start := time.Now()
	log := logger.FromContext(ctx)

	rules, err := e.rules.List(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("alerting: list rules: %w", err)
	}

	res := &TickResult{}
	now := start.UTC()

	for i := range rules {
		rl := &rules[i]
		res.RulesEvaluated++
		if err := e.evaluateRule(ctx, rl, now, res); err != nil {
			res.Errors++
			log.Warn("alerting: rule eval", "rule", rl.Name, "err", err)
		}
	}
	res.Duration = time.Since(start)
	return res, nil
}

func (e *Engine) evaluateRule(ctx context.Context, rl *domain.Rule, now time.Time, res *TickResult) error {
	if err := rl.Condition.Validate(); err != nil {
		return fmt.Errorf("regra %q inválida: %w", rl.Name, err)
	}
	mr, err := e.source.Evaluate(ctx, rl.Condition, now)
	if err != nil {
		return fmt.Errorf("metric source: %w", err)
	}
	if mr == nil {
		return nil // sem dados suficientes — não é erro, só não dispara
	}
	triggered := rl.Condition.Operator.Eval(mr.Value, rl.Condition.Threshold)

	if !triggered {
		// Auto-resolve: se a condição saiu do estado de alerta e existe
		// alerta aberto, resolve automaticamente.
		if active, _ := e.alerts.HasActiveForRuleDevice(ctx, rl.ID, mr.DeviceID); active {
			if err := e.alerts.ResolveByRuleAndDevice(ctx, rl.ID, mr.DeviceID); err == nil {
				res.AlertsResolved++
			}
		}
		return nil
	}

	// Cooldown: pulo se a última firing está dentro da janela.
	if rl.CooldownMinutes > 0 {
		last, err := e.alerts.LastFiredForRule(ctx, rl.ID)
		if err == nil && !last.IsZero() {
			if now.Sub(last) < time.Duration(rl.CooldownMinutes)*time.Minute {
				return nil
			}
		}
	}

	// Idempotência: já tem alerta ativo da mesma (rule, device) → não duplica.
	if active, _ := e.alerts.HasActiveForRuleDevice(ctx, rl.ID, mr.DeviceID); active {
		return nil
	}

	payload := buildPayload(rl, mr)
	a := &domain.Alert{
		RuleID:   rl.ID,
		DeviceID: mr.DeviceID,
		Severity: rl.Severity,
		FiredAt:  now,
		Payload:  payload,
	}
	if err := e.alerts.Create(ctx, a); err != nil {
		return fmt.Errorf("create alert: %w", err)
	}
	res.AlertsFired++

	// Dispatch — fail-soft por canal.
	for _, ch := range rl.Channels {
		ok := e.deliver(ctx, a, rl, mr, ch)
		if ok {
			res.NotificationsOK++
		}
	}
	return nil
}

// deliver tenta enviar a mensagem. Sempre append em notifications, status
// reflete o resultado. Retorna true se status=sent.
func (e *Engine) deliver(
	ctx context.Context, a *domain.Alert, rl *domain.Rule, mr *MetricResult, ch domain.Channel,
) bool {
	notifier, ok := e.notifiers[ch.Type]
	if !ok {
		_ = e.notifications.Append(ctx, &domain.Notification{
			AlertID:       a.ID,
			ChannelType:   ch.Type,
			ChannelTarget: ch.Target,
			Status:        domain.NotificationDropped,
			ErrorMessage:  "canal não configurado neste worker",
		})
		return false
	}
	subject, body := renderMessage(rl, a, mr)
	dCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := notifier.Send(dCtx, ch.Target, subject, body)
	n := &domain.Notification{
		AlertID:       a.ID,
		ChannelType:   ch.Type,
		ChannelTarget: ch.Target,
	}
	if err != nil {
		n.Status = domain.NotificationFailed
		n.ErrorMessage = err.Error()
		if errors.Is(err, domain.ErrChannelDisabled) {
			n.Status = domain.NotificationDropped
		}
	} else {
		n.Status = domain.NotificationSent
	}
	_ = e.notifications.Append(ctx, n)
	return n.Status == domain.NotificationSent
}

// renderMessage produz subject + body legíveis. Mantemos simples — sem
// engine de templates aqui (já temos um na Fase 3, mas é overkill pra alertas).
func renderMessage(rl *domain.Rule, a *domain.Alert, mr *MetricResult) (string, string) {
	subject := fmt.Sprintf("[%s] %s", a.Severity, rl.Name)
	body := fmt.Sprintf(
		"Alerta disparado: %s\n"+
			"Severidade: %s\n"+
			"Regra: %s\n"+
			"Métrica: %s = %.2f (limite: %s %.2f)\n"+
			"Janela: %s\n"+
			"Quando: %s",
		rl.Description, a.Severity, rl.Name,
		rl.Condition.Metric, mr.Value, rl.Condition.Operator, rl.Condition.Threshold,
		rl.Condition.Window,
		a.FiredAt.UTC().Format(time.RFC3339),
	)
	if mr.DeviceID != nil {
		body += "\nDevice: " + mr.DeviceID.String()
	}
	return subject, body
}

func buildPayload(rl *domain.Rule, mr *MetricResult) json.RawMessage {
	p := map[string]any{
		"value":     mr.Value,
		"threshold": rl.Condition.Threshold,
		"metric":    string(rl.Condition.Metric),
		"operator":  string(rl.Condition.Operator),
		"window":    rl.Condition.Window,
	}
	if mr.DeviceID != nil {
		p["device_id"] = mr.DeviceID.String()
	}
	for k, v := range mr.Sample {
		p[k] = v
	}
	raw, _ := json.Marshal(p)
	return raw
}
