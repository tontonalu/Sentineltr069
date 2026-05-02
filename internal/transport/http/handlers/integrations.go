package handlers

import (
	"net/http"
	"net/url"

	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
	intgpages "github.com/celinet/sentinel-acs/internal/views/pages/integrations"
)

// IntegrationsHandler — listagem de plugins ERP registrados + status básico.
//
// Esta primeira versão mostra apenas info estática (config, capabilities).
// Histórico de syncs e botão "force sync" virão quando tivermos um
// StatusTracker compartilhado entre worker e server (Redis-backed).
type IntegrationsHandler struct {
	// EnabledPlugins é um set de slugs configurados via env (server lê config).
	// Quando vazio, mostra apenas que o plugin está registrado mas desabilitado.
	EnabledPlugins map[string]EnabledPlugin
}

// EnabledPlugin descreve um plugin habilitado pela config.
type EnabledPlugin struct {
	BaseURL      string
	SyncInterval string
}

// List GET /integrations
func (h *IntegrationsHandler) List(w http.ResponseWriter, r *http.Request) {
	slugs := erp.List()

	plugins := make([]intgpages.PluginInfo, 0, len(slugs))
	for _, slug := range slugs {
		// Tenta instanciar SOMENTE para obter Info() — se a config não estiver
		// completa, pulamos com info "desabilitado".
		var info erp.ProviderInfo
		var enabled bool
		var baseURL, syncInterval string

		if cfg, ok := h.EnabledPlugins[slug]; ok {
			enabled = true
			baseURL = maskBaseURL(cfg.BaseURL)
			syncInterval = cfg.SyncInterval

			// Constrói com config válida só pra ler Info().
			provider, err := erp.New(slug, map[string]any{
				"base_url":      cfg.BaseURL,
				"client_id":     "_dummy_",
				"client_secret": "_dummy_",
			})
			if err == nil {
				info = provider.Info()
			}
		} else {
			// Plugin registrado mas sem config — usa Info estático.
			// Provider precisaria de config válida para construir, então
			// não conseguimos chamar Info() sem hack. Mostra slug apenas.
			info = erp.ProviderInfo{Slug: slug, DisplayName: slug, Version: "?"}
		}

		plugins = append(plugins, intgpages.PluginInfo{
			Info:     info,
			Enabled:  enabled,
			BaseURL:  baseURL,
			SyncCron: syncInterval,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = intgpages.List(plugins).Render(r.Context(), w)
}

// maskBaseURL mostra apenas host (sem credenciais ou querystring) — defesa
// em profundidade pra não expor secrets na UI.
func maskBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
