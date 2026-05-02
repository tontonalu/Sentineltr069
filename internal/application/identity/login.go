package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
)

// SessionTTL é o tempo padrão de uma sessão a partir do login.
// Se o usuário tem TOTP habilitado, podemos estender — por ora, fixo.
const SessionTTL = 12 * time.Hour

// LoginInput contém o que o handler HTTP coleta do form.
type LoginInput struct {
	Email     string
	Password  string
	IP        string
	UserAgent string
}

// LoginResult traz o session_id (cookie) e o user para o handler popular o flash.
// SessionID é uuid.Nil quando o login falhou.
type LoginResult struct {
	SessionID  uuid.UUID
	UserID     uuid.UUID
	ExpiresAt  time.Time
	NeedsTOTP  bool // true se o user tem TOTP — handler pede o código no próximo passo
}

// LoginService orquestra autenticação (senha + TOTP) e criação de sessão.
type LoginService struct {
	users    domain.UserRepository
	sessions domain.SessionRepository
}

func NewLoginService(users domain.UserRepository, sessions domain.SessionRepository) *LoginService {
	return &LoginService{users: users, sessions: sessions}
}

// Login valida email+senha. Em caso de sucesso, cria sessão (se sem TOTP)
// ou retorna NeedsTOTP=true (handler emite uma sessão "preauth" curta — fora
// do escopo desta primeira versão; vai entrar na CP-1.5).
func (s *LoginService) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	user, err := s.users.GetByEmail(ctx, in.Email)
	if errors.Is(err, domain.ErrUserNotFound) {
		// Mesmo erro pra "não existe" e "senha errada" → previne enumeração.
		_ = constantTimeFakeHash(in.Password)
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("login: get user: %w", err)
	}

	if !user.IsActive {
		return nil, domain.ErrUserInactive
	}

	ok, err := crypto.VerifyPassword(in.Password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("login: verify: %w", err)
	}
	if !ok {
		return nil, domain.ErrInvalidCredentials
	}

	// Rehash transparente quando os params mínimos sobem.
	if crypto.NeedsRehash(user.PasswordHash, crypto.DefaultArgon2Params) {
		if newHash, err := crypto.HashPassword(in.Password); err == nil {
			_ = s.users.UpdatePasswordHash(ctx, user.ID, newHash)
		}
	}

	if user.TOTPEnabled {
		return &LoginResult{UserID: user.ID, NeedsTOTP: true}, nil
	}

	return s.createSession(ctx, user.ID, in.IP, in.UserAgent)
}

// CompleteLogin é chamado pelo handler de TOTP após verificação bem-sucedida
// do segundo fator. Cria a sessão real e retorna seu id para o cookie.
func (s *LoginService) CompleteLogin(ctx context.Context, userID uuid.UUID, ip, ua string) (*LoginResult, error) {
	return s.createSession(ctx, userID, ip, ua)
}

func (s *LoginService) createSession(ctx context.Context, userID uuid.UUID, ip, ua string) (*LoginResult, error) {
	exp := time.Now().Add(SessionTTL)
	sess := &domain.Session{
		UserID:    userID,
		IP:        ip,
		UserAgent: ua,
		ExpiresAt: exp,
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, fmt.Errorf("login: create session: %w", err)
	}
	if err := s.users.UpdateLastLogin(ctx, userID); err != nil {
		// não-fatal — sessão já criada
		_ = err
	}
	return &LoginResult{SessionID: sess.ID, UserID: userID, ExpiresAt: exp}, nil
}

// constantTimeFakeHash imita o custo de um VerifyPassword real para que
// o tempo de resposta de "user inexistente" não vaze info via timing attack.
func constantTimeFakeHash(password string) error {
	_, err := crypto.HashPassword(password)
	return err
}

// ValidateSession lê a sessão pelo id e verifica validade.
// Devolve o user já carregado (conveniência pra middleware).
func (s *LoginService) ValidateSession(ctx context.Context, id uuid.UUID) (*domain.Session, *domain.User, error) {
	sess, err := s.sessions.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !sess.IsValid(time.Now()) {
		return nil, nil, domain.ErrSessionNotFound
	}
	user, err := s.users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if !user.IsActive {
		return nil, nil, domain.ErrUserInactive
	}
	return sess, user, nil
}

// Logout revoga a sessão (não deleta — mantemos para auditoria até DeleteExpired).
func (s *LoginService) Logout(ctx context.Context, id uuid.UUID) error {
	return s.sessions.Revoke(ctx, id)
}
