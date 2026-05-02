package handlers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	authpages "github.com/celinet/sentinel-acs/internal/views/pages/auth"
)

// PreauthCookieName — cookie httpOnly que carrega o token TOTP-pendente
// entre POST /login e POST /login/totp.
const PreauthCookieName = "sentinel_preauth"

// TOTPHandler agrupa endpoints de 2FA — verificação no login e enrollment.
type TOTPHandler struct {
	Login        *appidentity.LoginService
	TOTP         *appidentity.TOTPService
	Preauth      *appidentity.PreauthStore
	CookieSecure bool
}

// LoginTOTPPage GET /login/totp — exige cookie de preauth válido.
func (h *TOTPHandler) LoginTOTPPage(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(PreauthCookieName)
	if err != nil || c.Value == "" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	token := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authpages.VerifyTOTPPage(token, "").Render(r.Context(), w)
}

// LoginTOTPSubmit POST /login/totp — consome preauth, verifica código,
// cria sessão real, troca cookie.
func (h *TOTPHandler) LoginTOTPSubmit(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(PreauthCookieName)
	if err != nil || c.Value == "" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))

	userID, err := h.Preauth.Consume(r.Context(), c.Value)
	if err != nil {
		clearPreauthCookie(w, h.CookieSecure)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if err := h.TOTP.Verify(r.Context(), userID, code); err != nil {
		// Token consumido — para tentar de novo, recriamos o preauth e
		// voltamos com erro inline.
		newToken, _ := h.Preauth.Create(r.Context(), userID)
		setPreauthCookie(w, newToken, h.CookieSecure)

		token := mw.CSRFTokenFromContext(r.Context())
		msg := "Código inválido. Tente novamente."
		if errors.Is(err, domain.ErrTOTPInvalid) {
			msg = "Código incorreto ou expirado."
		}
		logger.FromContext(r.Context()).Info("totp verify failed", "user_id", userID, "err", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = authpages.VerifyTOTPForm(token, msg).Render(r.Context(), w)
		return
	}

	// Sucesso — completa login (cria sessão).
	res, err := h.Login.CompleteLogin(r.Context(), userID, clientIP(r), r.UserAgent())
	if err != nil {
		logger.FromContext(r.Context()).Error("complete login failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	clearPreauthCookie(w, h.CookieSecure)
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

// EnrollPage GET /settings/totp — gera secret + QR. Página renderizada
// apenas para usuários autenticados (passa por RequireAuth).
func (h *TOTPHandler) EnrollPage(w http.ResponseWriter, r *http.Request) {
	user, ok := mw.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.TOTPEnabled {
		// Já habilitado — fluxo de "trocar/desabilitar" virá depois.
		http.Error(w, "2FA já está ativo", http.StatusBadRequest)
		return
	}

	enr, err := h.TOTP.Enroll(r.Context(), user.ID)
	if err != nil {
		logger.FromContext(r.Context()).Error("totp enroll failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(enr.QRPng)
	token := mw.CSRFTokenFromContext(r.Context())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authpages.EnrollTOTPPage(token, dataURL, enr.Secret, "").Render(r.Context(), w)
}

// EnrollSubmit POST /settings/totp — confirma o código contra o secret enviado.
func (h *TOTPHandler) EnrollSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := mw.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	secret := r.PostFormValue("secret")
	code := strings.TrimSpace(r.PostFormValue("code"))

	if err := h.TOTP.Confirm(r.Context(), user.ID, secret, code); err != nil {
		token := mw.CSRFTokenFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = authpages.EnrollTOTPForm(token, secret, "Código inválido. Tente o próximo.").Render(r.Context(), w)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func setPreauthCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     PreauthCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   5 * 60, // alinhado com TTL no Redis
	})
}

func clearPreauthCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     PreauthCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
