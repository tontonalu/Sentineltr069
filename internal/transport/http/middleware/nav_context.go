package middleware

import (
	"context"
	"net/http"
)

type navCtxKey string

const navPathKey navCtxKey = "nav.path"

// NavContext injeta r.URL.Path no contexto para que o layout (templ Base)
// possa marcar o link ativo na sidebar sem ter que mudar a assinatura
// Base(title) — o que quebraria 14+ call sites.
func NavContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), navPathKey, r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PathFromContext devolve o path da request atual, ou "" se ausente.
func PathFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(navPathKey).(string); ok {
		return v
	}
	return ""
}
