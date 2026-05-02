// Package identity — casos de uso da camada de aplicação.
package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
)

// SeedAdminInput parametriza a criação do admin inicial.
// Em prod, AdminPassword vem de env (SEED_ADMIN_PASSWORD). Em dev/staging,
// se vazio, o caso de uso retorna erro — não geramos senha automática para
// evitar que ela fique flutuando em logs.
type SeedAdminInput struct {
	Email    string
	FullName string
	Password string
}

// SeedAdmin cria (ou re-ativa) um usuário com role superadmin global.
// Idempotente: se o admin já existe, apenas garante que está ativo e tem o role.
func SeedAdmin(
	ctx context.Context,
	users domain.UserRepository,
	roles domain.RoleRepository,
	assignments domain.AssignmentRepository,
	in SeedAdminInput,
) (uuid.UUID, error) {
	if in.Email == "" || in.Password == "" {
		return uuid.Nil, errors.New("seed: email e password obrigatórios")
	}
	if len(in.Password) < 12 {
		return uuid.Nil, errors.New("seed: password deve ter pelo menos 12 caracteres")
	}

	superRole, err := roles.GetByName(ctx, "superadmin")
	if err != nil {
		return uuid.Nil, fmt.Errorf("seed: buscar role superadmin: %w", err)
	}

	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("seed: hash: %w", err)
	}

	existing, err := users.GetByEmail(ctx, in.Email)
	switch {
	case errors.Is(err, domain.ErrUserNotFound):
		// Criar do zero.
		u := &domain.User{
			Email:        in.Email,
			PasswordHash: hash,
			FullName:     in.FullName,
			IsActive:     true,
		}
		if err := users.Create(ctx, u); err != nil {
			return uuid.Nil, fmt.Errorf("seed: criar user: %w", err)
		}
		if err := assignments.Grant(ctx, domain.Assignment{
			UserID:  u.ID,
			RoleID:  superRole.ID,
			ScopeID: domain.GlobalScope,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("seed: atribuir superadmin: %w", err)
		}
		return u.ID, nil

	case err != nil:
		return uuid.Nil, fmt.Errorf("seed: buscar user: %w", err)
	}

	// Já existe — garantir ativo + role superadmin.
	if !existing.IsActive {
		if err := users.SetActive(ctx, existing.ID, true); err != nil {
			return uuid.Nil, fmt.Errorf("seed: reativar: %w", err)
		}
	}
	if err := assignments.Grant(ctx, domain.Assignment{
		UserID:  existing.ID,
		RoleID:  superRole.ID,
		ScopeID: domain.GlobalScope,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("seed: atribuir superadmin: %w", err)
	}
	return existing.ID, nil
}
