// Package whatsapp implementa o adapter para a Evolution API (self-hosted).
//
// Evolution API expõe um endpoint REST por instância:
//
//	POST {base_url}/message/sendText/{instance}
//	Headers: apikey: <token>
//	Body: { "number": "5579999990000", "text": "..." }
//
// Documentação: https://doc.evolution-api.com/v2
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
)

// Config — credenciais da Evolution. Senha/token cifrado em repouso é
// responsabilidade do `crypto.SecretBox` na camada de config.
type Config struct {
	BaseURL  string // ex: "https://evolution.minhaempresa.com.br"
	APIKey   string
	Instance string // nome da instância configurada na Evolution
}

func (c Config) Enabled() bool {
	return c.BaseURL != "" && c.APIKey != "" && c.Instance != ""
}

// Notifier — implementa application.alerting.Notifier.
type Notifier struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Notifier {
	return &Notifier{
		cfg:  cfg,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// Type retorna o ChannelType canônico.
func (n *Notifier) Type() domain.ChannelType { return domain.ChannelWhatsApp }

// Send envia para um número (formato E.164 sem '+', ex: 5579999990000).
// Subject é prefixado no body — Evolution não suporta cabeçalho próprio.
func (n *Notifier) Send(ctx context.Context, target, subject, body string) error {
	if !n.cfg.Enabled() {
		return domain.ErrChannelDisabled
	}
	number := normalizeNumber(target)
	if number == "" {
		return fmt.Errorf("whatsapp: target inválido: %q", target)
	}

	text := body
	if subject != "" {
		text = "*" + subject + "*\n\n" + body
	}

	payload := map[string]any{
		"number": number,
		"text":   text,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := strings.TrimRight(n.cfg.BaseURL, "/") + "/message/sendText/" + n.cfg.Instance
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", n.cfg.APIKey)

	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("whatsapp: status %d", resp.StatusCode)
	}
	return nil
}

// normalizeNumber tira "+", espaços, hífens — Evolution aceita só dígitos.
func normalizeNumber(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
