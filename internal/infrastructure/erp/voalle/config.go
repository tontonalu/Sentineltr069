package voalle

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config são os parâmetros do plugin Voalle.
//
// CustomerSchema permite ajustar nomes de campos do JSON do Voalle sem
// recompilar — útil porque versões/contratos diferentes às vezes mudam
// nomenclatura (id vs codigo, nome vs full_name, etc).
type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	Timeout      time.Duration

	// Endpoints — defaults razoáveis, ajustar se a instância usar paths diferentes.
	TokenPath     string // default: "/oauth/token"
	CustomersPath string // default: "/api/v1/customers"

	// Mapeamento de campos do JSON do Voalle para nosso modelo canônico.
	// Suporta dot-notation pra navegar objetos (ex: "contrato.plano.nome").
	Schema CustomerSchema
}

// CustomerSchema define onde buscar cada campo dentro do JSON de cliente.
// Campos vazios são pulados.
type CustomerSchema struct {
	ID         string // default: "id"
	FullName   string // default: "nome"
	Document   string // default: "documento"
	PPPoELogin string // default: "pppoe_login"
	Plan       string // default: "plano"
	Address    string // default: "endereco"
	Status     string // default: "status"

	// Mapeia status do Voalle para nosso vocabulário.
	// Default: {"ativo":"active","suspenso":"suspended","cancelado":"cancelled"}
	StatusMap map[string]string
}

func defaultSchema() CustomerSchema {
	return CustomerSchema{
		ID:         "id",
		FullName:   "nome",
		Document:   "documento",
		PPPoELogin: "pppoe_login",
		Plan:       "plano",
		Address:    "endereco",
		Status:     "status",
		StatusMap: map[string]string{
			"ativo":      "active",
			"active":     "active",
			"suspenso":   "suspended",
			"suspended":  "suspended",
			"cancelado":  "cancelled",
			"cancelled":  "cancelled",
			"desativado": "cancelled",
		},
	}
}

// parseConfig aceita o map vindo do registry e valida.
func parseConfig(raw map[string]any) (Config, error) {
	cfg := Config{
		Timeout:       30 * time.Second,
		TokenPath:     "/oauth/token",
		CustomersPath: "/api/v1/customers",
		Schema:        defaultSchema(),
	}

	if v, _ := raw["base_url"].(string); v != "" {
		cfg.BaseURL = strings.TrimRight(v, "/")
	}
	if v, _ := raw["client_id"].(string); v != "" {
		cfg.ClientID = v
	}
	if v, _ := raw["client_secret"].(string); v != "" {
		cfg.ClientSecret = v
	}
	if v, _ := raw["timeout"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("voalle: timeout inválido %q: %w", v, err)
		}
		cfg.Timeout = d
	}
	if v, _ := raw["token_path"].(string); v != "" {
		cfg.TokenPath = v
	}
	if v, _ := raw["customers_path"].(string); v != "" {
		cfg.CustomersPath = v
	}

	if cfg.BaseURL == "" {
		return cfg, errors.New("voalle: base_url obrigatório")
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return cfg, errors.New("voalle: client_id e client_secret obrigatórios")
	}
	return cfg, nil
}
