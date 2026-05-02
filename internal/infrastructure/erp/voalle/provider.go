// Package voalle implementa o plugin ERP Voalle para o SentinelACS.
//
// Esta primeira versão é READ-ONLY (Fase 2.5):
//   - Info, HealthCheck
//   - SyncCustomers (paginado, page-based)
//   - GetCustomerByID
//
// Block/Unblock + Webhook entram na Fase 6 (Voalle Completo).
//
// Configuração esperada (do registry):
//
//	{
//	  "base_url":      "https://api.voalle.cliente.com.br",
//	  "client_id":     "...",
//	  "client_secret": "...",
//	  "timeout":       "30s",
//	  "token_path":    "/oauth/token",       // opcional
//	  "customers_path":"/api/v1/customers",  // opcional
//	}
package voalle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
)

// Slug do plugin no registry. Use erp.New("voalle", cfg) para instanciar.
const Slug = "voalle"

// init registra o plugin. Caller (cmd/server, cmd/worker) precisa fazer
// blank import deste pacote para o init rodar:
//
//	import _ "github.com/celinet/sentinel-acs/internal/infrastructure/erp/voalle"
func init() {
	erp.Register(Slug, func(raw map[string]any) (erp.Provider, error) {
		cfg, err := parseConfig(raw)
		if err != nil {
			return nil, err
		}
		return New(cfg), nil
	})
}

// Provider implementa erp.Provider para Voalle (read-only).
type Provider struct {
	cfg    Config
	tokens *tokenManager
	client *http.Client
}

// New permite construir o provider direto (sem registry) — útil para testes.
func New(cfg Config) *Provider {
	return &Provider{
		cfg:    cfg,
		tokens: newTokenManager(cfg),
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (p *Provider) Info() erp.ProviderInfo {
	return erp.ProviderInfo{
		Slug:        Slug,
		DisplayName: "Voalle",
		Version:     "1.0.0-readonly",
		Author:      "Celinet",
		Capabilities: []erp.Capability{
			erp.CapSyncCustomers,
		},
	}
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	_, err := p.tokens.Token(ctx)
	return err
}

// SyncCustomers itera o endpoint paginado do Voalle. Page-based — converte
// o Cursor opaco para número de página.
func (p *Provider) SyncCustomers(ctx context.Context, opts erp.SyncOptions) (*erp.SyncResult, error) {
	page := 1
	if opts.Pagination != nil && opts.Pagination.Token != "" {
		if n, err := strconv.Atoi(opts.Pagination.Token); err == nil && n > 0 {
			page = n
		}
	}
	pageSize := opts.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 100
	}

	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(pageSize))
	if opts.Since != nil {
		q.Set("modified_since", opts.Since.UTC().Format(time.RFC3339))
	}

	endpoint := p.cfg.BaseURL + p.cfg.CustomersPath + "?" + q.Encode()
	resp, body, err := p.doAuthorized(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Parse genérico: aceita {data: [...], meta: {...}} OU [...] direto.
	var raw struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Total      int  `json:"total"`
			Page       int  `json:"page"`
			PerPage    int  `json:"per_page"`
			HasMore    bool `json:"has_more"`
			TotalPages int  `json:"total_pages"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		// Tenta como array no topo
		var asArray []map[string]any
		if err2 := json.Unmarshal(body, &asArray); err2 == nil {
			raw.Data = asArray
		} else {
			return nil, fmt.Errorf("voalle: parse customers: %w (body: %s)", err, snippet(body))
		}
	}

	customers := make([]erp.Customer, 0, len(raw.Data))
	for _, item := range raw.Data {
		customers = append(customers, p.mapCustomer(item))
	}

	hasMore := raw.Meta.HasMore
	if !hasMore && raw.Meta.TotalPages > 0 {
		hasMore = page < raw.Meta.TotalPages
	}
	if !hasMore && len(raw.Data) == pageSize {
		// Sem meta confiável e a página veio cheia — assume que pode ter mais.
		hasMore = true
	}

	var nextCursor *erp.Cursor
	if hasMore {
		nextCursor = &erp.Cursor{Token: strconv.Itoa(page + 1)}
	}

	return &erp.SyncResult{
		Customers:  customers,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (p *Provider) GetCustomerByID(ctx context.Context, externalID string) (*erp.Customer, error) {
	endpoint := p.cfg.BaseURL + p.cfg.CustomersPath + "/" + url.PathEscape(externalID)
	resp, body, err := p.doAuthorized(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, erp.ErrCustomerNotFound
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("voalle: parse customer: %w", err)
	}
	// Algumas APIs retornam {"data": {...}}; aceita ambos.
	if d, ok := raw["data"].(map[string]any); ok {
		raw = d
	}
	c := p.mapCustomer(raw)
	return &c, nil
}

// BlockCustomer não suportado nesta versão read-only.
func (p *Provider) BlockCustomer(ctx context.Context, externalID, reason string) error {
	return erp.ErrCapabilityUnsupported
}

func (p *Provider) UnblockCustomer(ctx context.Context, externalID string) error {
	return erp.ErrCapabilityUnsupported
}

func (p *Provider) HandleWebhook(ctx context.Context, payload []byte, headers map[string]string) (*erp.WebhookEvent, error) {
	return nil, erp.ErrCapabilityUnsupported
}

// ──────────────── internals ────────────────

func (p *Provider) doAuthorized(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, []byte, error) {
	token, err := p.tokens.Token(ctx)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, nil, fmt.Errorf("voalle: build req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("voalle: do %s: %w", method, err)
	}

	// 401 → invalida token e tenta uma vez mais.
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		p.tokens.Invalidate()

		token, err = p.tokens.Token(ctx)
		if err != nil {
			return nil, nil, err
		}
		req2, _ := http.NewRequestWithContext(ctx, method, endpoint, body)
		req2.Header = req.Header.Clone()
		req2.Header.Set("Authorization", "Bearer "+token)
		resp, err = p.client.Do(req2)
		if err != nil {
			return nil, nil, fmt.Errorf("voalle: retry: %w", err)
		}
	}

	bs, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return resp, bs, fmt.Errorf("voalle %s %s: status %d: %s",
			method, endpoint, resp.StatusCode, snippet(bs))
	}
	return resp, bs, nil
}

// mapCustomer extrai campos canônicos do JSON do Voalle.
// Usa CustomerSchema para resolver nomes de campos divergentes.
func (p *Provider) mapCustomer(raw map[string]any) erp.Customer {
	s := p.cfg.Schema
	c := erp.Customer{
		ExternalID: getPathStr(raw, s.ID),
		FullName:   getPathStr(raw, s.FullName),
		Document:   getPathStr(raw, s.Document),
		PPPoELogin: getPathStr(raw, s.PPPoELogin),
		PlanName:   getPathStr(raw, s.Plan),
		Address:    getPathStr(raw, s.Address),
		Status:     normalizeStatus(getPathStr(raw, s.Status), s.StatusMap),
		Metadata:   raw,
	}
	if c.Status == "" {
		c.Status = "active" // default se ERP não mandar status
	}
	return c
}

// getPathStr lê dot-path do JSON (suporta "contrato.plano.nome").
func getPathStr(raw map[string]any, path string) string {
	if path == "" || raw == nil {
		return ""
	}
	parts := strings.Split(path, ".")
	var cur any = raw
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	switch v := cur.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func normalizeStatus(raw string, statusMap map[string]string) string {
	if raw == "" {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(raw))
	if v, ok := statusMap[key]; ok {
		return v
	}
	return key
}

func snippet(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
