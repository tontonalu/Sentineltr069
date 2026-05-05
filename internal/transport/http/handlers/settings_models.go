package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsModelsHandler — CRUD de Device Models em /settings/models.
// Domínio expõe Create/List, então a UI é list + create (sem edit/delete).
//
// HomModel e Templates são opcionais: quando fornecidos, a List exibe coluna
// de "Homologações" e a página /settings/models/{id}/homologations fica disponível.
type SettingsModelsHandler struct {
	Models    domain.DeviceModelRepository
	Vendors   domain.VendorRepository
	HomModel  hom.ModelHomologationRepo
	Templates *tplapp.Service
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

	// Conta homologações ativas por modelo. Sem HomModel injetado, mapa fica
	// vazio e a coluna mostra "0" universalmente.
	homCount := make(map[uuid.UUID]int, len(models))
	if h.HomModel != nil {
		for _, m := range models {
			recs, err := h.HomModel.ListByModel(r.Context(), m.ID)
			if err != nil {
				continue
			}
			for _, rec := range recs {
				if rec.Status == hom.StatusHomologated {
					homCount[m.ID]++
				}
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.ModelsList(models, vendorByID, homCount).Render(r.Context(), w)
}

// Homologations GET /settings/models/{id}/homologations
func (h *SettingsModelsHandler) Homologations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	model, err := h.Models.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrModelNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get model", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	in := settingspages.ModelHomologationsInput{Model: *model}
	if v, err := h.Vendors.GetByID(r.Context(), model.VendorID); err == nil {
		in.Vendor = v
	}
	if h.HomModel != nil {
		records, _ := h.HomModel.ListByModel(r.Context(), id)
		for _, rec := range records {
			row := settingspages.ModelHomologationsRow{Record: rec}
			if h.Templates != nil {
				if p, err := h.Templates.LoadFull(r.Context(), rec.ProfileID); err == nil {
					row.Profile = p
				}
			}
			in.Rows = append(in.Rows, row)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.ModelHomologationsPage(in).Render(r.Context(), w)
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
