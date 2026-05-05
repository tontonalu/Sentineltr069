package homologation

import "errors"

var (
	ErrSessionNotFound       = errors.New("homologation: sessão não encontrada")
	ErrSessionAlreadyActive  = errors.New("homologation: já existe uma sessão ativa para este device")
	ErrSessionNotActive      = errors.New("homologation: sessão não está em estado editável")
	ErrSessionMissingModel   = errors.New("homologation: device de lab precisa ter device_model definido")
	ErrSessionMissingTree    = errors.New("homologation: árvore TR-069 ainda não foi sondada (rode Probe)")
	ErrDeviceNotLab          = errors.New("homologation: device não está marcado como laboratório de homologação")
	ErrCanonicalKeyNotFound  = errors.New("homologation: canonical_key não encontrada no catálogo")
	ErrMappingNotFound       = errors.New("homologation: mapping não encontrado")
	ErrMappingDuplicate      = errors.New("homologation: canonical_key já mapeada nesta sessão")
	ErrInvalidStatus         = errors.New("homologation: status inválido")
	ErrNoEligibleMappings    = errors.New("homologation: nenhum mapping passou nos testes — não há o que homologar")
	ErrModelHomologationNotFound = errors.New("homologation: registro de homologação não encontrado")
)
