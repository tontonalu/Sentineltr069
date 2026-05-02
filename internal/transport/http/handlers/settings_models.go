package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsModelsHandler — CRUD de Device Models em /settings/models.
// Domínio expõe Create/List, então a UI é list + create (sem edit/delete).
type SettingsModelsHandler struct {
	Models  domain.DeviceModelRepository
	Vendors domain.VendorRepository
}

// List GET /settings/models
func (h *SettingsModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	models, err := h.Models.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list models failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	vendors, err := h.Vendors.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list vendors for models failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	vendorByID := make(map[uuid.UUID]domain.Vendor, len(vendors))
	for _, v := range vendors {
		vendorByID[v.ID] = v
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.ModelsList(models, vendorByID).Render(r.Context(), w)
}

// NewForm GET /settings/models/new
func (h *SettingsModelsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	vendors, err := h.Vendors.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list vendors failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.ModelNewPage(csrf, vendors, settingspages.ModelDraft{TRDataModel: domain.TR181}, "").Render(r.Context(), w)
}

// Create POST /settings/models
func (h *SettingsModelsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	draft := settingspages.ModelDraft{
		VendorID:    strings.TrimSpace(r.PostFormValue("vendor_id")),
		Model:       strings.TrimSpace(r.PostFormValue("model")),
		TRDataModel: strings.TrimSpace(r.PostFormValue("tr_data_model")),
		Description: strings.TrimSpace(r.PostFormValue("description")),
	}

	vendorID, err := uuid.Parse(draft.VendorID)
	if err != nil || draft.Model == "" {
		h.renderNewWithError(w, r, draft, "Vendor e modelo são obrigatórios.")
		return
	}
	if draft.TRDataModel != domain.TR098 && draft.TRDataModel != domain.TR181 {
		h.renderNewWithError(w, r, draft, "TR Data Model inválido.")
		return
	}

	m := domain.DeviceModel{
		VendorID:    vendorID,
		Model:       draft.Model,
		TRDataModel: draft.TRDataModel,
		Description: draft.Description,
	}
	if err := h.Models.Create(r.Context(), &m); err != nil {
		msg := err.Error()
		if errors.Is(err, domain.ErrModelDuplicate) {
			msg = "Modelo já cadastrado para este vendor."
		}
		logger.FromContext(r.Context()).Info("create model failed", "model", draft.Model, "err", err)
		h.renderNewWithError(w, r, draft, msg)
		return
	}
	http.Redirect(w, r, "/settings/models", http.StatusSeeOther)
}

func (h *SettingsModelsHandler) renderNewWithError(w http.ResponseWriter, r *http.Request, draft settingspages.ModelDraft, msg string) {
	vendors, _ := h.Vendors.List(r.Context())
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = settingspages.ModelNewPage(csrf, vendors, draft, msg).Render(r.Context(), w)
}
