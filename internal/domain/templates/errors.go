package templates

import "errors"

var (
	ErrProfileNotFound   = errors.New("templates: profile não encontrado")
	ErrParameterNotFound = errors.New("templates: parameter não encontrado")
	ErrProfileDuplicate  = errors.New("templates: profile com mesmo nome+vendor+modelo já existe")
	ErrInvalidDataType   = errors.New("templates: data_type inválido")
	ErrEmptyParameters   = errors.New("templates: profile sem parâmetros")
)
