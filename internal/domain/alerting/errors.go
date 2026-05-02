package alerting

import "errors"

var (
	ErrRuleNotFound      = errors.New("alerting: regra não encontrada")
	ErrAlertNotFound     = errors.New("alerting: alerta não encontrado")
	ErrChannelDisabled   = errors.New("alerting: canal não configurado")
	ErrInvalidRule       = errors.New("alerting: regra inválida")
	ErrCooldownActive    = errors.New("alerting: cooldown ativo — não dispara")
)
