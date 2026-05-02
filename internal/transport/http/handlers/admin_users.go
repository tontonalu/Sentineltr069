package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	adminpages "github.com/celinet/sentinel-acs/internal/views/pages/admin"
)

// AdminUsersHandler — endpoints administrativos de usuários.
type AdminUsersHandler struct {
	Users domain.UserRepository
	Roles domain.RoleRepository
	Admin *appidentity.AdminService
}

// List GET /admin/users — paginado por query string ?page=N (1-indexed).
func (h *AdminUsersHandler) List(w http.ResponseWriter, r *http.Request) {
	const pageSize = 25

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	users, total, err := h.Users.List(r.Context(), domain.Page{
		Offset: (page - 1) * pageSize,
		Limit:  pageSize,
	})
	if err != nil {
		logger.FromContext(r.Context()).Error("list users failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminpages.UsersList(csrf, users, total, page, pageSize).Render(r.Context(), w)
}

// NewForm GET /admin/users/new
func (h *AdminUsersHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	roles, err := h.Roles.List(r.Context())
	if err != nil {
		logger.FromContext(r.Context()).Error("list roles failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminpages.UserNewPage(csrf, roles, adminpages.FormDraft{}, "").Render(r.Context(), w)
}

// Create POST /admin/users
func (h *AdminUsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	actor, _ := mw.UserFromContext(r.Context())
	var actorID uuid.UUID
	if actor != nil {
		actorID = actor.ID
	}

	draft := adminpages.FormDraft{
		Email:     r.PostFormValue("email"),
		FullName:  r.PostFormValue("full_name"),
		RoleNames: r.PostForm["roles"],
	}

	_, err := h.Admin.CreateUser(r.Context(), appidentity.CreateUserInput{
		Email:     draft.Email,
		FullName:  draft.FullName,
		Password:  r.PostFormValue("password"),
		RoleNames: draft.RoleNames,
		GrantedBy: actorID,
	})
	if err != nil {
		msg := translateAdminError(err)
		logger.FromContext(r.Context()).Info("create user failed",
			"email", draft.Email, "reason", msg)

		roles, _ := h.Roles.List(r.Context())
		csrf := mw.CSRFTokenFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = adminpages.UserNewPage(csrf, roles, draft, msg).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// Detail GET /admin/users/{id}
func (h *AdminUsersHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	detail, err := h.Admin.GetDetail(r.Context(), id)
	if errors.Is(err, domain.ErrUserNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("user detail failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminpages.UserDetailPage(csrf, detail).Render(r.Context(), w)
}

// ToggleActive POST /admin/users/{id}/toggle — alterna is_active e devolve
// a row atualizada (HTMX swap).
func (h *AdminUsersHandler) ToggleActive(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	user, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "user não encontrado", http.StatusNotFound)
		return
	}

	// Não permitir desativar a si mesmo.
	actor, _ := mw.UserFromContext(r.Context())
	if actor != nil && actor.ID == id && user.IsActive {
		http.Error(w, "não é possível desativar a própria conta", http.StatusBadRequest)
		return
	}

	if err := h.Admin.SetActive(r.Context(), id, !user.IsActive); err != nil {
		logger.FromContext(r.Context()).Error("set active failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	updated, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminpages.UserRow(csrf, *updated).Render(r.Context(), w)
}

// AssignRole POST /admin/users/{id}/roles — body: role_id
func (h *AdminUsersHandler) AssignRole(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	roleID, err := uuid.Parse(r.PostFormValue("role_id"))
	if err != nil {
		http.Error(w, "role_id inválido", http.StatusBadRequest)
		return
	}

	actor, _ := mw.UserFromContext(r.Context())
	var actorID uuid.UUID
	if actor != nil {
		actorID = actor.ID
	}

	if err := h.Admin.AssignRole(r.Context(), userID, roleID, actorID); err != nil {
		logger.FromContext(r.Context()).Error("assign role failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users/"+userID.String(), http.StatusSeeOther)
}

// RevokeRole POST /admin/users/{id}/roles/{role_id}/revoke
func (h *AdminUsersHandler) RevokeRole(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	roleID, err := uuid.Parse(chi.URLParam(r, "role_id"))
	if err != nil {
		http.Error(w, "role_id inválido", http.StatusBadRequest)
		return
	}

	if err := h.Admin.RevokeRole(r.Context(), userID, roleID); err != nil {
		logger.FromContext(r.Context()).Error("revoke role failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users/"+userID.String(), http.StatusSeeOther)
}

func translateAdminError(err error) string {
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		return "E-mail já cadastrado."
	case errors.Is(err, domain.ErrRoleNotFound):
		return "Papel selecionado não existe."
	default:
		return err.Error()
	}
}
