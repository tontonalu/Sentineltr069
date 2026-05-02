package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

// CSRFCookieName e CSRFFormField são as chaves canônicas usadas pelos
// formulários templ + handlers POST.
const (
	CSRFCookieName = "sentinel_csrf"
	CSRFFormField  = "_csrf"
	CSRFHeader     = "X-CSRF-Token"
)

type csrfCtxKey string

const csrfTokenCtxKey csrfCtxKey = "csrf.token"

// CSRF é um middleware double-submit cookie:
//   - GET emite (ou reaproveita) um cookie sentinel_csrf com 32 bytes random.
//   - O mesmo valor é injetado no contexto para o template embutir num <input hidden>.
//   - POST/PUT/DELETE precisam apresentar o token via form field _csrf OU
//     header X-CSRF-Token, e ele deve bater (constant-time) com o cookie.
//
// Nota: este padrão funciona porque o cookie só pode ser lido por scripts no
// mesmo origin (e nem isso, pois fica HttpOnly=false intencionalmente, para
// que HTMX possa lê-lo se preferir injeção via header). A defesa real vem
// do fato de que um origin malicioso não consegue ler nem setar esse cookie.
func CSRF(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := readCSRFCookie(r)
			if token == "" {
				token = newCSRFToken()
				setCSRFCookie(w, token, secure)
			}

			if isMutating(r.Method) {
				submitted := r.Header.Get(CSRFHeader)
				if submitted == "" {
					if err := r.ParseForm(); err == nil {
						submitted = r.PostFormValue(CSRFFormField)
					}
				}
				if subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
					http.Error(w, "CSRF token inválido", http.StatusForbidden)
					return
				}
			}

			ctx := context.WithValue(r.Context(), csrfTokenCtxKey, token)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRFTokenFromContext devolve o token corrente para uso em templates.
func CSRFTokenFromContext(ctx context.Context) string {
	t, _ := ctx.Value(csrfTokenCtxKey).(string)
	return t
}

func readCSRFCookie(r *http.Request) string {
	c, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func setCSRFCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // intencional — frontend pode injetar no header X-CSRF-Token
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   12 * 60 * 60, // 12h
	})
}

func newCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
