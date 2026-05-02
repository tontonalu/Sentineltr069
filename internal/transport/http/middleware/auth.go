package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// SessionCookieName é o nome canônico do cookie httpOnly de sessão.
const SessionCookieName = "sentinel_session"

type authCtxKey string

const (
	userCtxKey  authCtxKey = "auth.user"
	permsCtxKey authCtxKey = "auth.perms"
)

// AuthDeps são as dependências injetadas no middleware.
// Mantemos a interface mínima — o middleware não conhece o storage diretamente.
type AuthDeps struct {
	Login       *appidentity.LoginService
	Assignments domain.AssignmentRepository
	// LoginURL — para onde redirecionar quando não autenticado. "/login" por padrão.
	LoginURL string
}

// RequireAuth lê o cookie de sessão, valida via LoginService e injeta no contexto:
//   - User (domain.User)
//   - EffectivePermissions (domain.EffectivePermissions)
//
// Comportamento em falha:
//   - Sem cookie → 302 para LoginURL (ou 401 para chamadas HTMX/JSON)
//   - Cookie inválido/expirado → cookie é apagado e 302
func RequireAuth(d AuthDeps) func(http.Handler) http.Handler {
	loginURL := d.LoginURL
	if loginURL == "" {
		loginURL = "/login"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				redirectOrUnauthorized(w, r, loginURL)
				return
			}

			sid, err := uuid.Parse(cookie.Value)
			if err != nil {
				clearCookie(w)
				redirectOrUnauthorized(w, r, loginURL)
				return
			}

			_, user, err := d.Login.ValidateSession(r.Context(), sid)
			if err != nil {
				if errors.Is(err, domain.ErrSessionNotFound) || errors.Is(err, domain.ErrUserInactive) {
					clearCookie(w)
					redirectOrUnauthorized(w, r, loginURL)
					return
				}
				logger.FromContext(r.Context()).Error("session validation failed", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			perms, err := d.Assignments.EffectivePermissions(r.Context(), user.ID)
			if err != nil {
				logger.FromContext(r.Context()).Error("load permissions failed", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			ctx := context.WithValue(r.Context(), userCtxKey, user)
			ctx = context.WithValue(ctx, permsCtxKey, perms)

			// Adiciona user_id ao logger para os logs subsequentes da request.
			l := logger.FromContext(ctx).With("user_id", user.ID.String())
			ctx = logger.WithLogger(ctx, l)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequirePermission deve vir DEPOIS de RequireAuth. Verifica que o usuário
// tem (resource, action) em escopo global. Para ações com escopo de POP,
// use RequirePermissionScoped.
func RequirePermission(resource, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			perms, ok := PermissionsFromContext(r.Context())
			if !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if !perms.Has(resource, action, domain.GlobalScope) {
				logger.FromContext(r.Context()).Warn("permission denied",
					"resource", resource, "action", action)
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UserFromContext devolve o user autenticado, ou (nil, false) se ausente.
func UserFromContext(ctx context.Context) (*domain.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*domain.User)
	return u, ok
}

// PermissionsFromContext devolve as permissões efetivas do user.
func PermissionsFromContext(ctx context.Context) (*domain.EffectivePermissions, bool) {
	p, ok := ctx.Value(permsCtxKey).(*domain.EffectivePermissions)
	return p, ok
}

// UserHasPermission é a versão "soft" de RequirePermission: devolve bool
// para handlers que precisam decidir o que renderizar (ex: gating de widgets
// no dashboard) em vez de retornar 403.
func UserHasPermission(ctx context.Context, resource, action string) bool {
	perms, ok := PermissionsFromContext(ctx)
	if !ok {
		return false
	}
	return perms.Has(resource, action, domain.GlobalScope)
}

func redirectOrUnauthorized(w http.ResponseWriter, r *http.Request, loginURL string) {
	if r.Header.Get("HX-Request") == "true" || isJSONRequest(r) {
		w.Header().Set("HX-Redirect", loginURL) // HTMX faz o redirect client-side
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, loginURL, http.StatusFound)
}

func isJSONRequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept != "" && (accept == "application/json" ||
		(len(accept) >= 16 && accept[:16] == "application/json"))
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}
