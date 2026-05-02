package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/celinet/sentinel-acs/internal/platform/logger"
	"github.com/celinet/sentinel-acs/internal/platform/ratelimit"
)

// RateLimitConfig parametriza um RateLimit. KeyFn calcula a chave a partir
// da request — geralmente "<scope>:<ip>" ou "<scope>:<user_id>".
//
// FailOpen=true (padrão recomendado) deixa passar requests quando o Redis
// está fora — preserva disponibilidade. Em endpoints muito sensíveis pode
// optar por FailOpen=false (rejeita 503).
type RateLimitConfig struct {
	Limiter   *ratelimit.Limiter
	KeyFn     func(r *http.Request) string
	Limit     int64
	Window    time.Duration
	FailOpen  bool
	Message   string // opcional, default "muitas tentativas, tente novamente em alguns minutos"
}

// RateLimit é o middleware HTTP. Aplica em rotas individuais via chi.With.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg.Message == "" {
		cfg.Message = "muitas tentativas, tente novamente em alguns minutos"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFn(r)
			res, err := cfg.Limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window)
			if err != nil {
				logger.FromContext(r.Context()).Warn("rate limit unavailable", "err", err)
				if cfg.FailOpen {
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(res.Limit, 10))
			remaining := res.Limit - res.Count
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(int64(res.ResetIn.Seconds()), 10))

			if !res.Allowed {
				logger.FromContext(r.Context()).Info("rate limited",
					"key", key, "count", res.Count, "limit", res.Limit)
				w.Header().Set("Retry-After", strconv.FormatInt(int64(res.ResetIn.Seconds()), 10))
				http.Error(w, cfg.Message, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyByIP é o KeyFn padrão para anti-bruteforce não-autenticado.
// Combina com prefixo para evitar colisão entre escopos diferentes.
func KeyByIP(prefix string) func(*http.Request) string {
	return func(r *http.Request) string {
		return prefix + ":" + clientIPFromRequest(r)
	}
}

// clientIPFromRequest extrai IP do request, respeitando X-Forwarded-For
// quando a app está atrás de Traefik (configurar Traefik para confiar
// apenas em redes internas — caso contrário pode ser falsificado).
func clientIPFromRequest(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if idx := strings.Index(v, ","); idx >= 0 {
			return strings.TrimSpace(v[:idx])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return host
}
