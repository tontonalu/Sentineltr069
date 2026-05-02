// Package main — worker assíncrono do SentinelACS.
//
// Roles atuais:
//   - Sync periódico com GenieACS NBI (CP-2.4)
//
// Próximos roles (Fases 4-5):
//   - Telemetry collector (CP-4.3)
//   - Alert engine evaluator (CP-5.3)
//   - Provisioning queue consumer (CP-3.5)
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	appinventory "github.com/celinet/sentinel-acs/internal/application/inventory"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	pgdb "github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	rds "github.com/celinet/sentinel-acs/internal/infrastructure/redis"
	"github.com/celinet/sentinel-acs/internal/platform/config"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

var version = "dev"

// Configurações do tick. Se virar pesado, mover para config.yaml.
const (
	syncInterval     = 1 * time.Minute
	syncTimeout      = 5 * time.Minute
	offlineThreshold = 30 * time.Minute
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(log)
	log.Info("sentinel-worker starting", "version", version, "env", cfg.App.Env)

	bootCtx, bootCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bootCancel()

	pgPool, err := pgdb.New(bootCtx, cfg.Postgres.URL)
	if err != nil {
		return err
	}
	defer pgPool.Close()

	redisClient, err := rds.New(bootCtx, cfg.Redis.URL)
	if err != nil {
		// Worker não depende de Redis ainda (event bus virá na Fase 3).
		log.Warn("redis indisponível", "err", err)
	} else {
		defer func() { _ = redisClient.Close() }()
	}

	genieClient := genieacs.New(cfg.GenieACS.NBIUrl, cfg.GenieACS.AuthUser, cfg.GenieACS.AuthPass)

	// Repositórios
	deviceRepo := pgdb.NewDeviceRepo(pgPool)
	customerRepo := pgdb.NewCustomerRepo(pgPool)
	vendorRepo := pgdb.NewVendorRepo(pgPool)
	modelRepo := pgdb.NewDeviceModelRepo(pgPool)

	syncSvc := appinventory.NewSyncService(
		deviceRepo, customerRepo, vendorRepo, modelRepo,
		genieClient, offlineThreshold,
	)

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Primeira execução imediata pra não esperar 1 minuto após o boot.
	runSyncOnce(rootCtx, syncSvc, log)

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			runSyncOnce(rootCtx, syncSvc, log)
		case sig := <-stop:
			log.Info("shutdown signal received", "signal", sig.String())
			cancel()
			return nil
		}
	}
}

// runSyncOnce executa um Tick com timeout dedicado. Erros não derrubam o worker —
// apenas viram log + métrica (futuro Prometheus).
func runSyncOnce(ctx context.Context, svc *appinventory.SyncService, log *slog.Logger) {
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	if _, err := svc.Tick(syncCtx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("sync tick failed", "err", err)
	}
}
