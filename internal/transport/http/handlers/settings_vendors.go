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

// SettingsVendorsHandler — CRUD de vendors em /settings/vendors.
// Suporta create + edit (rename) — sem delete por ora porque devices/models
// podem depender via FK, e o cleanup correto exige ferramenta separada.
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
		msg := err.Error()
		if errors.Is(err, domain.ErrSlugDuplicate) {
			msg = "Slug ou nome já cadastrado."
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

// EditForm GET /settings/vendors/{id}/edit — formulário de renomear vendor.
//
// Caso de uso típico: o sync auto-cadastrou um vendor pelo nome do chipset
// (ex.: "Realtek" para uma ONT V-SOL), e o operador quer corrigir para o
// brand real. Slug é re-derivado do nome no submit, então renomear "Realtek"
// para "V-SOL" também ajusta o slug — futuros syncs do mesmo device, agora
// com OUI override aplicado, encontram o vendor renomeado pelo slug "v-sol".
func (h *SettingsVendorsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	v, err := h.Vendors.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrVendorNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.VendorEditPage(csrf, *v, "").Render(r.Context(), w)
}

// Update POST /settings/vendors/{id} — aplica o rename.
func (h *SettingsVendorsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	cur, err := h.Vendors.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrVendorNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("get vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		h.renderEditWithError(w, r, *cur, "Nome é obrigatório.")
		return
	}
	cur.Name = name
	cur.Slug = slugifyVendor(name)
	if err := h.Vendors.Update(r.Context(), cur); err != nil {
		msg := err.Error()
		if errors.Is(err, domain.ErrSlugDuplicate) {
			msg = "Outro vendor já usa este nome (slug colide)."
		}
		logger.FromContext(r.Context()).Info("update vendor failed", "id", id, "err", err)
		h.renderEditWithError(w, r, *cur, msg)
		return
	}
	http.Redirect(w, r, "/settings/vendors", http.StatusSeeOther)
}

func (h *SettingsVendorsHandler) renderEditWithError(w http.ResponseWriter, r *http.Request, v domain.Vendor, msg string) {
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = settingspages.VendorEditPage(csrf, v, msg).Render(r.Context(), w)
}

// slugifyVendor é o mesmo algoritmo do sync.slugify mas inline para evitar
// import circular do package application/inventory aqui em transport/http.
// Mantém: lowercase, alfanumérico + "-", trim de "-" nas pontas, "unknown"
// como fallback.
func slugifyVendor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prev := byte('-')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			prev = c
		case c == '-' || c == ' ' || c == '_' || c == '.':
			if prev != '-' {
				b.WriteByte('-')
				prev = '-'
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "unknown"
	}
	return out
}
