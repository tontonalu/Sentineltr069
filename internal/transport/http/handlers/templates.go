package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	tplpages "github.com/celinet/sentinel-acs/internal/views/pages/templates"
)

// TemplatesHandler — CRUD de profiles em /templates.
type TemplatesHandler struct {
	Service *tplapp.Service
	Profiles *postgres.ProfileRepo // só pra List + filtros (service não expõe)
	History  *postgres.ProfileHistoryRepo
	Vendors  domain.VendorRepository
	Models   domain.DeviceModelRepository
}

// List GET /templates
func (h *TemplatesHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := postgres.ProfileListFilter{
		Search:     strings.TrimSpace(q.Get("q")),
		ActiveOnly: q.Get("active") == "1",
	}
	if v := q.Get("vendor"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.VendorID = &id
		}
	}
	profiles, err := h.Profiles.List(r.Context(), f)
	if err != nil {
		logger.FromContext(r.Context()).Error("templates list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	vendors, _ := h.Vendors.List(r.Context())

	var models []domain.DeviceModel
	if f.VendorID != nil {
		models, _ = h.Models.ListByVendor(r.Context(), *f.VendorID)
	}

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tplpages.List(tplpages.ListInput{
		Profiles:   profiles,
		Vendors:    vendors,
		Models:     models,
		Search:     f.Search,
		VendorID:   q.Get("vendor"),
		ModelID:    q.Get("model"),
		ActiveOnly: f.ActiveOnly,
		CSRF:       csrf,
	}).Render(r.Context(), w)
}

// NewForm GET /templates/new
func (h *TemplatesHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	vendors, _ := h.Vendors.List(r.Context())
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tplpages.Form(tplpages.FormInput{
		CSRF:     csrf,
		IsActive: true,
		Vendors:  vendors,
	}).Render(r.Context(), w)
}

// Create POST /templates
func (h *TemplatesHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	in := parseFormToInput(r)
	user, _ := mw.UserFromContext(r.Context())
	createIn := tplapp.CreateInput{
		Name:        in.Name,
		Description: in.Description,
		VendorID:    parseUUIDPtr(in.VendorID),
		ModelID:     parseUUIDPtr(in.ModelID),
		IsActive:    in.IsActive,
		ChangeNote:  in.ChangeNote,
		Parameters:  in.Parameters,
	}
	if user != nil {
		createIn.CreatedBy = &user.ID
	}
	p, err := h.Service.Create(r.Context(), createIn)
	if err != nil {
		h.renderFormErr(w, r, in, err, "")
		return
	}
	http.Redirect(w, r, "/templates/"+p.ID.String(), http.StatusSeeOther)
}

// EditForm GET /templates/{id}/edit
func (h *TemplatesHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	p, err := h.Service.LoadFull(r.Context(), id)
	if errors.Is(err, tmpl.ErrProfileNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("templates edit load", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	vendors, _ := h.Vendors.List(r.Context())
	in := tplpages.FormInput{
		CSRF:        mw.CSRFTokenFromContext(r.Context()),
		ProfileID:   p.ID.String(),
		Name:        p.Name,
		Description: p.Description,
		IsActive:    p.IsActive,
		Version:     p.Version,
		Parameters:  p.Parameters,
		Vendors:     vendors,
	}
	if p.VendorID != nil {
		in.VendorID = p.VendorID.String()
	}
	if p.ModelID != nil {
		in.ModelID = p.ModelID.String()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tplpages.Form(in).Render(r.Context(), w)
}

// Update POST /templates/{id}
func (h *TemplatesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	in := parseFormToInput(r)
	in.ProfileID = id.String()
	user, _ := mw.UserFromContext(r.Context())
	updIn := tplapp.UpdateInput{
		ID:          id,
		Name:        in.Name,
		Description: in.Description,
		VendorID:    parseUUIDPtr(in.VendorID),
		ModelID:     parseUUIDPtr(in.ModelID),
		IsActive:    in.IsActive,
		Parameters:  in.Parameters,
		ChangeNote:  in.ChangeNote,
	}
	if user != nil {
		updIn.ChangedBy = &user.ID
	}
	p, err := h.Service.Update(r.Context(), updIn)
	if err != nil {
		h.renderFormErr(w, r, in, err, id.String())
		return
	}
	http.Redirect(w, r, "/templates/"+p.ID.String(), http.StatusSeeOther)
}

// Detail GET /templates/{id}
func (h *TemplatesHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	p, err := h.Service.LoadFull(r.Context(), id)
	if errors.Is(err, tmpl.ErrProfileNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("templates detail", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	hist, _ := h.History.ListByProfile(r.Context(), id, 20)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tplpages.Detail(tplpages.DetailInput{
		Profile: *p,
		History: hist,
		CSRF:    mw.CSRFTokenFromContext(r.Context()),
	}).Render(r.Context(), w)
}

// ──────────────── helpers ────────────────

func (h *TemplatesHandler) renderFormErr(w http.ResponseWriter, r *http.Request, in tplpages.FormInput, err error, profileID string) {
	vendors, _ := h.Vendors.List(r.Context())
	in.Vendors = vendors
	in.ProfileID = profileID
	in.CSRF = mw.CSRFTokenFromContext(r.Context())
	in.Errors = []string{err.Error()}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = tplpages.Form(in).Render(r.Context(), w)
}

// parseFormToInput reconstrói FormInput + Parameters a partir do POST.
// Parameters são campos array (param_canonical_key[], etc.) — o índice
// posicional liga as colunas.
func parseFormToInput(r *http.Request) tplpages.FormInput {
	keys := r.PostForm["param_canonical_key[]"]
	paths := r.PostForm["param_tr_path[]"]
	templates := r.PostForm["param_value_template[]"]
	types := r.PostForm["param_data_type[]"]

	// O checkbox secret usa value=índice — só os marcados aparecem.
	// Construímos set de índices para reconciliar com os arrays posicionais.
	secretSet := map[int]bool{}
	for _, idxStr := range r.PostForm["param_is_secret_idx[]"] {
		if n, err := strconv.Atoi(idxStr); err == nil {
			secretSet[n] = true
		}
	}

	params := make([]tmpl.Parameter, 0, len(keys))
	for i := range keys {
		dt := tmpl.DataTypeString
		if i < len(types) {
			dt = tmpl.DataType(types[i])
		}
		valTemplate := ""
		if i < len(templates) {
			valTemplate = templates[i]
		}
		trPath := ""
		if i < len(paths) {
			trPath = paths[i]
		}
		params = append(params, tmpl.Parameter{
			CanonicalKey:  strings.TrimSpace(keys[i]),
			TRPath:        strings.TrimSpace(trPath),
			ValueTemplate: valTemplate,
			DataType:      dt,
			IsSecret:      secretSet[i],
			SortOrder:     i + 1,
		})
	}

	return tplpages.FormInput{
		Name:        strings.TrimSpace(r.PostForm.Get("name")),
		Description: strings.TrimSpace(r.PostForm.Get("description")),
		VendorID:    r.PostForm.Get("vendor_id"),
		ModelID:     r.PostForm.Get("model_id"),
		IsActive:    r.PostForm.Get("is_active") == "1",
		ChangeNote:  strings.TrimSpace(r.PostForm.Get("change_note")),
		Parameters:  params,
	}
}

func parseUUIDPtr(s string) *uuid.UUID {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}


