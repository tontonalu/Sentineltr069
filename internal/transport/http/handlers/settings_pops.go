package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsPOPsHandler — CRUD de POPs em /settings/pops.
type SettingsPOPsHandler struct {
	POPs domain.POPRepository
}

// List GET /settings/pops
func (h *SettingsPOPsHandler) List(w http.ResponseWriter, r *http.Request) {
	pops, err := h.POPs.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list pops failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.POPsList(csrf, pops).Render(r.Context(), w)
}

// NewForm GET /settings/pops/new
func (h *SettingsPOPsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.POPNewPage(csrf, settingspages.POPDraft{IsActive: true}, "").Render(r.Context(), w)
}

// Create POST /settings/pops
func (h *SettingsPOPsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	draft := popDraftFromForm(r)
	if draft.Name == "" {
		h.renderNewWithError(w, r, draft, "Nome é obrigatório.")
		return
	}
	pop := domain.POP{
		Name:     draft.Name,
		City:     draft.City,
		State:    draft.State,
		IsActive: draft.IsActive,
	}
	if err := h.POPs.Create(r.Context(), &pop); err != nil {
		logger.FromContext(r.Context()).Info("create pop failed", "err", err)
		h.renderNewWithError(w, r, draft, err.Error())
		return
	}
	http.Redirect(w, r, "/settings/pops", http.StatusSeeOther)
}

// EditForm GET /settings/pops/{id}/edit
func (h *SettingsPOPsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	pop, err := h.POPs.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrPOPNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get pop failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	draft := settingspages.POPDraft{
		Name:     pop.Name,
		City:     pop.City,
		State:    pop.State,
		IsActive: pop.IsActive,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.POPEditPage(csrf, pop.ID, draft, "").Render(r.Context(), w)
}

// Update POST /settings/pops/{id}
func (h *SettingsPOPsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	pop, err := h.POPs.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrPOPNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get pop failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	draft := popDraftFromForm(r)
	if draft.Name == "" {
		csrf := mw.CSRFTokenFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = settingspages.POPEditPage(csrf, id, draft, "Nome é obrigatório.").Render(r.Context(), w)
		return
	}

	pop.Name = draft.Name
	pop.City = draft.City
	pop.State = draft.State
	pop.IsActive = draft.IsActive
	if err := h.POPs.Update(r.Context(), pop); err != nil {
		logger.FromContext(r.Context()).Error("update pop failed", "err", err)
		csrf := mw.CSRFTokenFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_ = settingspages.POPEditPage(csrf, id, draft, err.Error()).Render(r.Context(), w)
		return
	}
	http.Redirect(w, r, "/settings/pops", http.StatusSeeOther)
}

// ToggleActive POST /settings/pops/{id}/toggle — alterna IsActive e devolve a row.
func (h *SettingsPOPsHandler) ToggleActive(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	pop, err := h.POPs.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrPOPNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get pop failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.POPs.SetActive(r.Context(), id, !pop.IsActive); err != nil {
		logger.FromContext(r.Context()).Error("set pop active failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	updated, err := h.POPs.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.POPRow(csrf, *updated).Render(r.Context(), w)
}

func (h *SettingsPOPsHandler) renderNewWithError(w http.ResponseWriter, r *http.Request, draft settingspages.POPDraft, msg string) {
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = settingspages.POPNewPage(csrf, draft, msg).Render(r.Context(), w)
}

func popDraftFromForm(r *http.Request) settingspages.POPDraft {
	return settingspages.POPDraft{
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		City:     strings.TrimSpace(r.PostFormValue("city")),
		State:    strings.TrimSpace(r.PostFormValue("state")),
		IsActive: r.PostFormValue("is_active") == "on",
	}
}
