package handlers

import (
	"net/http"
	"strconv"
	"strings"

	provapp "github.com/celinet/sentinel-acs/internal/application/provisioning"
	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	settingspages "github.com/celinet/sentinel-acs/internal/views/pages/settings"
)

// SettingsProvisioningHandler — GET/POST/Sync de /settings/provisioning.
type SettingsProvisioningHandler struct {
	Configs prov.ConfigRepository
	Syncer  *provapp.Syncer
}

// Show GET /settings/provisioning
func (h *SettingsProvisioningHandler) Show(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.Configs.Get(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("get provisioning config failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	canManage := mw.UserHasPermission(r.Context(), "provisioning_config", "manage")
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingspages.ProvisioningPage(csrf, draftFromConfig(cfg), cfg, canManage, "", "").Render(r.Context(), w)
}

// Update POST /settings/provisioning
func (h *SettingsProvisioningHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	draft := draftFromForm(r)
	cfg, err := h.Configs.Get(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("get provisioning config failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	cfg.CWMPUrl = draft.CWMPUrl
	cfg.InformIntervalS = draft.InformIntervalS
	cfg.DefaultCRUser = draft.DefaultCRUser
	cfg.DefaultCRPass = draft.DefaultCRPass
	cfg.PresetName = draft.PresetName
	if user, ok := mw.UserFromContext(r.Context()); ok {
		cfg.UpdatedBy = &user.ID
	}

	if err := cfg.Validate(); err != nil {
		h.renderWithError(w, r, draft, cfg, err.Error(), "")
		return
	}
	if err := h.Configs.Update(r.Context(), cfg); err != nil {
		logger.FromContext(r.Context()).Error("update provisioning config failed", "err", err)
		h.renderWithError(w, r, draft, cfg, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/settings/provisioning", http.StatusSeeOther)
}

// Sync POST /settings/provisioning/sync — aplica config no GenieACS.
func (h *SettingsProvisioningHandler) Sync(w http.ResponseWriter, r *http.Request) {
	syncErr := h.Syncer.Sync(r.Context())

	cfg, err := h.Configs.Get(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("reload provisioning config failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	canManage := mw.UserHasPermission(r.Context(), "provisioning_config", "manage")
	csrf := mw.CSRFTokenFromContext(r.Context())

	var errMsg, okMsg string
	if syncErr != nil {
		errMsg = "Falha ao sincronizar com ACS upstream: " + syncErr.Error()
		logger.FromContext(r.Context()).Warn("upstream sync failed", "err", syncErr)
	} else {
		okMsg = "Sincronizado com sucesso. Próximo Inform de cada CPE aplica os parâmetros."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if syncErr != nil {
		w.WriteHeader(http.StatusBadGateway)
	}
	_ = settingspages.ProvisioningPage(csrf, draftFromConfig(cfg), cfg, canManage, errMsg, okMsg).Render(r.Context(), w)
}

func (h *SettingsProvisioningHandler) renderWithError(w http.ResponseWriter, r *http.Request, draft settingspages.ProvisioningDraft, cfg *prov.Config, errMsg, okMsg string) {
	canManage := mw.UserHasPermission(r.Context(), "provisioning_config", "manage")
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = settingspages.ProvisioningPage(csrf, draft, cfg, canManage, errMsg, okMsg).Render(r.Context(), w)
}

func draftFromConfig(c *prov.Config) settingspages.ProvisioningDraft {
	return settingspages.ProvisioningDraft{
		CWMPUrl:         c.CWMPUrl,
		InformIntervalS: c.InformIntervalS,
		DefaultCRUser:   c.DefaultCRUser,
		DefaultCRPass:   c.DefaultCRPass,
		PresetName:      c.PresetName,
	}
}

func draftFromForm(r *http.Request) settingspages.ProvisioningDraft {
	interval, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("inform_interval_s")))
	return settingspages.ProvisioningDraft{
		CWMPUrl:         strings.TrimSpace(r.PostFormValue("cwmp_url")),
		InformIntervalS: interval,
		DefaultCRUser:   strings.TrimSpace(r.PostFormValue("default_cr_user")),
		DefaultCRPass:   r.PostFormValue("default_cr_pass"),
		PresetName:      strings.TrimSpace(r.PostFormValue("preset_name")),
	}
}
