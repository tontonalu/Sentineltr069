package erp

import "errors"

var (
	// ErrCapabilityUnsupported é retornado por métodos opcionais quando o
	// plugin não declara aquela Capability. Caller deve checar Info().Has()
	// antes de chamar para evitar este erro.
	ErrCapabilityUnsupported = errors.New("erp: capability não suportada por este plugin")

	// ErrCustomerNotFound — quando GetCustomerByID não encontra externalID.
	ErrCustomerNotFound = errors.New("erp: cliente não encontrado")

	// ErrAuth — falha de autenticação contra o ERP (token inválido/expirado).
	ErrAuth = errors.New("erp: falha de autenticação")
)
