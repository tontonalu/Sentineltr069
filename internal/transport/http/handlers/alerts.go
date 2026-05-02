package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
	identity "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	alertpages "github.com/celinet/sentinel-acs/internal/views/pages/alerts"
)

// AlertsHandler — UI e ações sobre regras + alertas.
type AlertsHandler struct {
	Rules  domain.RuleRepository
	Alerts domain.AlertRepository
}

// List GET /alerts — alertas ativos + regras cadastradas.
func (h *AlertsHandler) List(w http.ResponseWriter, r *http.Request) {
	active, err := h.Alerts.ListActive(r.Context(), 100)
	if err != nil {
		logger.FromContext(r.Context()).Error("alerts list active", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rules, err := h.Rules.List(r.Context(), false)
	if err != nil {
		logger.FromContext(r.Context()).Error("alerts list rules", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	perms, _ := mw.PermissionsFromContext(r.Context())
	canAck := perms != nil && perms.Has("alert", "acknowledge", identity.GlobalScope)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = alertpages.List(alertpages.ListInput{
		Active: active,
		Rules:  rules,
		CSRF:   mw.CSRFTokenFromContext(r.Context()),
		CanAck: canAck,
	}).Render(r.Context(), w)
}

// NewForm GET /alerts/rules/new
func (h *AlertsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = alertpages.Form(alertpages.FormInput{
		CSRF:            mw.CSRFTokenFromContext(r.Context()),
		Severity:        domain.SeverityWarning,
		IsActive:        true,
		CooldownMinutes: 15,
		ConditionJSON:   `{"type":"aggregate","metric":"device_status","filter":{"status":"offline"},"aggregation":"count_pct","operator":">","threshold":10,"window":"5m"}`,
		ChannelsJSON:    `[]`,
	}).Render(r.Context(), w)
}

// EditForm GET /alerts/rules/{id}/edit
func (h *AlertsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	rl, err := h.Rules.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrRuleNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("alerts get rule", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	chJSON, _ := json.MarshalIndent(rl.Channels, "", "  ")
	condJSON, _ := json.MarshalIndent(rl.Condition, "", "  ")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = alertpages.Form(alertpages.FormInput{
		CSRF:            mw.CSRFTokenFromContext(r.Context()),
		RuleID:          rl.ID.String(),
		Name:            rl.Name,
		Description:     rl.Description,
		Severity:        rl.Severity,
		IsActive:        rl.IsActive,
		CooldownMinutes: rl.CooldownMinutes,
		ConditionJSON:   string(condJSON),
		ChannelsJSON:    string(chJSON),
	}).Render(r.Context(), w)
}

// Create POST /alerts/rules
func (h *AlertsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	in, errs := parseRuleForm(r)
	if len(errs) > 0 {
		h.renderFormErr(w, r, in, errs, "")
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	rl := buildRuleFromForm(in)
	if user != nil {
		rl.CreatedBy = &user.ID
	}
	if err := h.Rules.Create(r.Context(), rl); err != nil {
		h.renderFormErr(w, r, in, []string{err.Error()}, "")
		return
	}
	http.Redirect(w, r, "/alerts", http.StatusSeeOther)
}

// Update POST /alerts/rules/{id}
func (h *AlertsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	in, errs := parseRuleForm(r)
	in.RuleID = id.String()
	if len(errs) > 0 {
		h.renderFormErr(w, r, in, errs, id.String())
		return
	}
	rl := buildRuleFromForm(in)
	rl.ID = id
	if err := h.Rules.Update(r.Context(), rl); err != nil {
		h.renderFormErr(w, r, in, []string{err.Error()}, id.String())
		return
	}
	http.Redirect(w, r, "/alerts", http.StatusSeeOther)
}

// Acknowledge POST /alerts/{id}/ack — HTMX swap, devolve a row atualizada.
func (h *AlertsHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "auth required", http.StatusUnauthorized)
		return
	}
	if err := h.Alerts.Acknowledge(r.Context(), id, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a, err := h.Alerts.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = alertpages.AlertRow(mw.CSRFTokenFromContext(r.Context()), true, *a).Render(r.Context(), w)
}

// Resolve POST /alerts/{id}/resolve — fecha o alerta.
func (h *AlertsHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := h.Alerts.Resolve(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Após resolve, a row some — devolvemos vazio para HTMX limpar.
	w.WriteHeader(http.StatusOK)
}

// ──────────── helpers ────────────

func (h *AlertsHandler) renderFormErr(w http.ResponseWriter, r *http.Request, in alertpages.FormInput, errs []string, ruleID string) {
	in.RuleID = ruleID
	in.CSRF = mw.CSRFTokenFromContext(r.Context())
	in.Errors = errs
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = alertpages.Form(in).Render(r.Context(), w)
}

func parseRuleForm(r *http.Request) (alertpages.FormInput, []string) {
	cooldown, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("cooldown_minutes")))
	in := alertpages.FormInput{
		Name:            strings.TrimSpace(r.PostForm.Get("name")),
		Description:     strings.TrimSpace(r.PostForm.Get("description")),
		Severity:        domain.Severity(strings.TrimSpace(r.PostForm.Get("severity"))),
		IsActive:        r.PostForm.Get("is_active") == "1",
		CooldownMinutes: cooldown,
		ConditionJSON:   r.PostForm.Get("condition"),
		ChannelsJSON:    r.PostForm.Get("channels"),
	}

	var errs []string
	if in.Name == "" {
		errs = append(errs, "nome obrigatório")
	}
	if !in.Severity.Valid() {
		errs = append(errs, "severidade inválida")
	}

	cond, err := domain.UnmarshalCondition([]byte(in.ConditionJSON))
	if err != nil {
		errs = append(errs, "condition: "+err.Error())
	} else if err := cond.Validate(); err != nil {
		errs = append(errs, "condition inválida: "+err.Error())
	}

	if in.ChannelsJSON != "" {
		var ch []domain.Channel
		if err := json.Unmarshal([]byte(in.ChannelsJSON), &ch); err != nil {
			errs = append(errs, "channels: JSON inválido")
		} else {
			for i, c := range ch {
				if !c.Type.Valid() {
					errs = append(errs, "channels["+strconv.Itoa(i)+"].type inválido")
				}
				if c.Target == "" {
					errs = append(errs, "channels["+strconv.Itoa(i)+"].target vazio")
				}
			}
		}
	}
	return in, errs
}

func buildRuleFromForm(in alertpages.FormInput) *domain.Rule {
	cond, _ := domain.UnmarshalCondition([]byte(in.ConditionJSON))
	var channels []domain.Channel
	if in.ChannelsJSON != "" {
		_ = json.Unmarshal([]byte(in.ChannelsJSON), &channels)
	}
	return &domain.Rule{
		Name:            in.Name,
		Description:     in.Description,
		Condition:       cond,
		Severity:        in.Severity,
		Channels:        channels,
		IsActive:        in.IsActive,
		CooldownMinutes: in.CooldownMinutes,
	}
}
