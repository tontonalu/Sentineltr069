package handlers

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsTR069ACLHandler — CRUD da lista de CIDRs autorizados na 7547.
type SettingsTR069ACLHandler struct {
	ACL prov.ACLRepository
}

// Show GET /settings/tr069-acl
func (h *SettingsTR069ACLHandler) Show(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "")
}

// Create POST /settings/tr069-acl — adiciona CIDR.
func (h *SettingsTR069ACLHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	cidrStr := strings.TrimSpace(r.PostFormValue("cidr"))
	desc := strings.TrimSpace(r.PostFormValue("description"))

	prefix, err := netip.ParsePrefix(cidrStr)
	if err != nil {
		h.render(w, r, "CIDR inválido (ex.: 192.168.0.0/24)")
		return
	}
	// Normaliza para o range — descarta bits de host. Ex.: 10.0.0.5/24 → 10.0.0.0/24.
	prefix = prefix.Masked()

	entry := &prov.ACLCIDR{CIDR: prefix, Description: desc}
	if user, ok := mw.UserFromContext(r.Context()); ok {
		entry.CreatedBy = &user.ID
	}

	if err := h.ACL.Create(r.Context(), entry); err != nil {
		if errors.Is(err, prov.ErrCIDRDuplicate) {
			h.render(w, r, "Esse CIDR já está cadastrado")
			return
		}
		logger.FromContext(r.Context()).Error("create acl cidr", "err", err)
		h.render(w, r, "Erro ao salvar: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings/tr069-acl?saved=1", http.StatusSeeOther)
}

// Delete POST /settings/tr069-acl/{id}/delete
func (h *SettingsTR069ACLHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := h.ACL.Delete(r.Context(), id); err != nil {
		if errors.Is(err, prov.ErrCIDRNotFound) {
			http.NotFound(w, r)
			return
		}
		logger.FromContext(r.Context()).Error("delete acl cidr", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/tr069-acl?deleted=1", http.StatusSeeOther)
}

func (h *SettingsTR069ACLHandler) render(w http.ResponseWriter, r *http.Request, errMsg string) {
	entries, err := h.ACL.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list acl cidrs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Flash via query string. Create redireciona com ?saved=1; Delete com ?deleted=1.
	var okMsg string
	if r.URL.Query().Get("saved") == "1" {
		okMsg = "CIDR adicionado."
	} else if r.URL.Query().Get("deleted") == "1" {
		okMsg = "CIDR removido."
	}

	canManage := mw.UserHasPermission(r.Context(), "tr069_acl", "manage")
	csrf := mw.CSRFTokenFromContext(r.Context())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = settingspages.TR069ACLPage(csrf, entries, canManage, errMsg, okMsg).Render(r.Context(), w)
}
