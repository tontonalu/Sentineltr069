package alerting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
)

// ──────────── Fakes ────────────

type fakeRuleRepo struct{ rules []domain.Rule }

func (r *fakeRuleRepo) Create(_ context.Context, x *domain.Rule) error {
	if x.ID == uuid.Nil {
		x.ID = uuid.New()
	}
	r.rules = append(r.rules, *x)
	return nil
}
func (r *fakeRuleRepo) Update(_ context.Context, _ *domain.Rule) error             { return nil }
func (r *fakeRuleRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Rule, error) { return nil, nil }
func (r *fakeRuleRepo) List(_ context.Context, _ bool) ([]domain.Rule, error)       { return r.rules, nil }
func (r *fakeRuleRepo) SetActive(_ context.Context, _ uuid.UUID, _ bool) error      { return nil }
func (r *fakeRuleRepo) Delete(_ context.Context, _ uuid.UUID) error                 { return nil }

type fakeAlertRepo struct {
	alerts     []domain.Alert
	lastFired  map[uuid.UUID]time.Time
	hasActive  map[string]bool
	resolveFn  func(rule uuid.UUID, dev *uuid.UUID)
}

func newFakeAlertRepo() *fakeAlertRepo {
	return &fakeAlertRepo{
		lastFired: map[uuid.UUID]time.Time{},
		hasActive: map[string]bool{},
	}
}
func (r *fakeAlertRepo) Create(_ context.Context, a *domain.Alert) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	if a.FiredAt.IsZero() {
		a.FiredAt = time.Now().UTC()
	}
	r.alerts = append(r.alerts, *a)
	r.lastFired[a.RuleID] = a.FiredAt
	r.hasActive[keyRD(a.RuleID, a.DeviceID)] = true
	return nil
}
func (r *fakeAlertRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Alert, error) {
	return nil, nil
}
func (r *fakeAlertRepo) ListActive(_ context.Context, _ int) ([]domain.Alert, error) {
	return r.alerts, nil
}
func (r *fakeAlertRepo) ListByRule(_ context.Context, _ uuid.UUID, _ int) ([]domain.Alert, error) {
	return nil, nil
}
func (r *fakeAlertRepo) Acknowledge(_ context.Context, _, _ uuid.UUID) error { return nil }
func (r *fakeAlertRepo) Resolve(_ context.Context, _ uuid.UUID) error        { return nil }
func (r *fakeAlertRepo) ResolveByRuleAndDevice(_ context.Context, rule uuid.UUID, dev *uuid.UUID) error {
	delete(r.hasActive, keyRD(rule, dev))
	if r.resolveFn != nil {
		r.resolveFn(rule, dev)
	}
	return nil
}
func (r *fakeAlertRepo) LastFiredForRule(_ context.Context, ruleID uuid.UUID) (time.Time, error) {
	return r.lastFired[ruleID], nil
}
func (r *fakeAlertRepo) HasActiveForRuleDevice(_ context.Context, ruleID uuid.UUID, dev *uuid.UUID) (bool, error) {
	return r.hasActive[keyRD(ruleID, dev)], nil
}

func keyRD(rule uuid.UUID, dev *uuid.UUID) string {
	if dev == nil {
		return rule.String() + ":-"
	}
	return rule.String() + ":" + dev.String()
}

type fakeNotifRepo struct{ entries []domain.Notification }

func (r *fakeNotifRepo) Append(_ context.Context, n *domain.Notification) error {
	r.entries = append(r.entries, *n)
	return nil
}
func (r *fakeNotifRepo) ListByAlert(_ context.Context, _ uuid.UUID) ([]domain.Notification, error) {
	return r.entries, nil
}

type fakeSource struct{ value float64 }

func (s fakeSource) Evaluate(_ context.Context, _ domain.Condition, _ time.Time) (*MetricResult, error) {
	return &MetricResult{Value: s.value}, nil
}

type fakeNotifier struct {
	t      domain.ChannelType
	calls  int
	failOn string // se target == failOn → erro
}

func (n *fakeNotifier) Type() domain.ChannelType { return n.t }
func (n *fakeNotifier) Send(_ context.Context, target, _, _ string) error {
	n.calls++
	if target == n.failOn {
		return assertErr
	}
	return nil
}

var assertErr = errAssert("envio falhou")

type errAssert string

func (e errAssert) Error() string { return string(e) }

// ──────────── Tests ────────────

func sampleRule(threshold float64, cooldown int) domain.Rule {
	cond := domain.Condition{
		Type:        domain.TypeAggregate,
		Metric:      domain.MetricDeviceStatus,
		Aggregation: domain.AggCountPct,
		Operator:    domain.OpGT,
		Threshold:   threshold,
		Window:      "5m",
	}
	condRaw, _ := json.Marshal(cond)
	return domain.Rule{
		ID:              uuid.New(),
		Name:            "POP offline > 10%",
		Severity:        domain.SeverityCritical,
		Condition:       cond,
		ConditionRaw:    condRaw,
		Channels:        []domain.Channel{{Type: domain.ChannelSMTP, Target: "oncall@x.com"}},
		IsActive:        true,
		CooldownMinutes: cooldown,
	}
}

func TestEngineFireAndNotify(t *testing.T) {
	rules := &fakeRuleRepo{rules: []domain.Rule{sampleRule(10, 0)}}
	alerts := newFakeAlertRepo()
	notifs := &fakeNotifRepo{}
	src := fakeSource{value: 25} // > 10
	smtp := &fakeNotifier{t: domain.ChannelSMTP}

	e := NewEngine(rules, alerts, notifs, src, smtp)
	res, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.AlertsFired != 1 || res.NotificationsOK != 1 {
		t.Fatalf("expected 1 fire + 1 notification, got %+v", res)
	}
	if smtp.calls != 1 {
		t.Errorf("smtp.Send chamado %d vezes", smtp.calls)
	}
	if len(alerts.alerts) != 1 || alerts.alerts[0].Severity != domain.SeverityCritical {
		t.Fatalf("alert not stored: %+v", alerts.alerts)
	}
}

func TestEngineCooldownBlocksDuplicate(t *testing.T) {
	rl := sampleRule(10, 30)
	rules := &fakeRuleRepo{rules: []domain.Rule{rl}}
	alerts := newFakeAlertRepo()
	// simula que já disparou agora.
	alerts.lastFired[rl.ID] = time.Now().UTC()
	notifs := &fakeNotifRepo{}
	src := fakeSource{value: 25}

	e := NewEngine(rules, alerts, notifs, src)
	res, _ := e.Tick(context.Background())
	if res.AlertsFired != 0 {
		t.Fatalf("cooldown ignorado: %+v", res)
	}
}

func TestEngineNoFireWhenBelowThreshold(t *testing.T) {
	rules := &fakeRuleRepo{rules: []domain.Rule{sampleRule(10, 0)}}
	alerts := newFakeAlertRepo()
	notifs := &fakeNotifRepo{}
	src := fakeSource{value: 5} // < 10

	e := NewEngine(rules, alerts, notifs, src)
	res, _ := e.Tick(context.Background())
	if res.AlertsFired != 0 {
		t.Fatalf("disparou abaixo do threshold: %+v", res)
	}
}

func TestEngineAutoResolveWhenConditionClears(t *testing.T) {
	rl := sampleRule(10, 0)
	rules := &fakeRuleRepo{rules: []domain.Rule{rl}}
	alerts := newFakeAlertRepo()
	// já existe alerta ativo mas a métrica caiu — engine deve resolver.
	alerts.hasActive[keyRD(rl.ID, nil)] = true
	notifs := &fakeNotifRepo{}
	src := fakeSource{value: 5}

	e := NewEngine(rules, alerts, notifs, src)
	res, _ := e.Tick(context.Background())
	if res.AlertsResolved != 1 {
		t.Fatalf("auto-resolve não disparou: %+v", res)
	}
}

func TestEngineDuplicateActiveSkipped(t *testing.T) {
	rl := sampleRule(10, 0)
	rules := &fakeRuleRepo{rules: []domain.Rule{rl}}
	alerts := newFakeAlertRepo()
	alerts.hasActive[keyRD(rl.ID, nil)] = true
	notifs := &fakeNotifRepo{}
	src := fakeSource{value: 25} // continua acima

	e := NewEngine(rules, alerts, notifs, src)
	res, _ := e.Tick(context.Background())
	if res.AlertsFired != 0 {
		t.Fatalf("duplicou alerta ativo: %+v", res)
	}
}

func TestEngineDropsWhenChannelMissing(t *testing.T) {
	rl := sampleRule(10, 0)
	// Adicionado canal Telegram, mas não temos notifier de Telegram.
	rl.Channels = append(rl.Channels, domain.Channel{Type: domain.ChannelTelegram, Target: "-100123"})
	rules := &fakeRuleRepo{rules: []domain.Rule{rl}}
	alerts := newFakeAlertRepo()
	notifs := &fakeNotifRepo{}
	src := fakeSource{value: 25}
	smtp := &fakeNotifier{t: domain.ChannelSMTP}

	e := NewEngine(rules, alerts, notifs, src, smtp)
	res, _ := e.Tick(context.Background())
	if res.NotificationsOK != 1 {
		t.Errorf("esperava 1 SMTP enviado, got %d", res.NotificationsOK)
	}
	// A notificação Telegram foi append como dropped.
	var dropped int
	for _, n := range notifs.entries {
		if n.Status == domain.NotificationDropped {
			dropped++
		}
	}
	if dropped != 1 {
		t.Errorf("esperava 1 notif dropped, got %d (entries=%+v)", dropped, notifs.entries)
	}
}
