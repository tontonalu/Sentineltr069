// Package identity contém o modelo de domínio de RBAC (usuários, roles,
// permissões, sessões). Camada pura — sem dependências de DB ou HTTP.
package identity

import (
	"time"

	"github.com/google/uuid"
)

// GlobalScope representa "sem escopo específico" em user_roles.scope_id.
// É um zero-UUID (ver migration 00001) para que o PRIMARY KEY funcione
// sem precisar de COALESCE.
var GlobalScope = uuid.Nil

// User é a entidade central. password_hash, totp_secret e is_active devem
// ser mutados apenas via casos de uso (application/identity).
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	TOTPSecret   string // cifrado com age — texto claro nunca persistido
	TOTPEnabled  bool
	FullName     string
	IsActive     bool
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Role agrupa permissões. Roles com IsSystem=true não podem ser deletadas via UI.
type Role struct {
	ID          uuid.UUID
	Name        string
	Description string
	IsSystem    bool
	CreatedAt   time.Time
}

// Permission é uma tupla resource+action (ex: "device" + "update_wifi").
type Permission struct {
	ID          uuid.UUID
	Resource    string
	Action      string
	Description string
}

// Key devolve a chave canônica usada pelo middleware de autorização.
func (p Permission) Key() string { return p.Resource + "." + p.Action }

// Assignment liga um User a um Role com escopo opcional (nil = global).
type Assignment struct {
	UserID    uuid.UUID
	RoleID    uuid.UUID
	ScopeID   uuid.UUID // GlobalScope para sem escopo
	GrantedAt time.Time
	GrantedBy *uuid.UUID
}

// IsGlobal indica se o assignment vale para todos os escopos.
func (a Assignment) IsGlobal() bool { return a.ScopeID == GlobalScope }

// Session é um token httpOnly persistido. Expiração e revogação são
// checadas a cada request pelo middleware de auth.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	IP        string
	UserAgent string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// IsValid devolve true se a sessão pode ser usada agora.
func (s Session) IsValid(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	return now.Before(s.ExpiresAt)
}

// EffectivePermissions é o conjunto agregado de permissões que um usuário
// possui em um determinado escopo. Calculado a partir de user_roles +
// role_permissions. O campo IsSuperadmin curto-circuita checagens granulares.
type EffectivePermissions struct {
	UserID       uuid.UUID
	IsSuperadmin bool
	Keys         map[string]struct{} // ex: {"device.update_wifi": {}, ...}
	Scopes       map[uuid.UUID]map[string]struct{}
}

// Has devolve true se o usuário tem permission "resource.action" no escopo
// fornecido. scope=GlobalScope verifica só permissões globais.
func (e EffectivePermissions) Has(resource, action string, scope uuid.UUID) bool {
	if e.IsSuperadmin {
		return true
	}
	key := resource + "." + action
	if _, ok := e.Keys[key]; ok {
		return true
	}
	if scope != GlobalScope {
		if scoped, ok := e.Scopes[scope]; ok {
			if _, ok := scoped[key]; ok {
				return true
			}
		}
	}
	return false
}
