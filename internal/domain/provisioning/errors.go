package provisioning

import "errors"

var (
	ErrJobNotFound   = errors.New("provisioning: job não encontrado")
	ErrBatchNotFound = errors.New("provisioning: batch não encontrado")
	ErrBatchClosed   = errors.New("provisioning: batch já finalizado")
	ErrApprovalRequired = errors.New("provisioning: lote excede threshold — aprovação requerida")
	ErrThrottled     = errors.New("provisioning: limite de jobs paralelos atingido")
	ErrProfileNotHomologated = errors.New("provisioning: profile não está homologado para o modelo dos devices alvo")
)
