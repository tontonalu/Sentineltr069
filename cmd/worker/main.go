// Package main — worker assíncrono do SentinelACS.
//
// Roles atuais:
//   - Sync periódico com GenieACS NBI (CP-2.4)
//   - Sync periódico com ERP Voalle (Fase 2.5)
//
// Próximos roles (Fases 4-5):
//   - Telemetry collector (CP-4.3)
//   - Alert engine evaluator (CP-5.3)
//
// Já implementados:
//   - Provisioning queue consumer (CP-3.5) — Redis Stream + polling fallback
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	appintegration "github.com/celinet/sentinel-acs/internal/application/integration"
	appinventory "github.com/celinet/sentinel-acs/internal/application/inventory"
	provapp "github.com/celinet/sentinel-acs/internal/application/provisioning"
	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	pgdb "github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	rds "github.com/celinet/sentinel-acs/internal/infrastructure/redis"
	"github.com/celinet/sentinel-acs/internal/platform/config"
	"github.com/celinet/sentinel-acs/internal/platform/logger"

	// Blank import para registrar o plugin Voalle no registry via init().
	_ "github.com/celinet/sentinel-acs/internal/infrastructure/erp/voalle"
)

var version = "dev"

const (
	syncInterval     = 1 * time.Minute
	syncTimeout      = 5 * time.Minute
	offlineThreshold = 30 * time.Minute

	erpSyncTimeout = 10 * time.Minute

	// Provisioning queue: stream block timeout + fallback polling cadence.
	provisioningBlock        = 5 * time.Second
	provisioningPollInterval = 30 * time.Second
	provisioningJobTimeout   = 90 * time.Second
	provisioningBatchSize    = 10
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
		log.Warn("redis indisponível", "err", err)
	} else {
		defer func() { _ = redisClient.Close() }()
	}

	genieClient := genieacs.New(cfg.GenieACS.NBIUrl, cfg.GenieACS.AuthUser, cfg.GenieACS.AuthPass)

	deviceRepo := pgdb.NewDeviceRepo(pgPool)
	customerRepo := pgdb.NewCustomerRepo(pgPool)
	vendorRepo := pgdb.NewVendorRepo(pgPool)
	modelRepo := pgdb.NewDeviceModelRepo(pgPool)
	jobRepo := pgdb.NewJobRepo(pgPool)
	batchRepo := pgdb.NewBatchRepo(pgPool)

	syncSvc := appinventory.NewSyncService(
		deviceRepo, customerRepo, vendorRepo, modelRepo,
		genieClient, offlineThreshold,
	)

	// Provisioning executor: consome jobs queued e empurra para o NBI.
	executor := provapp.NewExecutor(jobRepo, batchRepo, &deviceResolver{repo: deviceRepo}, genieClient)

	tracker := appintegration.NewStatusTracker()

	// Voalle (opcional): se configurado, sobe um ticker dedicado.
	var voalleSync *appintegration.ERPSyncService
	if cfg.Voalle.Enabled() {
		provider, err := erp.New("voalle", cfg.Voalle.AsMap())
		if err != nil {
			log.Error("plugin voalle falhou", "err", err)
		} else {
			voalleSync = appintegration.NewERPSyncService(provider, customerRepo, tracker)
			log.Info("voalle plugin habilitado",
				"slug", provider.Info().Slug,
				"sync_interval", cfg.Voalle.SyncIntervalDuration().String())
		}
	} else {
		log.Info("voalle plugin desabilitado (config vazia)")
	}

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Tickers
	genieTicker := time.NewTicker(syncInterval)
	defer genieTicker.Stop()

	// Voalle ticker — só ligamos se o plugin foi instanciado.
	var voalleTicker *time.Ticker
	if voalleSync != nil {
		voalleTicker = time.NewTicker(cfg.Voalle.SyncIntervalDuration())
		defer voalleTicker.Stop()
		// Primeira execução logo depois do GenieACS — assim na primeira volta
		// já temos customers para o link by PPPoE.
		go func() {
			time.Sleep(15 * time.Second)
			runVoalle(rootCtx, voalleSync, log)
		}()
	}

	// GenieACS sync imediato
	runGenie(rootCtx, syncSvc, log)

	// Provisioning queue: 1 goroutine consume stream + 1 ticker faz polling
	// fallback (cobre casos onde o publisher não conseguiu enfileirar).
	provDone := make(chan struct{}, 2)
	if redisClient != nil {
		go func() {
			defer func() { provDone <- struct{}{} }()
			runProvisioningStream(rootCtx, redisClient, executor, log)
		}()
	}
	go func() {
		defer func() { provDone <- struct{}{} }()
		runProvisioningPolling(rootCtx, executor, log)
	}()

	for {
		select {
		case <-genieTicker.C:
			runGenie(rootCtx, syncSvc, log)
		case <-tickC(voalleTicker):
			runVoalle(rootCtx, voalleSync, log)
		case sig := <-stop:
			log.Info("shutdown signal received", "signal", sig.String())
			cancel()
			// aguarda goroutines de provisioning finalizarem
			waitFor := time.After(10 * time.Second)
			for i := 0; i < 2; i++ {
				select {
				case <-provDone:
				case <-waitFor:
					return nil
				}
			}
			return nil
		}
	}
}

// ──────────── Provisioning queue consumer ────────────

// runProvisioningStream lê do Redis Stream provisioning.requested e dispara
// os jobs imediatamente. Mensagens consumidas são acked após RunByID retornar.
func runProvisioningStream(
	ctx context.Context, rdb rds.Client,
	exec *provapp.Executor, log *slog.Logger,
) {
	if err := rds.EnsureProvisioningGroup(ctx, rdb); err != nil {
		log.Error("ensure provisioning group", "err", err)
		// Sem o group, polling cobre — não é fatal.
	}
	consumer := fmt.Sprintf("worker-%d", os.Getpid())
	log.Info("provisioning stream consumer up", "consumer", consumer)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := rds.ConsumeProvisioning(ctx, rdb, consumer, provisioningBatchSize, provisioningBlock)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Error("provisioning stream read", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, m := range msgs {
			runOneProvisioning(ctx, exec, m.JobID, log)
			_ = rds.AckProvisioning(ctx, rdb, m.MessageID)
		}
	}
}

// runProvisioningPolling reclama jobs do banco a cada N segundos. Cobre o
// caso de Redis indisponível e jobs com retry agendado para o futuro.
func runProvisioningPolling(ctx context.Context, exec *provapp.Executor, log *slog.Logger) {
	t := time.NewTicker(provisioningPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runCtx, cancel := context.WithTimeout(ctx, provisioningJobTimeout)
			n, err := exec.RunOnce(runCtx, provisioningBatchSize)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Error("provisioning poll failed", "err", err)
			}
			if n > 0 {
				log.Info("provisioning batch processed", "count", n)
			}
		}
	}
}

func runOneProvisioning(ctx context.Context, exec *provapp.Executor, jobID uuid.UUID, log *slog.Logger) {
	runCtx, cancel := context.WithTimeout(ctx, provisioningJobTimeout)
	defer cancel()
	if err := exec.RunByID(runCtx, jobID); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("provisioning job failed", "job_id", jobID, "err", err)
	}
}

// deviceResolver — adapter mínimo entre o executor (que só precisa de
// genieacs_id) e o DeviceRepo do Postgres.
type deviceResolver struct{ repo *pgdb.DeviceRepo }

func (d *deviceResolver) ResolveGenieACSID(ctx context.Context, internalID uuid.UUID) (string, error) {
	dev, err := d.repo.GetByID(ctx, internalID)
	if err != nil {
		return "", err
	}
	return dev.GenieACSID, nil
}


// tickC retorna o canal do ticker, ou nil se for nil — select com canal nil
// nunca dispara, deixa a feature opt-out automática.
func tickC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func runGenie(ctx context.Context, svc *appinventory.SyncService, log *slog.Logger) {
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	if _, err := svc.Tick(syncCtx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("genieacs sync failed", "err", err)
	}
}

func runVoalle(ctx context.Context, svc *appintegration.ERPSyncService, log *slog.Logger) {
	if svc == nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, erpSyncTimeout)
	defer cancel()
	if _, err := svc.Tick(syncCtx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("voalle sync failed", "err", err)
	}
}
