package voalle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
)

// fakeOAuthAndAPI monta um httptest server que responde tanto /oauth/token
// quanto endpoints de API com um simples switch.
func fakeOAuthAndAPI(t *testing.T, customers []map[string]any) (*httptest.Server, *int) {
	t.Helper()
	tokenCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if r.PostForm.Get("grant_type") != "client_credentials" {
				t.Errorf("grant_type incorreto: %q", r.PostForm.Get("grant_type"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fake-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v1/customers":
			if r.Header.Get("Authorization") != "Bearer fake-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			page, _ := strDefault(r.URL.Query().Get("page"), "1"), 0
			_ = page
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": customers,
				"meta": map[string]any{
					"total":    len(customers),
					"page":     1,
					"per_page": 100,
					"has_more": false,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &tokenCalls
}

func strDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func TestSyncCustomersHappyPath(t *testing.T) {
	customers := []map[string]any{
		{
			"id":          "1001",
			"nome":        "João da Silva",
			"documento":   "12345678901",
			"pppoe_login": "joao123",
			"plano":       "100 Mega Fibra",
			"endereco":    "Rua A, 123",
			"status":      "ativo",
		},
		{
			"id":          "1002",
			"nome":        "Maria Santos",
			"documento":   "98765432100",
			"pppoe_login": "maria456",
			"plano":       "300 Mega Pro",
			"endereco":    "Av B, 456",
			"status":      "suspenso",
		},
	}
	srv, _ := fakeOAuthAndAPI(t, customers)
	defer srv.Close()

	p := New(Config{
		BaseURL:       srv.URL,
		ClientID:      "id",
		ClientSecret:  "sec",
		Timeout:       5e9,
		TokenPath:     "/oauth/token",
		CustomersPath: "/api/v1/customers",
		Schema:        defaultSchema(),
	})

	res, err := p.SyncCustomers(context.Background(), erp.SyncOptions{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Customers) != 2 {
		t.Fatalf("esperava 2 customers, got %d", len(res.Customers))
	}
	c := res.Customers[0]
	if c.ExternalID != "1001" || c.FullName != "João da Silva" {
		t.Errorf("customer 0 inesperado: %+v", c)
	}
	if c.Status != "active" {
		t.Errorf("status normalização falhou: got %q", c.Status)
	}
	if res.Customers[1].Status != "suspended" {
		t.Errorf("status normalização (suspenso): got %q", res.Customers[1].Status)
	}
	if res.HasMore {
		t.Error("não deveria ter HasMore com meta.has_more=false")
	}
}

func TestTokenIsCached(t *testing.T) {
	customers := []map[string]any{
		{"id": "1", "nome": "X", "status": "ativo"},
	}
	srv, tokenCalls := fakeOAuthAndAPI(t, customers)
	defer srv.Close()

	p := New(Config{
		BaseURL:       srv.URL,
		ClientID:      "id",
		ClientSecret:  "sec",
		Timeout:       5e9,
		TokenPath:     "/oauth/token",
		CustomersPath: "/api/v1/customers",
		Schema:        defaultSchema(),
	})

	for i := 0; i < 5; i++ {
		if _, err := p.SyncCustomers(context.Background(), erp.SyncOptions{}); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	if *tokenCalls != 1 {
		t.Errorf("esperava 1 chamada de OAuth (token cacheado), got %d", *tokenCalls)
	}
}

func TestProviderInfo(t *testing.T) {
	p := New(Config{BaseURL: "http://x", ClientID: "a", ClientSecret: "b", Schema: defaultSchema()})
	info := p.Info()
	if info.Slug != "voalle" {
		t.Errorf("slug: %q", info.Slug)
	}
	if !info.Has(erp.CapSyncCustomers) {
		t.Error("deveria declarar CapSyncCustomers")
	}
	if info.Has(erp.CapBlockCustomer) {
		t.Error("read-only não deveria declarar block")
	}
}

func TestUnsupportedCapabilities(t *testing.T) {
	p := New(Config{BaseURL: "http://x", ClientID: "a", ClientSecret: "b", Schema: defaultSchema()})
	if err := p.BlockCustomer(context.Background(), "1", "any"); !errors.Is(err, erp.ErrCapabilityUnsupported) {
		t.Errorf("Block: got %v", err)
	}
	if err := p.UnblockCustomer(context.Background(), "1"); !errors.Is(err, erp.ErrCapabilityUnsupported) {
		t.Errorf("Unblock: got %v", err)
	}
	if _, err := p.HandleWebhook(context.Background(), nil, nil); !errors.Is(err, erp.ErrCapabilityUnsupported) {
		t.Errorf("Webhook: got %v", err)
	}
}

func TestParseConfigValidation(t *testing.T) {
	cases := map[string]map[string]any{
		"sem base_url":      {"client_id": "x", "client_secret": "y"},
		"sem client_id":     {"base_url": "https://x", "client_secret": "y"},
		"sem client_secret": {"base_url": "https://x", "client_id": "y"},
		"timeout inválido":  {"base_url": "https://x", "client_id": "x", "client_secret": "y", "timeout": "abc"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfig(raw); err == nil {
				t.Fatal("esperava erro de validação")
			}
		})
	}
}

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"base_url":      "https://api.voalle.com.br/  ",
		"client_id":     "x",
		"client_secret": "y",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.HasSuffix(cfg.BaseURL, "voalle.com.br") {
		t.Errorf("base_url não trimado: %q", cfg.BaseURL)
	}
	if cfg.TokenPath != "/oauth/token" {
		t.Errorf("token_path default errado: %q", cfg.TokenPath)
	}
}
