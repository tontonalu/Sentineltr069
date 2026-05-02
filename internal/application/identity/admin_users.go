package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
)

// AdminService agrega operações administrativas sobre usuários e roles.
// Não trata sessões — quando um user é desativado/role revogado, sessões
// existentes continuam válidas até expirar (próxima sessão valida is_active).
//
// Para forçar logout imediato, chame sessions.RevokeAllForUser depois.
type AdminService struct {
	users domain.UserRepository
	roles domain.RoleRepository
	asgs  domain.AssignmentRepository
}

func NewAdminService(
	users domain.UserRepository,
	roles domain.RoleRepository,
	asgs domain.AssignmentRepository,
) *AdminService {
	return &AdminService{users: users, roles: roles, asgs: asgs}
}

// CreateUserInput parametriza criação de novo usuário.
// RoleNames são nomes (ex: "operator", "viewer") — sem escopo (global).
// Atribuir role com escopo de POP virá quando POPs existirem (Fase 2).
type CreateUserInput struct {
	Email     string
	FullName  string
	Password  string
	RoleNames []string
	GrantedBy uuid.UUID // user que está criando (para audit_log futuro)
}

// CreateUser valida, cifra senha e cria registro com roles atribuídas em
// uma sequência de operações. Não há transação cruzando repos no momento —
// se a atribuição de role falhar após o create, o user fica sem role e
// uma operação manual é necessária. Aceitável para a primeira versão;
// quando audit_log entrar (Fase 8), envolveremos em UnitOfWork.
func (s *AdminService) CreateUser(ctx context.Context, in CreateUserInput) (uuid.UUID, error) {
	if err := validateCreateInput(in); err != nil {
		return uuid.Nil, err
	}

	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("admin: hash: %w", err)
	}

	u := &domain.User{
		Email:        strings.ToLower(strings.TrimSpace(in.Email)),
		PasswordHash: hash,
		FullName:     strings.TrimSpace(in.FullName),
		IsActive:     true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return uuid.Nil, err
	}

	for _, name := range in.RoleNames {
		role, err := s.roles.GetByName(ctx, name)
		if err != nil {
			return u.ID, fmt.Errorf("admin: role %q: %w", name, err)
		}
		grantedBy := in.GrantedBy
		assignment := domain.Assignment{
			UserID:  u.ID,
			RoleID:  role.ID,
			ScopeID: domain.GlobalScope,
		}
		if grantedBy != uuid.Nil {
			assignment.GrantedBy = &grantedBy
		}
		if err := s.asgs.Grant(ctx, assignment); err != nil {
			return u.ID, fmt.Errorf("admin: grant %q: %w", name, err)
		}
	}

	return u.ID, nil
}

func validateCreateInput(in CreateUserInput) error {
	email := strings.TrimSpace(in.Email)
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("e-mail inválido")
	}
	if strings.TrimSpace(in.FullName) == "" {
		return errors.New("nome obrigatório")
	}
	if len(in.Password) < 12 {
		return errors.New("senha deve ter ao menos 12 caracteres")
	}
	if len(in.RoleNames) == 0 {
		return errors.New("ao menos um role é obrigatório")
	}
	return nil
}

// SetActive ativa/desativa um usuário. Não revoga sessões — caller decide.
func (s *AdminService) SetActive(ctx context.Context, userID uuid.UUID, active bool) error {
	return s.users.SetActive(ctx, userID, active)
}

// AssignRole concede um role em escopo global. POPs ainda não existem.
func (s *AdminService) AssignRole(ctx context.Context, userID, roleID, grantedBy uuid.UUID) error {
	a := domain.Assignment{
		UserID:  userID,
		RoleID:  roleID,
		ScopeID: domain.GlobalScope,
	}
	if grantedBy != uuid.Nil {
		a.GrantedBy = &grantedBy
	}
	return s.asgs.Grant(ctx, a)
}

// RevokeRole remove um role global de um user.
// Caller deve garantir que o user não fique sem nenhum role (UI valida).
func (s *AdminService) RevokeRole(ctx context.Context, userID, roleID uuid.UUID) error {
	return s.asgs.Revoke(ctx, userID, roleID, domain.GlobalScope)
}

// UserDetail combina os dados que a tela de detalhe precisa em uma única estrutura.
type UserDetail struct {
	User          domain.User
	AssignedRoles []domain.Role
	AvailableRoles []domain.Role
}

// GetDetail carrega user + roles atribuídos + roles disponíveis (todas - atribuídos).
func (s *AdminService) GetDetail(ctx context.Context, userID uuid.UUID) (*UserDetail, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	allRoles, err := s.roles.List(ctx)
	if err != nil {
		return nil, err
	}

	asgs, err := s.asgs.ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	assigned := map[uuid.UUID]bool{}
	for _, a := range asgs {
		if a.IsGlobal() {
			assigned[a.RoleID] = true
		}
	}

	var assignedList, available []domain.Role
	for _, r := range allRoles {
		if assigned[r.ID] {
			assignedList = append(assignedList, r)
		} else {
			available = append(available, r)
		}
	}

	return &UserDetail{
		User:           *user,
		AssignedRoles:  assignedList,
		AvailableRoles: available,
	}, nil
}
