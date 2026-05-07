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
	diagapp "github.com/celinet/sentinel-acs/internal/application/diagnostics"
	diagdom "github.com/celinet/sentinel-acs/internal/domain/diagnostics"
	identity "github.com/celinet/sentinel-acs/internal/domain/identity"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	devicepages "github.com/celinet/sentinel-acs/internal/views/pages/devices"
)

// DeviceTabsHandler — handlers ortogonais ao DevicesHandler. Mantemos
// separados porque dependem do ProfileViewSvc + Telemetry repo (opcionais),
// enquanto DevicesHandler é o CRUD básico que sempre existe.
//
// Vendors/Models/Customers/POPs são lookups secundários para enriquecer
// a aba "Dispositivo" (Identificação completa com fabricante, plano, PPPoE).
type DeviceTabsHandler struct {
	Devices     domain.DeviceRepository
	Vendors     domain.VendorRepository
	Models      domain.DeviceModelRepository
	Customers   domain.CustomerRepository
	POPs        domain.POPRepository
	ProfileView *devapp.Service
	Telemetry   tele.Repository

	// Refresh manual (botão na aba Estatísticas) + diagnostics remotos
	// (ping/traceroute via TR-069). Todos opcionais — se nil, as rotas
	// associadas não são registradas (server main checa).
	GenieACS        *genieacs.Client
	RefreshGate     *RefreshGate
	Diagnostics     *diagapp.Service
	DiagnosticsRepo diagdom.Repository
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
		vendor, model, customer, pop := h.loadIdentificationLookups(r, dev)
		_ = devicepages.TabDevice(devicepages.TabInput{
			Device:    *dev,
			Vendor:    vendor,
			Model:     model,
			Customer:  customer,
			POP:       pop,
			View:      pv,
			CSRFToken: csrf,
			CanEdit:   canEdit,
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
		var history []diagdom.Diagnostic
		if h.DiagnosticsRepo != nil {
			history, _ = h.DiagnosticsRepo.ListByDevice(r.Context(), id, 20)
		}
		_ = devicepages.TabDiag(devicepages.DiagInput{
			Device:      *dev,
			History:     history,
			CanDiagnose: userHasPermission(r, "device", "diagnose"),
		}).Render(r.Context(), w)
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

// loadIdentificationLookups — Vendor/Model/Customer/POP do device. Tolerante
// a erros: se uma das queries falhar, segue com nil naquele slot. Não há
// motivo para abortar a aba inteira por causa de um lookup secundário.
func (h *DeviceTabsHandler) loadIdentificationLookups(r *http.Request, d *domain.Device) (
	*domain.Vendor, *domain.DeviceModel, *domain.Customer, *domain.POP,
) {
	var (
		vendor   *domain.Vendor
		model    *domain.DeviceModel
		customer *domain.Customer
		pop      *domain.POP
	)
	if d.ModelID != nil && h.Models != nil {
		if m, err := h.Models.GetByID(r.Context(), *d.ModelID); err == nil {
			model = m
			if h.Vendors != nil {
				if v, err := h.Vendors.GetByID(r.Context(), m.VendorID); err == nil {
					vendor = v
				}
			}
		}
	}
	if d.CustomerID != nil && h.Customers != nil {
		if c, err := h.Customers.GetByID(r.Context(), *d.CustomerID); err == nil {
			customer = c
		}
	}
	if d.POPID != nil && h.POPs != nil {
		if p, err := h.POPs.GetByID(r.Context(), *d.POPID); err == nil {
			pop = p
		}
	}
	return vendor, model, customer, pop
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
//
// Além das séries para os charts, popula KPIs (último valor coletado de
// CPU/Mem/Temp/Wi-Fi clients) consultando direto o ponto mais recente das
// queries raw — independente do range (sempre últimos 15 min).
func (h *DeviceTabsHandler) loadStats(r *http.Request, id uuid.UUID, dev domain.Device, rangeStr string) devicepages.StatsInput {
	in := devicepages.StatsInput{
		Device:              dev,
		Range:               rangeStr,
		CanRefreshTelemetry: userHasPermission(r, "telemetry", "read"),
	}
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
			in.TempSeries = systemHourlyToTempSeries(sys)
		}
	} else {
		if w, err := h.Telemetry.QueryWifiRaw(r.Context(), id, rg); err == nil {
			in.WifiSeries, in.WifiSeries5G = bucketRawWifiByBand(w)
			in.LatestWifiClients = latestWifiClients(w)
		}
		if wan, err := h.Telemetry.QueryWanRaw(r.Context(), id, rg); err == nil {
			in.WanRxSeries = wanRawToSeries(wan, true)
			in.WanTxSeries = wanRawToSeries(wan, false)
		}
		if sys, err := h.Telemetry.QuerySystemRaw(r.Context(), id, rg); err == nil {
			in.CPUSeries, in.MemSeries = systemRawToSeries(sys)
			in.TempSeries = systemRawToTempSeries(sys)
			in.LatestCPUPct, in.LatestMemPct, in.LatestTemperatureC = latestSystemKPIs(sys)
		}
	}

	// KPIs sempre da janela curta (15min) — senão hourly traria dado velho.
	now := time.Now().UTC()
	short := tele.Range{From: now.Add(-15 * time.Minute), To: now}
	if w, err := h.Telemetry.QueryWifiRaw(r.Context(), id, short); err == nil && in.LatestWifiClients == nil {
		in.LatestWifiClients = latestWifiClients(w)
	}
	if sys, err := h.Telemetry.QuerySystemRaw(r.Context(), id, short); err == nil && in.LatestCPUPct == nil {
		in.LatestCPUPct, in.LatestMemPct, in.LatestTemperatureC = latestSystemKPIs(sys)
	}
	return in
}

// systemHourlyToTempSeries — extrai max_temperature_c por bucket. Usamos
// MAX em vez de AVG porque temperatura monitorada é "pior caso da hora".
func systemHourlyToTempSeries(pts []tele.HourlySystemPoint) []devicepages.SeriesPoint {
	out := make([]devicepages.SeriesPoint, 0, len(pts))
	for _, p := range pts {
		if p.MaxTemperatureC == nil {
			continue
		}
		out = append(out, devicepages.SeriesPoint{X: p.Bucket.UnixMilli(), Y: *p.MaxTemperatureC})
	}
	return out
}

func systemRawToTempSeries(samples []tele.SystemSample) []devicepages.SeriesPoint {
	out := make([]devicepages.SeriesPoint, 0, len(samples))
	for _, s := range samples {
		if s.TemperatureC == nil {
			continue
		}
		out = append(out, devicepages.SeriesPoint{X: s.Time.UnixMilli(), Y: *s.TemperatureC})
	}
	return out
}

// latestSystemKPIs — devolve último ponto não-nulo de cada métrica.
// Iteramos do mais recente pro mais antigo (samples vêm ordenadas ASC).
func latestSystemKPIs(samples []tele.SystemSample) (cpu, mem, temp *float64) {
	for i := len(samples) - 1; i >= 0; i-- {
		s := samples[i]
		if cpu == nil && s.CPUPct != nil {
			cpu = s.CPUPct
		}
		if mem == nil && s.MemPct != nil {
			mem = s.MemPct
		}
		if temp == nil && s.TemperatureC != nil {
			temp = s.TemperatureC
		}
		if cpu != nil && mem != nil && temp != nil {
			break
		}
	}
	return
}

// latestWifiClients — soma o ConnectedClients do último timestamp. Quando
// um device tem múltiplos SSIDs (2.4G + 5G), o valor agregado é o que
// importa para o KPI (operador pensa "X clientes no AP").
func latestWifiClients(samples []tele.WifiSample) *int {
	if len(samples) == 0 {
		return nil
	}
	// Encontra o time mais recente.
	var lastT time.Time
	for _, s := range samples {
		if s.Time.After(lastT) {
			lastT = s.Time
		}
	}
	// Agrega tudo dentro de uma janela de 5 min em torno desse time
	// (samples do mesmo tick têm timestamps idênticos por design, mas
	// ser tolerante evita 0 quando o tick demorou).
	total := 0
	any := false
	for _, s := range samples {
		if lastT.Sub(s.Time) > 5*time.Minute {
			continue
		}
		if s.ConnectedClients != nil {
			total += *s.ConnectedClients
			any = true
		}
	}
	if !any {
		return nil
	}
	return &total
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
