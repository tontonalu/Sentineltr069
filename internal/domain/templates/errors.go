package templates

import "errors"

var (
	ErrProfileNotFound   = errors.New("templates: profile não encontrado")
	ErrParameterNotFound = errors.New("templates: parameter não encontrado")
	ErrProfileDuplicate  = errors.New("templates: profile com mesmo nome+vendor+modelo já existe")
	ErrInvalidDataType   = errors.New("templates: data_type inválido")
	ErrEmptyParameters   = errors.New("templates: profile sem parâmetros")

	// ErrProfileImmutable é devolvido pelo Update quando o profile já foi
	// homologado (is_homologated=TRUE). O conteúdo vira histórico permanente;
	// para evoluir, abre-se nova sessão de homologação que gera profile
	// novo (ex.: _v2), preservando v1 como referência.
	ErrProfileImmutable = errors.New("templates: profile homologado é imutável — gere uma nova versão via wizard de homologação")
)
