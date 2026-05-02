package identity

import (
	"context"
	"errors"
	"testing"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
)

func TestLoginSuccess(t *testing.T) {
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()

	hash, _ := crypto.HashPassword("senha-do-teste-123")
	_ = users.Create(context.Background(), &domain.User{
		Email:        "alice@local",
		PasswordHash: hash,
		FullName:     "Alice",
		IsActive:     true,
	})

	svc := NewLoginService(users, sessions)
	res, err := svc.Login(context.Background(), LoginInput{
		Email:    "alice@local",
		Password: "senha-do-teste-123",
		IP:       "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.SessionID.String() == "" {
		t.Fatal("session id vazio")
	}
	if res.NeedsTOTP {
		t.Fatal("não deveria precisar de TOTP")
	}
}

func TestLoginInvalidPassword(t *testing.T) {
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()

	hash, _ := crypto.HashPassword("senha-correta-aqui")
	_ = users.Create(context.Background(), &domain.User{
		Email:        "alice@local",
		PasswordHash: hash,
		FullName:     "Alice",
		IsActive:     true,
	})

	svc := NewLoginService(users, sessions)
	_, err := svc.Login(context.Background(), LoginInput{
		Email:    "alice@local",
		Password: "senha-errada-aqui",
	})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("esperava ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginUserNotFound_NoEnumeration(t *testing.T) {
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()
	svc := NewLoginService(users, sessions)

	// Não existe → mesmo erro que senha errada (sem revelar enumeração).
	_, err := svc.Login(context.Background(), LoginInput{
		Email:    "ninguem@local",
		Password: "qualquer-coisa-aqui",
	})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("esperava ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginInactiveUser(t *testing.T) {
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()

	hash, _ := crypto.HashPassword("senha-correta-aqui")
	_ = users.Create(context.Background(), &domain.User{
		Email:        "bob@local",
		PasswordHash: hash,
		FullName:     "Bob",
		IsActive:     false,
	})

	svc := NewLoginService(users, sessions)
	_, err := svc.Login(context.Background(), LoginInput{
		Email:    "bob@local",
		Password: "senha-correta-aqui",
	})
	if !errors.Is(err, domain.ErrUserInactive) {
		t.Fatalf("esperava ErrUserInactive, got %v", err)
	}
}

func TestLoginNeedsTOTP(t *testing.T) {
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()

	hash, _ := crypto.HashPassword("senha-correta-aqui")
	_ = users.Create(context.Background(), &domain.User{
		Email:        "carol@local",
		PasswordHash: hash,
		FullName:     "Carol",
		IsActive:     true,
		TOTPEnabled:  true,
		TOTPSecret:   "fake-encrypted-secret",
	})

	svc := NewLoginService(users, sessions)
	res, err := svc.Login(context.Background(), LoginInput{
		Email:    "carol@local",
		Password: "senha-correta-aqui",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !res.NeedsTOTP {
		t.Fatal("deveria pedir TOTP")
	}
	if res.SessionID.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatal("não deveria ter sessão antes de TOTP")
	}
}

func TestValidateSessionExpired(t *testing.T) {
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()

	hash, _ := crypto.HashPassword("senha-correta-aqui")
	u := &domain.User{
		Email:        "alice@local",
		PasswordHash: hash,
		FullName:     "Alice",
		IsActive:     true,
	}
	_ = users.Create(context.Background(), u)

	svc := NewLoginService(users, sessions)

	res, err := svc.Login(context.Background(), LoginInput{
		Email:    "alice@local",
		Password: "senha-correta-aqui",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Força expiração ao revogar (simula o efeito).
	if err := sessions.Revoke(context.Background(), res.SessionID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, _, err := svc.ValidateSession(context.Background(), res.SessionID); !errors.Is(err, domain.ErrSessionNotFound) {
		t.Fatalf("esperava ErrSessionNotFound, got %v", err)
	}
}
