// Package config carrega a configuração do SentinelACS via koanf.
//
// Ordem de precedência (maior para menor):
//   1. Variáveis de ambiente (mapeadas explicitamente em envBindings).
//   2. config.yaml na raiz do projeto, se existir.
//   3. defaults definidos em loadDefaults.
//
// Em produção, valores sensíveis (senhas, secrets) DEVEM vir de env — nunca
// commitar config.yaml com credenciais reais.
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	App      App      `koanf:"app"`
	Postgres Postgres `koanf:"postgres"`
	Redis    Redis    `koanf:"redis"`
	GenieACS GenieACS `koanf:"genieacs"`
	Voalle   Voalle   `koanf:"voalle"`
	Log      Log      `koanf:"log"`
}

type App struct {
	Env             string `koanf:"env"`
	Port            string `koanf:"port"`
	BaseURL         string `koanf:"base_url"`
	SessionSecret   string `koanf:"session_secret"`
	AgeKeyFile      string `koanf:"age_key_file"`
	ShutdownTimeout string `koanf:"shutdown_timeout"`
}

func (a App) Shutdown() time.Duration {
	if d, err := time.ParseDuration(a.ShutdownTimeout); err == nil {
		return d
	}
	return 15 * time.Second
}

type Postgres struct {
	URL string `koanf:"url"`
}

type Redis struct {
	URL string `koanf:"url"`
}

type GenieACS struct {
	NBIUrl   string `koanf:"nbi_url"`
	FSUrl    string `koanf:"fs_url"`
	AuthUser string `koanf:"auth_user"`
	AuthPass string `koanf:"auth_pass"`
}

// Voalle são os parâmetros do plugin ERP. Vazio = plugin desativado.
// Em produção: defina pelo menos BaseURL + ClientID + ClientSecret.
type Voalle struct {
	BaseURL      string `koanf:"base_url"`
	ClientID     string `koanf:"client_id"`
	ClientSecret string `koanf:"client_secret"`
	Timeout      string `koanf:"timeout"`        // ex: "30s"
	SyncInterval string `koanf:"sync_interval"`  // ex: "5m"
	TokenPath    string `koanf:"token_path"`     // override
	CustomersPath string `koanf:"customers_path"` // override
}

// Enabled diz se o plugin foi minimamente configurado.
func (v Voalle) Enabled() bool {
	return v.BaseURL != "" && v.ClientID != "" && v.ClientSecret != ""
}

// AsMap converte para o map[string]any que erp.New espera.
func (v Voalle) AsMap() map[string]any {
	out := map[string]any{
		"base_url":      v.BaseURL,
		"client_id":     v.ClientID,
		"client_secret": v.ClientSecret,
	}
	if v.Timeout != "" {
		out["timeout"] = v.Timeout
	}
	if v.TokenPath != "" {
		out["token_path"] = v.TokenPath
	}
	if v.CustomersPath != "" {
		out["customers_path"] = v.CustomersPath
	}
	return out
}

// SyncIntervalDuration parseia ou devolve 5min como default.
func (v Voalle) SyncIntervalDuration() time.Duration {
	if d, err := time.ParseDuration(v.SyncInterval); err == nil && d > 0 {
		return d
	}
	return 5 * time.Minute
}

type Log struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

// envBindings mapeia variáveis de ambiente → caminho koanf.
// Manter explícito é mais seguro do que auto-detectar (evita vazar env vars
// não relacionadas para o config).
var envBindings = map[string]string{
	"APP_ENV":             "app.env",
	"APP_PORT":            "app.port",
	"APP_BASE_URL":        "app.base_url",
	"APP_SESSION_SECRET":  "app.session_secret",
	"APP_AGE_KEY_FILE":    "app.age_key_file",
	"APP_SHUTDOWN":        "app.shutdown_timeout",
	"DATABASE_URL":        "postgres.url",
	"REDIS_URL":           "redis.url",
	"GENIEACS_NBI_URL":    "genieacs.nbi_url",
	"GENIEACS_FS_URL":     "genieacs.fs_url",
	"GENIEACS_AUTH_USER":  "genieacs.auth_user",
	"GENIEACS_AUTH_PASS":  "genieacs.auth_pass",
	"VOALLE_BASE_URL":     "voalle.base_url",
	"VOALLE_CLIENT_ID":    "voalle.client_id",
	"VOALLE_CLIENT_SECRET":"voalle.client_secret",
	"VOALLE_TIMEOUT":      "voalle.timeout",
	"VOALLE_SYNC_INTERVAL":"voalle.sync_interval",
	"LOG_LEVEL":           "log.level",
	"LOG_FORMAT":          "log.format",
}

func loadDefaults() map[string]any {
	return map[string]any{
		"app.env":              "development",
		"app.port":             "8080",
		"app.shutdown_timeout": "15s",
		"genieacs.nbi_url":     "http://genieacs-nbi:7557",
		"log.level":            "info",
		"log.format":           "json",
	}
}

// Load resolve a configuração final. Falha apenas em erros de parsing
// ou quando faltam campos obrigatórios em produção.
func Load() (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(loadDefaults(), "."), nil); err != nil {
		return nil, fmt.Errorf("config: defaults: %w", err)
	}

	if _, err := os.Stat("config.yaml"); err == nil {
		if err := k.Load(file.Provider("config.yaml"), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("config: ler config.yaml: %w", err)
		}
	}

	overrides := map[string]any{}
	for envVar, path := range envBindings {
		if v := os.Getenv(envVar); v != "" {
			overrides[path] = v
		}
	}
	if len(overrides) > 0 {
		if err := k.Load(confmap.Provider(overrides, "."), nil); err != nil {
			return nil, fmt.Errorf("config: env overrides: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validate(c *Config) error {
	if c.App.Env == "production" {
		if c.App.SessionSecret == "" {
			return fmt.Errorf("config: APP_SESSION_SECRET obrigatório em produção")
		}
		if c.Postgres.URL == "" {
			return fmt.Errorf("config: DATABASE_URL obrigatório em produção")
		}
		if c.Redis.URL == "" {
			return fmt.Errorf("config: REDIS_URL obrigatório em produção")
		}
	}
	return nil
}
