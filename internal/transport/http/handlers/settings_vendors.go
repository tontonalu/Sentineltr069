package handlers

import (
	"errors"
	"net/http"
	"strings"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsVendorsHandler — CRUD de vendors em /settings/vendors.
// O domínio só expõe Create/List (sem Update), então a UI é list + create.
type SettingsVendorsHandler struct {
	Vendors domain.VendorRepository
}

// List GET /settings/vendors
func (h *SettingsVendorsHandler) List(w http.ResponseWriter, r *http.Request) {
	vendors, err := h.Vendors.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list vendors failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.VendorsList(vendors).Render(r.Context(), w)
}

// NewForm GET /settings/vendors/new
func (h *SettingsVendorsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.VendorNewPage(csrf, settingspages.VendorDraft{}, "").Render(r.Context(), w)
}

// Create POST /settings/vendors
func (h *SettingsVendorsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	draft := settingspages.VendorDraft{
		Slug: strings.TrimSpace(strings.ToLower(r.PostFormValue("slug"))),
		Name: strings.TrimSpace(r.PostFormValue("name")),
	}
	if draft.Slug == "" || draft.Name == "" {
		h.renderNewWithError(w, r, draft, "Slug e nome são obrigatórios.")
		return
	}
	v := domain.Vendor{Slug: draft.Slug, Name: draft.Name}
	if err := h.Vendors.Create(r.Context(), &v); err != nil {
		msg := "erro ao cadastrar vendor."
		if errors.Is(err, domain.ErrSlugDuplicate) {
			msg = "Slug ou nome já cadastrado."
		} else {
			msg = err.Error()
		}
		logger.FromContext(r.Context()).Info("create vendor failed", "slug", draft.Slug, "err", err)
		h.renderNewWithError(w, r, draft, msg)
		return
	}
	http.Redirect(w, r, "/settings/vendors", http.StatusSeeOther)
}

func (h *SettingsVendorsHandler) renderNewWithError(w http.ResponseWriter, r *http.Request, draft settingspages.VendorDraft, msg string) {
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = settingspages.VendorNewPage(csrf, draft, msg).Render(r.Context(), w)
}
