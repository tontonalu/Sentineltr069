// identity_repo — adapter Postgres dos repositórios de identidade.
//
// Implementado com pgx direto (sem ORM) para máxima clareza e performance.
// Quando o domínio amadurecer e o número de queries crescer, podemos migrar
// para sqlc geração — interfaces em domain/identity ficam estáveis.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/celinet/sentinel-acs/internal/domain/identity"
)

// ────────────────────── UserRepository ──────────────────────

type UserRepo struct{ pool Pool }

func NewUserRepo(pool Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) Create(ctx context.Context, u *identity.User) error {
	const q = `
		INSERT INTO users (id, email, password_hash, full_name, is_active)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`
	var idArg any
	if u.ID != uuid.Nil {
		idArg = u.ID
	}
	err := r.pool.QueryRow(ctx, q, idArg, u.Email, u.PasswordHash, u.FullName, u.IsActive).
		Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err, "users_email_key") {
			return identity.ErrEmailTaken
		}
		return fmt.Errorf("user create: %w", err)
	}
	return nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	const q = `
		SELECT id, email, password_hash, COALESCE(totp_secret,''), totp_enabled,
		       full_name, is_active, last_login_at, created_at, updated_at
		  FROM users WHERE id = $1`
	return r.scanUser(ctx, q, id)
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	const q = `
		SELECT id, email, password_hash, COALESCE(totp_secret,''), totp_enabled,
		       full_name, is_active, last_login_at, created_at, updated_at
		  FROM users WHERE email = $1`
	return r.scanUser(ctx, q, email)
}

func (r *UserRepo) scanUser(ctx context.Context, q string, arg any) (*identity.User, error) {
	var u identity.User
	err := r.pool.QueryRow(ctx, q, arg).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.TOTPSecret, &u.TOTPEnabled,
		&u.FullName, &u.IsActive, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user scan: %w", err)
	}
	return &u, nil
}

func (r *UserRepo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`, id, hash)
	return err
}

func (r *UserRepo) UpdateLastLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *UserRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET is_active = $2, updated_at = NOW() WHERE id = $1`, id, active)
	return err
}

func (r *UserRepo) UpdateTOTP(ctx context.Context, id uuid.UUID, encryptedSecret string, enabled bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET totp_secret = NULLIF($2,''), totp_enabled = $3, updated_at = NOW()
		 WHERE id = $1`, id, encryptedSecret, enabled)
	return err
}

func (r *UserRepo) List(ctx context.Context, p identity.Page) ([]identity.User, int, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	const q = `
		SELECT id, email, '' AS password_hash, '' AS totp_secret, totp_enabled,
		       full_name, is_active, last_login_at, created_at, updated_at,
		       COUNT(*) OVER() AS total
		  FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("user list: %w", err)
	}
	defer rows.Close()

	var (
		users []identity.User
		total int
	)
	for rows.Next() {
		var u identity.User
		if err := rows.Scan(
			&u.ID, &u.Email, &u.PasswordHash, &u.TOTPSecret, &u.TOTPEnabled,
			&u.FullName, &u.IsActive, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
			&total,
		); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// ────────────────────── RoleRepository ──────────────────────

type RoleRepo struct{ pool Pool }

func NewRoleRepo(pool Pool) *RoleRepo { return &RoleRepo{pool: pool} }

func (r *RoleRepo) GetByName(ctx context.Context, name string) (*identity.Role, error) {
	const q = `SELECT id, name, COALESCE(description,''), is_system, created_at FROM roles WHERE name = $1`
	var role identity.Role
	err := r.pool.QueryRow(ctx, q, name).Scan(
		&role.ID, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrRoleNotFound
	}
	return &role, err
}

func (r *RoleRepo) List(ctx context.Context) ([]identity.Role, error) {
	const q = `SELECT id, name, COALESCE(description,''), is_system, created_at FROM roles ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Role
	for rows.Next() {
		var role identity.Role
		if err := rows.Scan(&role.ID, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

func (r *RoleRepo) Permissions(ctx context.Context, roleID uuid.UUID) ([]identity.Permission, error) {
	const q = `
		SELECT p.id, p.resource, p.action, COALESCE(p.description,'')
		  FROM permissions p
		  JOIN role_permissions rp ON rp.permission_id = p.id
		 WHERE rp.role_id = $1
		 ORDER BY p.resource, p.action`
	rows, err := r.pool.Query(ctx, q, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Permission
	for rows.Next() {
		var p identity.Permission
		if err := rows.Scan(&p.ID, &p.Resource, &p.Action, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *RoleRepo) GrantPermission(ctx context.Context, roleID, permID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO role_permissions (role_id, permission_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, roleID, permID)
	return err
}

func (r *RoleRepo) RevokePermission(ctx context.Context, roleID, permID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM role_permissions WHERE role_id=$1 AND permission_id=$2`, roleID, permID)
	return err
}

// ────────────────────── SessionRepository ──────────────────────

type SessionRepo struct{ pool Pool }

func NewSessionRepo(pool Pool) *SessionRepo { return &SessionRepo{pool: pool} }

func (r *SessionRepo) Create(ctx context.Context, s *identity.Session) error {
	const q = `
		INSERT INTO sessions (id, user_id, ip, user_agent, expires_at)
		VALUES (COALESCE($1, gen_random_uuid()), $2, NULLIF($3,'')::inet, NULLIF($4,''), $5)
		RETURNING id, created_at`
	var idArg any
	if s.ID != uuid.Nil {
		idArg = s.ID
	}
	return r.pool.QueryRow(ctx, q, idArg, s.UserID, s.IP, s.UserAgent, s.ExpiresAt).
		Scan(&s.ID, &s.CreatedAt)
}

func (r *SessionRepo) GetByID(ctx context.Context, id uuid.UUID) (*identity.Session, error) {
	const q = `
		SELECT id, user_id, COALESCE(host(ip),'') AS ip, COALESCE(user_agent,''),
		       expires_at, revoked_at, created_at
		  FROM sessions WHERE id = $1`
	var s identity.Session
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&s.ID, &s.UserID, &s.IP, &s.UserAgent, &s.ExpiresAt, &s.RevokedAt, &s.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrSessionNotFound
	}
	return &s, err
}

func (r *SessionRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

func (r *SessionRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

func (r *SessionRepo) DeleteExpired(ctx context.Context) (int, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < NOW() OR (revoked_at IS NOT NULL AND revoked_at < NOW() - INTERVAL '7 days')`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ────────────────────── AssignmentRepository ──────────────────────

type AssignmentRepo struct{ pool Pool }

func NewAssignmentRepo(pool Pool) *AssignmentRepo { return &AssignmentRepo{pool: pool} }

func (r *AssignmentRepo) Grant(ctx context.Context, a identity.Assignment) error {
	const q = `
		INSERT INTO user_roles (user_id, role_id, scope_id, granted_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, role_id, scope_id) DO NOTHING`
	_, err := r.pool.Exec(ctx, q, a.UserID, a.RoleID, a.ScopeID, a.GrantedBy)
	return err
}

func (r *AssignmentRepo) Revoke(ctx context.Context, userID, roleID, scopeID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM user_roles WHERE user_id=$1 AND role_id=$2 AND scope_id=$3`,
		userID, roleID, scopeID)
	return err
}

func (r *AssignmentRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]identity.Assignment, error) {
	const q = `
		SELECT user_id, role_id, scope_id, granted_at, granted_by
		  FROM user_roles WHERE user_id = $1`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Assignment
	for rows.Next() {
		var a identity.Assignment
		if err := rows.Scan(&a.UserID, &a.RoleID, &a.ScopeID, &a.GrantedAt, &a.GrantedBy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// EffectivePermissions agrega tudo de uma vez via SQL — evita N+1.
//
// is_superadmin é detectado pela presença do role de sistema "superadmin"
// em escopo global.
func (r *AssignmentRepo) EffectivePermissions(ctx context.Context, userID uuid.UUID) (*identity.EffectivePermissions, error) {
	const q = `
		SELECT
		    COALESCE(BOOL_OR(rl.name = 'superadmin' AND ur.scope_id = '00000000-0000-0000-0000-000000000000'::uuid), FALSE) AS is_super,
		    ur.scope_id,
		    p.resource || '.' || p.action AS perm_key
		  FROM user_roles ur
		  JOIN roles rl              ON rl.id = ur.role_id
		  LEFT JOIN role_permissions rp ON rp.role_id = rl.id
		  LEFT JOIN permissions p     ON p.id = rp.permission_id
		 WHERE ur.user_id = $1
		 GROUP BY ur.scope_id, perm_key`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &identity.EffectivePermissions{
		UserID: userID,
		Keys:   map[string]struct{}{},
		Scopes: map[uuid.UUID]map[string]struct{}{},
	}

	for rows.Next() {
		var (
			isSuper bool
			scope   uuid.UUID
			permKey *string
		)
		if err := rows.Scan(&isSuper, &scope, &permKey); err != nil {
			return nil, err
		}
		if isSuper {
			out.IsSuperadmin = true
		}
		if permKey == nil || *permKey == "." {
			continue
		}
		if scope == identity.GlobalScope {
			out.Keys[*permKey] = struct{}{}
		} else {
			if out.Scopes[scope] == nil {
				out.Scopes[scope] = map[string]struct{}{}
			}
			out.Scopes[scope][*permKey] = struct{}{}
		}
	}
	return out, rows.Err()
}
