package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	appinventory "github.com/celinet/sentinel-acs/internal/application/inventory"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	devicepages "github.com/celinet/sentinel-acs/internal/views/pages/devices"
)

// DevicesHandler — listagem e detalhes de CPEs.
type DevicesHandler struct {
	Devices   domain.DeviceRepository
	Customers domain.CustomerRepository
	Vendors   domain.VendorRepository
	Models    domain.DeviceModelRepository
	POPs      domain.POPRepository
	GenieACS  *genieacs.Client
	SyncSvc   *appinventory.SyncService
}

const devicesPageSize = 50

// List GET /devices
func (h *DevicesHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	filter := domain.DeviceFilter{
		Status: q.Get("status"),
		Search: strings.TrimSpace(q.Get("q")),
	}
	popStr := q.Get("pop")
	vendorStr := q.Get("vendor")
	if popStr != "" {
		if id, err := uuid.Parse(popStr); err == nil {
			filter.POPID = &id
		}
	}
	if vendorStr != "" {
		if id, err := uuid.Parse(vendorStr); err == nil {
			filter.VendorID = &id
		}
	}

	devs, total, err := h.Devices.List(r.Context(), filter, domain.Page{
		Offset: (page - 1) * devicesPageSize,
		Limit:  devicesPageSize,
	})
	if err != nil {
		logger.FromContext(r.Context()).Error("list devices", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	vendors, _ := h.Vendors.List(r.Context())
	pops, _ := h.POPs.List(r.Context())

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.List(devicepages.ListInput{
		Devices:        devs,
		Vendors:        vendors,
		POPs:           pops,
		Total:          total,
		Page:           page,
		PageSize:       devicesPageSize,
		SelectedPOP:    popStr,
		SelectedVendor: vendorStr,
		SelectedStatus: filter.Status,
		Search:         filter.Search,
		CSRFToken:      csrf,
		CanSync:        h.SyncSvc != nil,
	}).Render(r.Context(), w)
}

// Sync POST /devices/sync — dispara um Tick manual do SyncService.
func (h *DevicesHandler) Sync(w http.ResponseWriter, r *http.Request) {
	if h.SyncSvc == nil {
		http.Error(w, "sync indisponível", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if _, err := h.SyncSvc.Tick(ctx); err != nil {
		logger.FromContext(r.Context()).Error("manual sync", "err", err)
		http.Error(w, "falha ao sincronizar com ACS upstream", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/devices", http.StatusSeeOther)
}

// Detail GET /devices/{id}
func (h *DevicesHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}

	d, err := h.Devices.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrDeviceNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	in := devicepages.DetailInput{Device: *d}

	if d.ModelID != nil {
		if m, err := h.Models.GetByID(r.Context(), *d.ModelID); err == nil {
			in.Model = m
			if v, err := h.Vendors.GetByID(r.Context(), m.VendorID); err == nil {
				in.Vendor = v
			}
		}
	}
	if d.CustomerID != nil {
		if c, err := h.Customers.GetByID(r.Context(), *d.CustomerID); err == nil {
			in.Customer = c
		}
	}
	if d.POPID != nil {
		if p, err := h.POPs.GetByID(r.Context(), *d.POPID); err == nil {
			in.POP = p
		}
	}

	user, _ := mw.UserFromContext(r.Context())
	perms, _ := mw.PermissionsFromContext(r.Context())
	in = devicepages.DetailFromUser(user, perms, in)

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.Detail(csrf, in).Render(r.Context(), w)
}

// ConnectionRequest POST /devices/{id}/connection-request
func (h *DevicesHandler) ConnectionRequest(w http.ResponseWriter, r *http.Request) {
	d := h.deviceFromURL(w, r)
	if d == nil {
		return
	}
	if err := h.GenieACS.ConnectionRequest(r.Context(), d.GenieACSID); err != nil {
		logger.FromContext(r.Context()).Error("connection request", "err", err, "device", d.GenieACSID)
		http.Error(w, "falha ao acionar Connection Request", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/devices/"+d.ID.String(), http.StatusSeeOther)
}

// Reboot POST /devices/{id}/reboot
func (h *DevicesHandler) Reboot(w http.ResponseWriter, r *http.Request) {
	d := h.deviceFromURL(w, r)
	if d == nil {
		return
	}
	if _, err := h.GenieACS.Reboot(r.Context(), d.GenieACSID); err != nil {
		logger.FromContext(r.Context()).Error("reboot", "err", err, "device", d.GenieACSID)
		http.Error(w, "falha ao agendar reboot", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/devices/"+d.ID.String(), http.StatusSeeOther)
}

// deviceFromURL é helper compartilhado para os handlers de ação.
// Já trata 404 e 400; devolve nil quando a request foi respondida.
func (h *DevicesHandler) deviceFromURL(w http.ResponseWriter, r *http.Request) *domain.Device {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return nil
	}
	d, err := h.Devices.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrDeviceNotFound) {
		http.NotFound(w, r)
		return nil
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil
	}
	return d
}
