// Package postgres expõe o pool de conexões pgx e helpers de saúde.
//
// Implementação completa (sqlc, repositórios) virá nas Fases 1+. Por ora,
// este pacote oferece apenas o suficiente para o /healthz validar conectividade.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool é o tipo exportado para injeção em handlers.
type Pool = *pgxpool.Pool

// New cria o pool a partir de uma DSN. ctx aplica timeout para a 1ª conexão.
func New(ctx context.Context, dsn string) (Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres: DATABASE_URL vazio")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return pool, nil
}

// Ping wrapper para uso no /healthz.
func Ping(ctx context.Context, p Pool) error {
	if p == nil {
		return fmt.Errorf("postgres: pool nil")
	}
	return p.Ping(ctx)
}
