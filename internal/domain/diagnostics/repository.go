package diagnostics

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound — caller deve mapear para 404 na UI.
var ErrNotFound = errors.New("diagnostic not found")

// Repository — persistência. As queries são suficientes para o fluxo:
// Create no submit, Update conforme polling do worker, ListByDevice para
// histórico na UI, ListActive pro tick do worker.
type Repository interface {
	Create(ctx context.Context, d *Diagnostic) error
	GetByID(ctx context.Context, id uuid.UUID) (*Diagnostic, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status, errMsg string) error
	UpdateResult(ctx context.Context, id uuid.UUID, status Status, result map[string]any) error
	ListByDevice(ctx context.Context, deviceID uuid.UUID, limit int) ([]Diagnostic, error)
	// ListActive — used by the worker poller. Retorna requested+running
	// dentro do deadline; tickers são responsáveis por timeoutar antes.
	ListActive(ctx context.Context, limit int) ([]Diagnostic, error)
}
