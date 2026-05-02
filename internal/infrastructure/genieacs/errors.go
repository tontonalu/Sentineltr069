package genieacs

import (
	"errors"
	"fmt"
)

var (
	// ErrDeviceNotFound — quando QueryDevices retorna lista vazia para um _id específico.
	ErrDeviceNotFound = errors.New("genieacs: device não encontrado")

	// ErrTaskNotFound — task já consumida ou inexistente.
	ErrTaskNotFound = errors.New("genieacs: task não encontrada")
)

// APIError é o erro retornado quando o NBI responde status >= 400.
// Body é o corpo bruto da resposta (truncado pelo http.Client) — útil pra log.
type APIError struct {
	Op     string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("genieacs %s: status %d: %s", e.Op, e.Status, e.Body)
	}
	return fmt.Sprintf("genieacs %s: status %d", e.Op, e.Status)
}
