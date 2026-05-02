// Package telegram implementa adapter para a Bot API oficial.
//
// Endpoint:
//
//	POST https://api.telegram.org/bot{TOKEN}/sendMessage
//	Body: { "chat_id": "-100...", "text": "...", "parse_mode": "Markdown" }
//
// Não usamos go-telegram/bot porque é só um POST + parse — incluir SDK
// inteiro só pra mandar 1 mensagem é overhead (~6MB compilado).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
)

type Config struct {
	BotToken string
}

func (c Config) Enabled() bool { return c.BotToken != "" }

type Notifier struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Notifier {
	return &Notifier{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

func (n *Notifier) Type() domain.ChannelType { return domain.ChannelTelegram }

// Send envia para um chat_id (negativo para grupo, positivo para usuário).
func (n *Notifier) Send(ctx context.Context, target, subject, body string) error {
	if !n.cfg.Enabled() {
		return domain.ErrChannelDisabled
	}
	if target == "" {
		return fmt.Errorf("telegram: chat_id vazio")
	}

	text := body
	if subject != "" {
		text = "*" + escapeMarkdown(subject) + "*\n\n" + body
	}

	payload := map[string]any{
		"chat_id":    target,
		"text":       text,
		"parse_mode": "Markdown",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := "https://api.telegram.org/bot" + n.cfg.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram: status %d", resp.StatusCode)
	}
	return nil
}

// escapeMarkdown — bot api Markdown (legacy) não tolera *, _, [, ], (, )
// no meio do texto sem escape.
func escapeMarkdown(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '*', '_', '[', ']', '(', ')':
			out = append(out, '\\', c)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
