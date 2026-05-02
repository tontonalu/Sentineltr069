package identity

import (
	"context"

	"github.com/google/uuid"
)

// UserRepository abstrai o storage de usuários.
// Implementação em internal/infrastructure/postgres.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	UpdateLastLogin(ctx context.Context, id uuid.UUID) error
	SetActive(ctx context.Context, id uuid.UUID, active bool) error
	UpdateTOTP(ctx context.Context, id uuid.UUID, encryptedSecret string, enabled bool) error
	List(ctx context.Context, page Page) ([]User, int, error)
}

// SessionRepository — sessões persistidas (httpOnly cookie aponta para id).
type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	GetByID(ctx context.Context, id uuid.UUID) (*Session, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
	DeleteExpired(ctx context.Context) (int, error)
}

// RoleRepository — roles e suas permissões.
type RoleRepository interface {
	GetByName(ctx context.Context, name string) (*Role, error)
	List(ctx context.Context) ([]Role, error)
	Permissions(ctx context.Context, roleID uuid.UUID) ([]Permission, error)
	GrantPermission(ctx context.Context, roleID, permID uuid.UUID) error
	RevokePermission(ctx context.Context, roleID, permID uuid.UUID) error
}

// PermissionRepository — catálogo de permissões.
type PermissionRepository interface {
	List(ctx context.Context) ([]Permission, error)
	GetByKey(ctx context.Context, resource, action string) (*Permission, error)
}

// AssignmentRepository — vínculos user ↔ role com escopo.
type AssignmentRepository interface {
	Grant(ctx context.Context, a Assignment) error
	Revoke(ctx context.Context, userID, roleID, scopeID uuid.UUID) error
	ListForUser(ctx context.Context, userID uuid.UUID) ([]Assignment, error)
	EffectivePermissions(ctx context.Context, userID uuid.UUID) (*EffectivePermissions, error)
}

// Page — paginação genérica.
type Page struct {
	Offset int
	Limit  int
}
