package identity

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
)

// In-memory fakes para testar a camada de aplicação sem precisar de Postgres.
// Repos reais (com testcontainers) são testados em internal/infrastructure/postgres.

type fakeUserRepo struct {
	mu    sync.Mutex
	users map[uuid.UUID]*domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: map[uuid.UUID]*domain.User{}}
}

func (r *fakeUserRepo) Create(_ context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.users {
		if strings.EqualFold(ex.Email, u.Email) {
			return domain.ErrEmailTaken
		}
	}
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	now := time.Now()
	u.CreatedAt = now
	u.UpdatedAt = now
	cpy := *u
	r.users[u.ID] = &cpy
	return nil
}

func (r *fakeUserRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	cpy := *u
	return &cpy, nil
}

func (r *fakeUserRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if strings.EqualFold(u.Email, email) {
			cpy := *u
			return &cpy, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (r *fakeUserRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return domain.ErrUserNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (r *fakeUserRepo) UpdateLastLogin(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return domain.ErrUserNotFound
	}
	now := time.Now()
	u.LastLoginAt = &now
	return nil
}

func (r *fakeUserRepo) SetActive(_ context.Context, id uuid.UUID, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return domain.ErrUserNotFound
	}
	u.IsActive = active
	return nil
}

func (r *fakeUserRepo) UpdateTOTP(_ context.Context, id uuid.UUID, secret string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return domain.ErrUserNotFound
	}
	u.TOTPSecret = secret
	u.TOTPEnabled = enabled
	return nil
}

func (r *fakeUserRepo) List(_ context.Context, p domain.Page) ([]domain.User, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.User, 0, len(r.users))
	for _, u := range r.users {
		cpy := *u
		out = append(out, cpy)
	}
	total := len(out)
	if p.Offset > total {
		return nil, total, nil
	}
	end := p.Offset + p.Limit
	if end > total {
		end = total
	}
	return out[p.Offset:end], total, nil
}

// ──────────── SessionRepository ────────────

type fakeSessionRepo struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*domain.Session
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{sessions: map[uuid.UUID]*domain.Session{}}
}

func (r *fakeSessionRepo) Create(_ context.Context, s *domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	s.CreatedAt = time.Now()
	cpy := *s
	r.sessions[s.ID] = &cpy
	return nil
}

func (r *fakeSessionRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	cpy := *s
	return &cpy, nil
}

func (r *fakeSessionRepo) Revoke(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return domain.ErrSessionNotFound
	}
	now := time.Now()
	s.RevokedAt = &now
	return nil
}

func (r *fakeSessionRepo) RevokeAllForUser(_ context.Context, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, s := range r.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &now
		}
	}
	return nil
}

func (r *fakeSessionRepo) DeleteExpired(_ context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	now := time.Now()
	for id, s := range r.sessions {
		if s.ExpiresAt.Before(now) {
			delete(r.sessions, id)
			count++
		}
	}
	return count, nil
}

// ──────────── RoleRepository ────────────

type fakeRoleRepo struct {
	mu    sync.Mutex
	roles map[string]*domain.Role
}

func newFakeRoleRepo(seed ...string) *fakeRoleRepo {
	r := &fakeRoleRepo{roles: map[string]*domain.Role{}}
	for _, name := range seed {
		r.roles[name] = &domain.Role{
			ID: uuid.New(), Name: name, IsSystem: true, CreatedAt: time.Now(),
		}
	}
	return r
}

func (r *fakeRoleRepo) GetByName(_ context.Context, name string) (*domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	role, ok := r.roles[name]
	if !ok {
		return nil, domain.ErrRoleNotFound
	}
	cpy := *role
	return &cpy, nil
}

func (r *fakeRoleRepo) List(_ context.Context) ([]domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Role, 0, len(r.roles))
	for _, role := range r.roles {
		out = append(out, *role)
	}
	return out, nil
}

func (r *fakeRoleRepo) Permissions(_ context.Context, _ uuid.UUID) ([]domain.Permission, error) {
	return nil, nil
}
func (r *fakeRoleRepo) GrantPermission(_ context.Context, _, _ uuid.UUID) error  { return nil }
func (r *fakeRoleRepo) RevokePermission(_ context.Context, _, _ uuid.UUID) error { return nil }

// ──────────── AssignmentRepository ────────────

type fakeAssignmentRepo struct {
	mu          sync.Mutex
	assignments map[string]domain.Assignment // key = userID|roleID|scopeID
}

func newFakeAssignmentRepo() *fakeAssignmentRepo {
	return &fakeAssignmentRepo{assignments: map[string]domain.Assignment{}}
}

func asgKey(u, r, s uuid.UUID) string { return u.String() + "|" + r.String() + "|" + s.String() }

func (r *fakeAssignmentRepo) Grant(_ context.Context, a domain.Assignment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a.GrantedAt = time.Now()
	r.assignments[asgKey(a.UserID, a.RoleID, a.ScopeID)] = a
	return nil
}

func (r *fakeAssignmentRepo) Revoke(_ context.Context, userID, roleID, scopeID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.assignments, asgKey(userID, roleID, scopeID))
	return nil
}

func (r *fakeAssignmentRepo) ListForUser(_ context.Context, userID uuid.UUID) ([]domain.Assignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Assignment
	for _, a := range r.assignments {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *fakeAssignmentRepo) EffectivePermissions(_ context.Context, userID uuid.UUID) (*domain.EffectivePermissions, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := &domain.EffectivePermissions{
		UserID: userID,
		Keys:   map[string]struct{}{},
		Scopes: map[uuid.UUID]map[string]struct{}{},
	}
	// Para simplicidade do fake, marcamos isSuper se houver assignment com role_id de nome
	// "superadmin" — mas como o repo não conhece roleNames, deixamos tudo a cargo do teste.
	return out, nil
}
