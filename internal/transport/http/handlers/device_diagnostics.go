// device_diagnostics — handlers do refresh manual e dos diagnósticos remotos
// (ping/traceroute) executados a partir da página /devices/{id}.
//
// Convenções:
//
//   - Refresh tem cooldown de 30s por device. Estado em memória — single-server
//     OK; quando escalar pra múltiplos pods migrar pra Redis (chave SETNX EX 30).
//
//   - Endpoints de ping/traceroute devolvem fragmento HTMX com a linha do
//     diagnostic já em "running"; o front faz polling em /diagnostics/{id}
//     a cada 2s até o status virar terminal (complete/error/timeout).
package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	diagapp "github.com/celinet/sentinel-acs/internal/application/diagnostics"
	diagdom "github.com/celinet/sentinel-acs/internal/domain/diagnostics"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	devicepages "github.com/celinet/sentinel-acs/internal/views/pages/devices"
)

// RefreshGate — cooldown em memória para refresh-telemetry. Mapa
// `device_id → próximo horário válido`. Mutex protege escrita concorrente.
type RefreshGate struct {
	mu      sync.Mutex
	until   map[uuid.UUID]time.Time
	cooldown time.Duration
}

func NewRefreshGate(cooldown time.Duration) *RefreshGate {
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &RefreshGate{
		until:    map[uuid.UUID]time.Time{},
		cooldown: cooldown,
	}
}

// Reserve devolve true se o caller pode prosseguir; false se ainda em cooldown.
// Atomicamente reserva o slot atualizando o `until` para now+cooldown.
func (g *RefreshGate) Reserve(deviceID uuid.UUID) (ok bool, retryAfter time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if t, exists := g.until[deviceID]; exists && t.After(now) {
		return false, t.Sub(now)
	}
	g.until[deviceID] = now.Add(g.cooldown)
	return true, 0
}

// RefreshTelemetry POST /devices/{id}/refresh-telemetry — aciona Refresh()
// no NBI pra revalidar toda a árvore do CPE. Resposta é um fragmento HTMX
// que substitui o botão por uma mensagem de sucesso/erro.
//
// Não rodamos o collector inline — telemetry continua sendo agregada no
// próximo tick (5min). O Refresh garante que a próxima leitura via cache
// trará valores frescos. Compromisso pragmático: chamar o collector aqui
// dispararia work pesado fora da janela controlada.
func (h *DeviceTabsHandler) RefreshTelemetry(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	if h.GenieACS == nil {
		http.Error(w, "GenieACS não configurado", http.StatusServiceUnavailable)
		return
	}
	if h.RefreshGate == nil {
		// Defensivo — server main sempre injeta.
		h.RefreshGate = NewRefreshGate(30 * time.Second)
	}

	allow, retry := h.RefreshGate.Reserve(id)
	if !allow {
		// Devolvemos 200 com fragmento — HTMX renderiza inline. 429 também
		// funcionaria mas a UI ficaria sem feedback visível.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = devicepages.RefreshResult(devicepages.RefreshResultInput{
			DeviceID:    id,
			OK:          false,
			Message:     "Aguarde " + strconv.Itoa(int(retry.Seconds()+0.5)) + "s para tentar de novo",
			NextRetryIn: retry,
		}).Render(r.Context(), w)
		return
	}

	dev, err := h.Devices.GetByID(r.Context(), id)
	if err != nil || dev == nil {
		http.Error(w, "device não encontrado", http.StatusNotFound)
		return
	}
	if _, err := h.GenieACS.Refresh(r.Context(), dev.GenieACSID, ""); err != nil {
		logger.FromContext(r.Context()).Warn("refresh telemetry", "err", err, "device", id)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = devicepages.RefreshResult(devicepages.RefreshResultInput{
			DeviceID: id,
			OK:       false,
			Message:  "Falha ao agendar refresh: " + err.Error(),
		}).Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.RefreshResult(devicepages.RefreshResultInput{
		DeviceID: id,
		OK:       true,
		Message:  "Refresh agendado — dados serão atualizados nos próximos ~30s",
	}).Render(r.Context(), w)
}

// RefreshButton GET /devices/{id}/refresh-telemetry/button — devolve apenas
// o botão de refresh. Usado pelo HTMX delayed-swap depois do cooldown
// expirar, devolvendo o controle ao operador sem precisar reload da página.
func (h *DeviceTabsHandler) RefreshButton(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.RefreshButtonOnly(id).Render(r.Context(), w)
}

// RunPing POST /devices/{id}/diagnostics/ping — dispara IPPingDiagnostics.
func (h *DeviceTabsHandler) RunPing(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	if h.Diagnostics == nil {
		http.Error(w, "diagnósticos não configurados", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	req := diagdom.PingRequest{
		Host:      strings.TrimSpace(r.PostForm.Get("host")),
		Count:     atoiOr(r.PostForm.Get("count"), 4),
		SizeBytes: atoiOr(r.PostForm.Get("size_bytes"), 64),
		TimeoutMS: atoiOr(r.PostForm.Get("timeout_ms"), 5000),
		Interface: strings.TrimSpace(r.PostForm.Get("interface")),
	}
	var actorID *uuid.UUID
	if u, ok := mw.UserFromContext(r.Context()); ok && u != nil {
		actorID = &u.ID
	}
	d, err := h.Diagnostics.EnqueuePing(r.Context(), id, req, actorID)
	if err != nil {
		writeDiagnosticError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.DiagnosticRow(devicepages.DiagnosticRowInput{
		DeviceID:   id,
		Diagnostic: *d,
	}).Render(r.Context(), w)
}

// RunTraceroute POST /devices/{id}/diagnostics/traceroute
func (h *DeviceTabsHandler) RunTraceroute(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	if h.Diagnostics == nil {
		http.Error(w, "diagnósticos não configurados", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	req := diagdom.TracerouteRequest{
		Host:      strings.TrimSpace(r.PostForm.Get("host")),
		MaxHops:   atoiOr(r.PostForm.Get("max_hops"), 30),
		SizeBytes: atoiOr(r.PostForm.Get("size_bytes"), 64),
		TimeoutMS: atoiOr(r.PostForm.Get("timeout_ms"), 5000),
		Interface: strings.TrimSpace(r.PostForm.Get("interface")),
	}
	var actorID *uuid.UUID
	if u, ok := mw.UserFromContext(r.Context()); ok && u != nil {
		actorID = &u.ID
	}
	d, err := h.Diagnostics.EnqueueTraceroute(r.Context(), id, req, actorID)
	if err != nil {
		writeDiagnosticError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.DiagnosticRow(devicepages.DiagnosticRowInput{
		DeviceID:   id,
		Diagnostic: *d,
	}).Render(r.Context(), w)
}

// DiagnosticFragment GET /devices/{id}/diagnostics/{diagID} — fragmento HTMX
// auto-polling. Quando status não é terminal, devolve com hx-trigger="every 2s";
// caso contrário retorna sem trigger pra parar o polling.
func (h *DeviceTabsHandler) DiagnosticFragment(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	if h.DiagnosticsRepo == nil {
		http.Error(w, "diagnósticos não configurados", http.StatusServiceUnavailable)
		return
	}
	diagID, err := uuid.Parse(chi.URLParam(r, "diag_id"))
	if err != nil {
		http.Error(w, "diag_id inválido", http.StatusBadRequest)
		return
	}
	d, err := h.DiagnosticsRepo.GetByID(r.Context(), diagID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if d.DeviceID != id {
		http.Error(w, "diagnostic não pertence ao device", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.DiagnosticRow(devicepages.DiagnosticRowInput{
		DeviceID:   id,
		Diagnostic: *d,
	}).Render(r.Context(), w)
}

// ──────────── helpers ────────────

func writeDiagnosticError(w http.ResponseWriter, r *http.Request, err error) {
	logger.FromContext(r.Context()).Warn("diagnostic enqueue", "err", err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = devicepages.DiagnosticErrorBanner(err.Error()).Render(r.Context(), w)
}

func atoiOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

var _ = diagapp.NewService // mantém o import — campo Diagnostics é desse tipo.
