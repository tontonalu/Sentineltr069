package voalle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
)

// tokenManager guarda o access_token + expiry e renova on-demand.
// Pensado para chamadas concorrentes: lock simples ao redor do refresh.
//
// O Voalle (e a maioria dos OAuth-providers) emite tokens com expires_in
// em segundos. Renovamos 30s antes para evitar flakes em chamadas perto
// do limite.
type tokenManager struct {
	cfg    Config
	client *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

func newTokenManager(cfg Config) *tokenManager {
	return &tokenManager{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Token devolve um access_token válido. Se o token cacheado ainda está
// dentro da janela, devolve sem ir à rede.
func (t *tokenManager) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.accessToken != "" && time.Now().Before(t.expiresAt.Add(-30*time.Second)) {
		return t.accessToken, nil
	}

	token, ttl, err := t.fetch(ctx)
	if err != nil {
		return "", err
	}
	t.accessToken = token
	t.expiresAt = time.Now().Add(ttl)
	return token, nil
}

// Invalidate força fetch novo na próxima chamada (use após receber 401).
func (t *tokenManager) Invalidate() {
	t.mu.Lock()
	t.accessToken = ""
	t.expiresAt = time.Time{}
	t.mu.Unlock()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func (t *tokenManager) fetch(ctx context.Context) (string, time.Duration, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {t.cfg.ClientID},
		"client_secret": {t.cfg.ClientSecret},
	}

	endpoint := t.cfg.BaseURL + t.cfg.TokenPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("voalle: build oauth req: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Retry leve (3 tentativas com backoff) para flakes de rede.
	var (
		resp     *http.Response
		lastErr  error
		backoff  = 500 * time.Millisecond
		attempts = 3
	)
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "", 0, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		resp, lastErr = t.client.Do(req)
		if lastErr == nil && resp.StatusCode < 500 {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	if lastErr != nil {
		return "", 0, fmt.Errorf("voalle: oauth req: %w", lastErr)
	}
	if resp == nil {
		return "", 0, errors.New("voalle: oauth: resposta vazia")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", 0, fmt.Errorf("%w: status %d: %s", erp.ErrAuth, resp.StatusCode, body)
	}
	if resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("voalle: oauth status %d: %s", resp.StatusCode, body)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("voalle: parse token: %w", err)
	}
	if tr.AccessToken == "" {
		return "", 0, errors.New("voalle: access_token vazio na resposta")
	}
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}
	return tr.AccessToken, ttl, nil
}
