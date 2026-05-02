package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	authpages "github.com/celinet/sentinel-acs/internal/views/pages/auth"
)

// AuthHandler agrupa endpoints de login/logout. CookieSecure controla a
// flag Secure dos cookies (true em prod com HTTPS).
//
// Preauth é necessário quando há usuários com TOTP — para gerar o token
// que liga POST /login → POST /login/totp. Pode ser nil em deploys sem
// 2FA habilitado, embora não seja recomendado.
type AuthHandler struct {
	Login        *appidentity.LoginService
	Preauth      *appidentity.PreauthStore
	CookieSecure bool
}

// LoginPage GET /login — devolve a página completa.
func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Já autenticado? Vai pra home.
	if c, err := r.Cookie(mw.SessionCookieName); err == nil && c.Value != "" {
		if sid, err := uuid.Parse(c.Value); err == nil {
			if _, _, err := h.Login.ValidateSession(r.Context(), sid); err == nil {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}
	}
	token := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authpages.LoginPage(token, "").Render(r.Context(), w)
}

// LoginSubmit POST /login — valida credenciais e cria sessão.
// Em sucesso, set-cookie + redirect para /. Em falha, retorna o form
// re-renderizado com erro (HTMX swap).
func (h *AuthHandler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")

	res, err := h.Login.Login(r.Context(), appidentity.LoginInput{
		Email:     email,
		Password:  password,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		token := mw.CSRFTokenFromContext(r.Context())
		msg := translateLoginError(err)
		logger.FromContext(r.Context()).Info("login failed", "email", email, "reason", msg)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = authpages.LoginForm(token, msg, email).Render(r.Context(), w)
		return
	}

	if res.NeedsTOTP {
		if h.Preauth == nil {
			http.Error(w, "TOTP não configurado no servidor", http.StatusInternalServerError)
			return
		}
		preauthToken, err := h.Preauth.Create(r.Context(), res.UserID)
		if err != nil {
			logger.FromContext(r.Context()).Error("preauth create failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "sentinel_preauth",
			Value:    preauthToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   h.CookieSecure,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   5 * 60,
		})
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login/totp")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/login/totp", http.StatusSeeOther)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     mw.SessionCookieName,
		Value:    res.SessionID.String(),
		Path:     "/",
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  res.ExpiresAt,
	})

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout POST /logout — revoga sessão e limpa cookie.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(mw.SessionCookieName); err == nil && c.Value != "" {
		if sid, err := uuid.Parse(c.Value); err == nil {
			_ = h.Login.Logout(r.Context(), sid)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     mw.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func translateLoginError(err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		return "E-mail ou senha incorretos."
	case errors.Is(err, domain.ErrUserInactive):
		return "Usuário desativado. Procure um administrador."
	default:
		return "Não foi possível entrar agora. Tente novamente."
	}
}

// clientIP extrai o IP real, respeitando X-Forwarded-For do Traefik.
// Em prod, configure Traefik para confiar apenas em redes internas.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// pega o primeiro (client mais distante).
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
