package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsCanonicalKeysHandler — CRUD do catálogo padronizado de canonical_keys.
// Permissão: leitura para template.read; mutações para template.manage.
type SettingsCanonicalKeysHandler struct {
	Keys hom.CanonicalKeyRepo
}

// List GET /settings/canonical-keys
func (h *SettingsCanonicalKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	keys, err := h.Keys.List(r.Context(), "")
	if err != nil {
		logger.FromContext(r.Context()).Error("list canonical keys", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.CanonicalKeysList(csrf, keys, "").Render(r.Context(), w)
}

// Create POST /settings/canonical-keys — adiciona chave nova ao catálogo.
// Hint paths são whitespace-separated (uma por linha no textarea).
func (h *SettingsCanonicalKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	dt := tmpl.DataType(r.PostForm.Get("data_type"))
	if !dt.Valid() {
		dt = tmpl.DataTypeString
	}
	cat := strings.TrimSpace(r.PostForm.Get("category"))
	if cat == "" {
		cat = hom.CategoryOther
	}
	k := &hom.CanonicalKey{
		Key:               strings.TrimSpace(r.PostForm.Get("key")),
		LabelPT:           strings.TrimSpace(r.PostForm.Get("label_pt")),
		Description:       strings.TrimSpace(r.PostForm.Get("description")),
		Category:          cat,
		SuggestedDataType: dt,
		DefaultIsSecret:   r.PostForm.Get("default_is_secret") == "1",
		HintPathsTR098:    splitLines(r.PostForm.Get("hint_paths_tr098")),
		HintPathsTR181:    splitLines(r.PostForm.Get("hint_paths_tr181")),
	}
	if k.Key == "" || k.LabelPT == "" {
		h.renderListErr(w, r, "Key e label são obrigatórios.")
		return
	}
	if err := h.Keys.Create(r.Context(), k); err != nil {
		logger.FromContext(r.Context()).Error("create canonical key", "err", err)
		h.renderListErr(w, r, err.Error())
		return
	}
	http.Redirect(w, r, "/settings/canonical-keys", http.StatusSeeOther)
}

// Delete POST /settings/canonical-keys/{id}/delete — remove do catálogo.
// Não toca em mappings ou profiles existentes (canonical_key não tem FK forte).
func (h *SettingsCanonicalKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := h.Keys.Delete(r.Context(), id); err != nil {
		if errors.Is(err, hom.ErrCanonicalKeyNotFound) {
			http.NotFound(w, r)
			return
		}
		logger.FromContext(r.Context()).Error("delete canonical key", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/canonical-keys", http.StatusSeeOther)
}

func (h *SettingsCanonicalKeysHandler) renderListErr(w http.ResponseWriter, r *http.Request, msg string) {
	keys, _ := h.Keys.List(r.Context(), "")
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = settingspages.CanonicalKeysList(csrf, keys, msg).Render(r.Context(), w)
}

// splitLines parte o textarea em linhas, descarta vazias e trim.
// Aceita separadores \n, \r\n e ; (mais flexível para o operador).
func splitLines(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	rep := strings.NewReplacer("\r\n", "\n", "\r", "\n", ";", "\n")
	parts := strings.Split(rep.Replace(s), "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
