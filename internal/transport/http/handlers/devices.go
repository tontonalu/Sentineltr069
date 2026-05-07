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

	// Resolve customers em batch (1 query por device é OK para a página
	// atual — pageSize=50; se virar gargalo trocamos por GetByIDs em lote).
	customers := make(map[uuid.UUID]*domain.Customer, len(devs))
	for _, d := range devs {
		if d.CustomerID == nil {
			continue
		}
		if c, err := h.Customers.GetByID(r.Context(), *d.CustomerID); err == nil {
			customers[d.ID] = c
		}
	}

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.List(devicepages.ListInput{
		Devices:           devs,
		Vendors:           vendors,
		POPs:              pops,
		Total:             total,
		Page:              page,
		PageSize:          devicesPageSize,
		CustomersByDevice: customers,
		SelectedPOP:       popStr,
		SelectedVendor:    vendorStr,
		SelectedStatus:    filter.Status,
		Search:            filter.Search,
		CSRFToken:         csrf,
		CanSync:           h.SyncSvc != nil,
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

	// Catálogo completo para o dropdown "Atribuir modelo". Mesmo quando o
	// device já tem modelo, deixamos o usuário trocar (devices de lab podem
	// ser reaproveitados entre homologações de modelos diferentes).
	if all, err := h.Models.List(r.Context()); err == nil {
		in.AllModels = all
		vendorByID := make(map[uuid.UUID]string, len(all))
		if vs, err := h.Vendors.List(r.Context()); err == nil {
			for _, v := range vs {
				vendorByID[v.ID] = v.Name
			}
		}
		in.VendorNameByID = vendorByID
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

// SetModel POST /devices/{id}/set-model — atribui (ou limpa) o modelo do device.
// Form param `model_id` = UUID do DeviceModel. Vazio limpa o vínculo.
//
// Caso de uso: quando o sync com GenieACS não detectou Manufacturer/ModelName
// (ONUs que só populam parte do TR-069), o usuário pode cadastrar o modelo em
// /settings/models e aqui apontar o device para ele — desbloqueia homologação.
func (h *DevicesHandler) SetModel(w http.ResponseWriter, r *http.Request) {
	d := h.deviceFromURL(w, r)
	if d == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.PostForm.Get("model_id"))
	var modelID *uuid.UUID
	if raw != "" {
		mid, err := uuid.Parse(raw)
		if err != nil {
			http.Error(w, "model_id inválido", http.StatusBadRequest)
			return
		}
		// Garante que o modelo existe (FK no Postgres já protege, mas aqui
		// damos uma mensagem amigável em vez de 500).
		if _, err := h.Models.GetByID(r.Context(), mid); err != nil {
			if errors.Is(err, domain.ErrModelNotFound) {
				http.Error(w, "modelo não encontrado", http.StatusBadRequest)
				return
			}
			logger.FromContext(r.Context()).Error("get model", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		modelID = &mid
	}
	if err := h.Devices.SetModel(r.Context(), d.ID, modelID); err != nil {
		logger.FromContext(r.Context()).Error("set model", "err", err, "device", d.ID)
		http.Error(w, "falha ao atribuir modelo", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/devices/"+d.ID.String(), http.StatusSeeOther)
}

// MarkLab POST /devices/{id}/mark-lab — toggle is_homologation_lab.
// Form param `lab` = "1" liga, qualquer outro valor (ou ausente) desliga.
// Apenas devices marcados podem ser usados como lab_device do wizard.
func (h *DevicesHandler) MarkLab(w http.ResponseWriter, r *http.Request) {
	d := h.deviceFromURL(w, r)
	if d == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	isLab := r.PostForm.Get("lab") == "1"
	if err := h.Devices.SetHomologationLab(r.Context(), d.ID, isLab); err != nil {
		logger.FromContext(r.Context()).Error("mark lab", "err", err, "device", d.ID)
		http.Error(w, "falha ao atualizar flag de laboratório", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/devices/"+d.ID.String(), http.StatusSeeOther)
}

// Delete POST /devices/{id}/delete — remove o device do Postgres e do ACS upstream.
func (h *DevicesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	d := h.deviceFromURL(w, r)
	if d == nil {
		return
	}
	if h.SyncSvc == nil {
		http.Error(w, "sync indisponível", http.StatusServiceUnavailable)
		return
	}
	if err := h.SyncSvc.DeleteDevice(r.Context(), d.ID); err != nil {
		logger.FromContext(r.Context()).Error("delete device", "err", err, "device", d.GenieACSID)
		http.Error(w, "falha ao excluir device", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/devices?deleted=1", http.StatusSeeOther)
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
