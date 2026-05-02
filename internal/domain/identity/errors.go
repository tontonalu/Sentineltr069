package identity

import "errors"

var (
	// ErrUserNotFound — quando uma busca por id/email não encontra registro.
	ErrUserNotFound = errors.New("identity: user not found")

	// ErrEmailTaken — usado em criação ou alteração de email.
	ErrEmailTaken = errors.New("identity: email já cadastrado")

	// ErrInvalidCredentials — nunca diferencie "user inexistente" de "senha errada"
	// em mensagens visíveis (evita enumeração).
	ErrInvalidCredentials = errors.New("identity: credenciais inválidas")

	// ErrUserInactive — usuário existe mas está desativado (is_active=false).
	ErrUserInactive = errors.New("identity: usuário inativo")

	// ErrSessionNotFound — sessão revogada/expirada também devolve este erro.
	ErrSessionNotFound = errors.New("identity: sessão inválida")

	// ErrRoleNotFound, ErrPermissionNotFound — buscas que falham.
	ErrRoleNotFound       = errors.New("identity: role não encontrada")
	ErrPermissionNotFound = errors.New("identity: permission não encontrada")

	// ErrTOTPRequired — login passou senha mas falta o código TOTP.
	ErrTOTPRequired = errors.New("identity: TOTP necessário")

	// ErrTOTPInvalid — código TOTP inválido ou expirado.
	ErrTOTPInvalid = errors.New("identity: TOTP inválido")
)
