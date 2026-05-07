// device_tabs — handlers da página de detalhe com abas (/devices/{id}).
//
// Cada aba é carregada via HTMX a partir de /devices/{id}/tabs/{name},
// devolvendo um fragmento de HTML. Edição inline de campos passa por
// POST /devices/{id}/fields/{canonical_key} que cria provisioning_jobs
// single-parameter.
package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	devapp "github.com/celinet/sentinel-acs/internal/application/devices"
	identity "github.com/celinet/sentinel-acs/internal/domain/identity"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	devicepages "github.com/celinet/sentinel-acs/internal/views/pages/devices"
)

// DeviceTabsHandler — handlers ortogonais ao DevicesHandler. Mantemos
// separados porque dependem do ProfileViewSvc + Telemetry repo (opcionais),
// enquanto DevicesHandler é o CRUD básico que sempre existe.
type DeviceTabsHandler struct {
	Devices     domain.DeviceRepository
	ProfileView *devapp.Service
	Telemetry   tele.Repository
}

// Tab GET /devices/{id}/tabs/{name} — fragmento HTMX por aba.
func (h *DeviceTabsHandler) Tab(w http.ResponseWriter, r *http.Request) {
	id, dev, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	name := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "name")))

	// Carrega ProfileView só pra abas que dependem de mappings.
	var pv *devapp.DeviceProfileView
	switch name {
	case "device", "internet", "wireless", "lan", "voip":
		var err error
		pv, err = h.ProfileView.LoadProfileView(r.Context(), id)
		if err != nil {
			logger.FromContext(r.Context()).Error("profile view", "err", err, "device", id)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	csrf := mw.CSRFTokenFromContext(r.Context())
	canEdit := userHasPermission(r, "device", "configure")

	switch name {
	case "device":
		_ = devicepages.TabDevice(devicepages.TabInput{
			Device: *dev, View: pv, CSRFToken: csrf, CanEdit: canEdit,
		}).Render(r.Context(), w)
	case "internet":
		_ = devicepages.TabCategory(devicepages.CategoryInput{
			Device: *dev, View: pv, CSRFToken: csrf, CanEdit: canEdit,
			Category: "wan", Title: "Internet",
		}).Render(r.Context(), w)
	case "wireless":
		_ = devicepages.TabCategory(devicepages.CategoryInput{
			Device: *dev, View: pv, CSRFToken: csrf, CanEdit: canEdit,
			Category: "wifi", Title: "Wireless",
		}).Render(r.Context(), w)
	case "lan":
		_ = devicepages.TabCategory(devicepages.CategoryInput{
			Device: *dev, View: pv, CSRFToken: csrf, CanEdit: canEdit,
			Category: "lan", Title: "LAN",
		}).Render(r.Context(), w)
	case "voip":
		_ = devicepages.TabCategory(devicepages.CategoryInput{
			Device: *dev, View: pv, CSRFToken: csrf, CanEdit: canEdit,
			Category: "voice", Title: "VoIP",
		}).Render(r.Context(), w)
	case "hosts":
		hosts := h.loadHosts(r, id)
		_ = devicepages.TabHosts(devicepages.HostsInput{Device: *dev, Hosts: hosts}).Render(r.Context(), w)
	case "ports":
		ports := h.loadPorts(r, id)
		_ = devicepages.TabPorts(devicepages.PortsInput{Device: *dev, Ports: ports}).Render(r.Context(), w)
	case "stats":
		rng := r.URL.Query().Get("range")
		if rng == "" {
			rng = "24h"
		}
		input := h.loadStats(r, id, *dev, rng)
		_ = devicepages.TabStats(input).Render(r.Context(), w)
	case "diag":
		_ = devicepages.TabDiag(devicepages.DiagInput{Device: *dev}).Render(r.Context(), w)
	default:
		http.Error(w, "aba desconhecida", http.StatusNotFound)
	}
}

// UpdateField POST /devices/{id}/fields/{canonical_key}
// Form: value=...
// Resposta: fragmento HTMX com a linha do field atualizada + banner de job.
func (h *DeviceTabsHandler) UpdateField(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deviceFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(chi.URLParam(r, "canonical_key"))
	value := r.PostForm.Get("value")
	if key == "" {
		http.Error(w, "canonical_key obrigatório", http.StatusBadRequest)
		return
	}

	var actorID *uuid.UUID
	if u, ok := mw.UserFromContext(r.Context()); ok && u != nil {
		actorID = &u.ID
	}

	field, jobID, err := h.ProfileView.UpdateField(r.Context(), id, key, value, actorID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		// Devolvemos 200 com fragmento de erro para HTMX renderizar inline
		// (em vez de quebrar a UI). O field continua presente, com Error.
		_ = devicepages.FieldError(devicepages.FieldErrorInput{
			CanonicalKey: key, Message: err.Error(),
		}).Render(r.Context(), w)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	canEdit := userHasPermission(r, "device", "configure")
	_ = devicepages.FieldRow(devicepages.FieldRowInput{
		DeviceID:  id, Field: *field, CSRFToken: csrf, CanEdit: canEdit,
		EnqueuedJob: jobID,
	}).Render(r.Context(), w)
}

// ──────────── helpers ────────────

func (h *DeviceTabsHandler) deviceFromURL(w http.ResponseWriter, r *http.Request) (uuid.UUID, *domain.Device, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return uuid.Nil, nil, false
	}
	d, err := h.Devices.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrDeviceNotFound) {
		http.NotFound(w, r)
		return uuid.Nil, nil, false
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("device tab get", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return uuid.Nil, nil, false
	}
	return id, d, true
}

// loadHosts — última janela de 15 min do TimescaleDB.
func (h *DeviceTabsHandler) loadHosts(r *http.Request, id uuid.UUID) []tele.HostSample {
	if h.Telemetry == nil {
		return nil
	}
	now := time.Now().UTC()
	rg := tele.Range{From: now.Add(-15 * time.Minute), To: now}
	hosts, err := h.Telemetry.LatestHostsByDevice(r.Context(), id, rg)
	if err != nil {
		logger.FromContext(r.Context()).Debug("hosts query", "err", err)
		return nil
	}
	return hosts
}

func (h *DeviceTabsHandler) loadPorts(r *http.Request, id uuid.UUID) []tele.PortSample {
	if h.Telemetry == nil {
		return nil
	}
	now := time.Now().UTC()
	rg := tele.Range{From: now.Add(-15 * time.Minute), To: now}
	ports, err := h.Telemetry.LatestPortsByDevice(r.Context(), id, rg)
	if err != nil {
		logger.FromContext(r.Context()).Debug("ports query", "err", err)
		return nil
	}
	return ports
}

// loadStats — reusa o mesmo formato `HistoryInput` do /devices/{id}/history,
// que já tem renderer SVG (history.templ:dualLineSVG).
func (h *DeviceTabsHandler) loadStats(r *http.Request, id uuid.UUID, dev domain.Device, rangeStr string) devicepages.StatsInput {
	in := devicepages.StatsInput{Device: dev, Range: rangeStr}
	if h.Telemetry == nil {
		return in
	}
	rg, useHourly := rangeFromString(rangeStr)
	if useHourly {
		if pts, err := h.Telemetry.QueryWifiHourly(r.Context(), id, rg); err == nil {
			in.WifiSeries, in.WifiSeries5G = bucketHourlyByBand(pts)
		}
		if wan, err := h.Telemetry.QueryWanHourly(r.Context(), id, rg); err == nil {
			in.WanRxSeries = wanHourlyToSeries(wan, true)
			in.WanTxSeries = wanHourlyToSeries(wan, false)
		}
		if sys, err := h.Telemetry.QuerySystemHourly(r.Context(), id, rg); err == nil {
			in.CPUSeries, in.MemSeries = systemHourlyToSeries(sys)
		}
	} else {
		if w, err := h.Telemetry.QueryWifiRaw(r.Context(), id, rg); err == nil {
			in.WifiSeries, in.WifiSeries5G = bucketRawWifiByBand(w)
		}
		if wan, err := h.Telemetry.QueryWanRaw(r.Context(), id, rg); err == nil {
			in.WanRxSeries = wanRawToSeries(wan, true)
			in.WanTxSeries = wanRawToSeries(wan, false)
		}
		if sys, err := h.Telemetry.QuerySystemRaw(r.Context(), id, rg); err == nil {
			in.CPUSeries, in.MemSeries = systemRawToSeries(sys)
		}
	}
	return in
}

// userHasPermission — wrapper sobre EffectivePermissions para uso pontual
// nos handlers de fragments (a route já tem RequirePermission, mas dentro
// do mesmo middleware uma página precisa decidir condicional na UI).
func userHasPermission(r *http.Request, resource, action string) bool {
	perms, ok := mw.PermissionsFromContext(r.Context())
	if !ok || perms == nil {
		return false
	}
	return perms.Has(resource, action, identity.GlobalScope)
}
