// Package integration contém os casos de uso de sincronização com sistemas
// externos (ERPs, RADIUS futuro). Camada de aplicação — orquestra plugins
// (em internal/infrastructure/erp/*) com repositórios de domínio.
package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
	"github.com/celinet/sentinel-acs/internal/platform/logger"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
)

// ERPSyncService orquestra a sincronização periódica de customers entre um
// ERP (via plugin) e nossa tabela `customers`.
//
// Estratégia:
//   - Mantém o último timestamp de sucesso (usado como Since na próxima volta)
//   - Pagina até HasMore=false
//   - Upsert idempotente (chave external_source + external_id)
//   - Erro num customer não derruba o batch — log + métrica
type ERPSyncService struct {
	provider  erp.Provider
	customers domain.CustomerRepository
	tracker   *StatusTracker

	mu        sync.Mutex
	lastSince time.Time
}

func NewERPSyncService(p erp.Provider, c domain.CustomerRepository, t *StatusTracker) *ERPSyncService {
	return &ERPSyncService{
		provider:  p,
		customers: c,
		tracker:   t,
	}
}

// SyncResult traz métricas para logging/UI.
type SyncResult struct {
	Plugin    string
	Total     int
	Upserted  int
	Errors    int
	Pages     int
	Duration  time.Duration
	StartedAt time.Time
	EndedAt   time.Time
}

// Tick executa um ciclo completo de sync (todas as páginas necessárias).
// Idempotente. ctx deve ter timeout — chamadas longas paginam várias vezes.
func (s *ERPSyncService) Tick(ctx context.Context) (*SyncResult, error) {
	if s.provider == nil {
		return nil, errors.New("erp_sync: provider nil")
	}

	log := logger.FromContext(ctx).With("plugin", s.provider.Info().Slug)
	start := time.Now()

	s.mu.Lock()
	since := s.lastSince
	s.mu.Unlock()

	res := &SyncResult{
		Plugin:    s.provider.Info().Slug,
		StartedAt: start,
	}

	var (
		cursor *erp.Cursor
		opts   = erp.SyncOptions{PageSize: 100}
	)
	if !since.IsZero() {
		opts.Since = &since
	}

	for {
		opts.Pagination = cursor
		page, err := s.provider.SyncCustomers(ctx, opts)
		if err != nil {
			res.Errors++
			res.Duration = time.Since(start)
			res.EndedAt = time.Now()
			s.tracker.Record(s.provider.Info().Slug, res, err)
			return res, fmt.Errorf("erp_sync: page %d: %w", res.Pages+1, err)
		}
		res.Pages++
		res.Total += len(page.Customers)

		for _, c := range page.Customers {
			if err := s.upsertOne(ctx, c); err != nil {
				log.Warn("upsert customer failed",
					"external_id", c.ExternalID, "err", err)
				res.Errors++
				continue
			}
			res.Upserted++
		}

		if !page.HasMore || page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}

	res.Duration = time.Since(start)
	res.EndedAt = time.Now()

	// Avança o cursor temporal só se não houve erro grave.
	if res.Errors == 0 {
		s.mu.Lock()
		s.lastSince = start
		s.mu.Unlock()
	}

	s.tracker.Record(s.provider.Info().Slug, res, nil)
	log.Info("erp sync done",
		"total", res.Total, "upserted", res.Upserted, "errors", res.Errors,
		"pages", res.Pages, "duration_ms", res.Duration.Milliseconds())

	return res, nil
}

func (s *ERPSyncService) upsertOne(ctx context.Context, c erp.Customer) error {
	dom := &domain.Customer{
		ExternalSource: s.provider.Info().Slug,
		ExternalID:     c.ExternalID,
		FullName:       c.FullName,
		Document:       c.Document,
		PPPoELogin:     c.PPPoELogin,
		PlanName:       c.PlanName,
		Address:        c.Address,
		Status:         c.Status,
	}
	if dom.Status == "" {
		dom.Status = domain.CustomerActive
	}
	return s.customers.Upsert(ctx, dom)
}

// ResetSince zera o cursor temporal — força full sync no próximo Tick.
// Útil para reconciliações manuais.
func (s *ERPSyncService) ResetSince() {
	s.mu.Lock()
	s.lastSince = time.Time{}
	s.mu.Unlock()
}
