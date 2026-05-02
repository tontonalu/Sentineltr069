package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
)

func TestCreateUserSuccess(t *testing.T) {
	users := newFakeUserRepo()
	roles := newFakeRoleRepo("operator", "viewer")
	asgs := newFakeAssignmentRepo()

	svc := NewAdminService(users, roles, asgs)

	id, err := svc.CreateUser(context.Background(), CreateUserInput{
		Email:     "  Eve@LOCAL  ",
		FullName:  "Eve Test",
		Password:  "senha-superforte-1234",
		RoleNames: []string{"operator", "viewer"},
		GrantedBy: uuid.New(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("id vazio")
	}

	user, err := users.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if user.Email != "eve@local" {
		t.Errorf("email não normalizado: %q", user.Email)
	}
	if user.FullName != "Eve Test" {
		t.Errorf("nome incorreto: %q", user.FullName)
	}
	ok, _ := crypto.VerifyPassword("senha-superforte-1234", user.PasswordHash)
	if !ok {
		t.Error("hash não bate com senha original")
	}

	asgList, _ := asgs.ListForUser(context.Background(), id)
	if len(asgList) != 2 {
		t.Errorf("esperava 2 assignments, got %d", len(asgList))
	}
}

func TestCreateUserValidation(t *testing.T) {
	svc := NewAdminService(newFakeUserRepo(), newFakeRoleRepo("operator"), newFakeAssignmentRepo())

	cases := []struct {
		name string
		in   CreateUserInput
		want string
	}{
		{"email vazio", CreateUserInput{Email: "", FullName: "X", Password: "abcdefghijkl", RoleNames: []string{"operator"}}, "e-mail"},
		{"email sem @", CreateUserInput{Email: "x", FullName: "X", Password: "abcdefghijkl", RoleNames: []string{"operator"}}, "e-mail"},
		{"sem nome", CreateUserInput{Email: "a@b", FullName: "", Password: "abcdefghijkl", RoleNames: []string{"operator"}}, "nome"},
		{"senha curta", CreateUserInput{Email: "a@b", FullName: "X", Password: "curta", RoleNames: []string{"operator"}}, "senha"},
		{"sem role", CreateUserInput{Email: "a@b", FullName: "X", Password: "abcdefghijkl", RoleNames: nil}, "role"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.CreateUser(context.Background(), c.in)
			if err == nil {
				t.Fatal("esperava erro")
			}
			if !strings.Contains(strings.ToLower(err.Error()), c.want) {
				t.Errorf("mensagem inesperada: %v (esperava conter %q)", err, c.want)
			}
		})
	}
}

func TestCreateUserDuplicateEmail(t *testing.T) {
	users := newFakeUserRepo()
	roles := newFakeRoleRepo("operator")
	asgs := newFakeAssignmentRepo()
	svc := NewAdminService(users, roles, asgs)

	in := CreateUserInput{
		Email: "x@local", FullName: "X", Password: "abcdefghijkl", RoleNames: []string{"operator"},
	}
	if _, err := svc.CreateUser(context.Background(), in); err != nil {
		t.Fatalf("primeira criação: %v", err)
	}
	_, err := svc.CreateUser(context.Background(), in)
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Errorf("esperava ErrEmailTaken, got %v", err)
	}
}

func TestSetActive(t *testing.T) {
	users := newFakeUserRepo()
	roles := newFakeRoleRepo("operator")
	asgs := newFakeAssignmentRepo()
	svc := NewAdminService(users, roles, asgs)

	id, _ := svc.CreateUser(context.Background(), CreateUserInput{
		Email: "x@local", FullName: "X", Password: "abcdefghijkl", RoleNames: []string{"operator"},
	})

	if err := svc.SetActive(context.Background(), id, false); err != nil {
		t.Fatalf("setActive: %v", err)
	}
	user, _ := users.GetByID(context.Background(), id)
	if user.IsActive {
		t.Error("user deveria estar inativo")
	}
}

func TestAssignAndRevokeRole(t *testing.T) {
	users := newFakeUserRepo()
	roles := newFakeRoleRepo("operator", "viewer")
	asgs := newFakeAssignmentRepo()
	svc := NewAdminService(users, roles, asgs)

	id, _ := svc.CreateUser(context.Background(), CreateUserInput{
		Email: "x@local", FullName: "X", Password: "abcdefghijkl", RoleNames: []string{"viewer"},
	})

	operator, _ := roles.GetByName(context.Background(), "operator")
	if err := svc.AssignRole(context.Background(), id, operator.ID, uuid.New()); err != nil {
		t.Fatalf("assign: %v", err)
	}

	asgList, _ := asgs.ListForUser(context.Background(), id)
	if len(asgList) != 2 {
		t.Errorf("esperava 2 roles, got %d", len(asgList))
	}

	if err := svc.RevokeRole(context.Background(), id, operator.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	asgList, _ = asgs.ListForUser(context.Background(), id)
	if len(asgList) != 1 {
		t.Errorf("esperava 1 role, got %d", len(asgList))
	}
}
