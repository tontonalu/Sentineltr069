// Package main — worker assíncrono do SentinelACS.
//
// Roles atuais:
//   - Sync periódico com GenieACS NBI (CP-2.4)
//   - Sync periódico com ERP Voalle (Fase 2.5)
//   - Provisioning queue consumer (CP-3.5) — Redis Stream + polling fallback
//   - Telemetry collector (CP-4.3) — coleta a cada 5 min em chunks paralelos
//   - Alert engine evaluator (CP-5.3) — tick de 1 min, dispara WhatsApp/Telegram/SMTP
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

	aclapp "github.com/celinet/sentinel-acs/internal/application/acl"
	alertapp "github.com/celinet/sentinel-acs/internal/application/alerting"
	appintegration "github.com/celinet/sentinel-acs/internal/application/integration"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	appinventory "github.com/celinet/sentinel-acs/internal/application/inventory"
	provapp "github.com/celinet/sentinel-acs/internal/application/provisioning"
	teleapp "github.com/celinet/sentinel-acs/internal/application/telemetry"
	"github.com/celinet/sentinel-acs/internal/infrastructure/erp"
	"github.com/celinet/sentinel-acs/internal/infrastructure/notifier/smtp"
	"github.com/celinet/sentinel-acs/internal/infrastructure/notifier/telegram"
	"github.com/celinet/sentinel-acs/internal/infrastructure/notifier/whatsapp"
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

	// Telemetry collector: tick a cada 5 min (alinhado com §10.1 do doc).
	telemetryTickInterval = 5 * time.Minute
	telemetryTickTimeout  = 4 * time.Minute

	// Alerting engine: tick 1 min (CP-5.3). Avalia regras vs métricas atuais
	// e dispara notificações respeitando cooldown.
	alertTickInterval = 1 * time.Minute
	alertTickTimeout  = 45 * time.Second

	// Cleanup de snapshots de homologação: 1× ao dia, mantém últimos 30d.
	homCleanupInterval = 24 * time.Hour
	homSnapshotTTL     = 30 * 24 * time.Hour

	// ACL syncer: 30s entre ticks. Frequência alta porque a operação é
	// barata (read DB + write file) e o impacto de "ACL desatualizada na
	// 7547" é alto.
	aclSyncInterval = 30 * time.Second
	aclFilePath     = "/var/cwmp-acl/cidrs.txt"
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
	telemetryRepo := pgdb.NewTelemetryRepo(pgPool)

	syncSvc := appinventory.NewSyncService(
		deviceRepo, customerRepo, vendorRepo, modelRepo,
		genieClient, offlineThreshold,
	)

	// Provisioning executor: consome jobs queued e empurra para o NBI.
	executor := provapp.NewExecutor(jobRepo, batchRepo, &deviceResolver{repo: deviceRepo}, genieClient)

	// Homologation cleanup: zera tree_snapshot de sessões finalizadas há mais
	// de 30 dias. Snapshots viram 1-2 MB JSONB e crescem indefinidamente sem
	// purge. Mantém metadados (status, datas) e mappings — auditoria preserva.
	homSessionRepo := pgdb.NewHomologationSessionRepo(pgPool)

	// Telemetry collector: amostra devices online a cada 5 min e grava
	// nas hypertables. Usa cache do GenieACS (snapshot do último inform).
	collector := teleapp.NewCollector(deviceRepo, genieClient, telemetryRepo, teleapp.CollectorOptions{
		ChunkSize:        200,
		Parallel:         5,
		PerDeviceTimeout: 10 * time.Second,
		OnlineThreshold:  offlineThreshold,
	})

	// Alerting engine — repos + composite source (devices + telemetry) +
	// notifiers configurados via env. Notifiers desabilitados são `nil`
	// e ignorados pelo NewEngine.
	ruleRepo := pgdb.NewRuleRepo(pgPool)
	alertRepo := pgdb.NewAlertRepo(pgPool)
	notifRepo := pgdb.NewNotificationRepo(pgPool)
	source := alertapp.NewCompositeSource(deviceRepo, telemetryRepo)

	notifiers := buildNotifiers(cfg, log)
	alertEngine := alertapp.NewEngine(ruleRepo, alertRepo, notifRepo, source, notifiers...)

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

	// ACL syncer — escreve a lista de CIDRs autorizados num arquivo
	// montado do host. O systemd path-unit no host detecta a mudança e
	// reconcilia iptables. Roda em goroutine própria para não competir
	// com os tickers de sync/telemetria/alerting.
	aclRepo := pgdb.NewTR069ACLRepo(pgPool)
	aclSyncer := aclapp.NewSyncer(aclRepo, aclFilePath)
	go aclSyncer.Run(rootCtx, aclSyncInterval)

	// Tickers
	genieTicker := time.NewTicker(syncInterval)
	defer genieTicker.Stop()

	telemetryTicker := time.NewTicker(telemetryTickInterval)
	defer telemetryTicker.Stop()

	alertTicker := time.NewTicker(alertTickInterval)
	defer alertTicker.Stop()

	// Cleanup de snapshots de homologação: 1× a cada 24h. Roda imediatamente
	// no boot (catch-up se worker ficou off) e depois no ticker.
	homCleanupTicker := time.NewTicker(homCleanupInterval)
	defer homCleanupTicker.Stop()
	go runHomologationCleanup(rootCtx, homSessionRepo, log)

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
		case <-telemetryTicker.C:
			runTelemetry(rootCtx, collector, log)
		case <-alertTicker.C:
			runAlertEngine(rootCtx, alertEngine, log)
		case <-homCleanupTicker.C:
			runHomologationCleanup(rootCtx, homSessionRepo, log)
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

// buildNotifiers instancia adapters habilitados via config. Cada um pode
// estar ausente — o engine pula canais sem notifier configurado.
func buildNotifiers(cfg *config.Config, log *slog.Logger) []alertapp.Notifier {
	var out []alertapp.Notifier
	if cfg.Notifier.WhatsApp.BaseURL != "" {
		out = append(out, whatsapp.New(whatsapp.Config{
			BaseURL:  cfg.Notifier.WhatsApp.BaseURL,
			APIKey:   cfg.Notifier.WhatsApp.APIKey,
			Instance: cfg.Notifier.WhatsApp.Instance,
		}))
		log.Info("whatsapp notifier habilitado", "base_url", cfg.Notifier.WhatsApp.BaseURL)
	}
	if cfg.Notifier.Telegram.BotToken != "" {
		out = append(out, telegram.New(telegram.Config{
			BotToken: cfg.Notifier.Telegram.BotToken,
		}))
		log.Info("telegram notifier habilitado")
	}
	if cfg.Notifier.SMTP.Host != "" {
		out = append(out, smtp.New(smtp.Config{
			Host:        cfg.Notifier.SMTP.Host,
			Port:        cfg.Notifier.SMTP.PortNum(),
			Username:    cfg.Notifier.SMTP.Username,
			Password:    cfg.Notifier.SMTP.Password,
			FromAddress: cfg.Notifier.SMTP.FromAddress,
			FromName:    cfg.Notifier.SMTP.FromName,
		}))
		log.Info("smtp notifier habilitado", "host", cfg.Notifier.SMTP.Host)
	}
	if len(out) == 0 {
		log.Warn("nenhum notifier configurado — alertas serão criados mas não enviados")
	}
	return out
}

func runAlertEngine(ctx context.Context, e *alertapp.Engine, log *slog.Logger) {
	if e == nil {
		return
	}
	tCtx, cancel := context.WithTimeout(ctx, alertTickTimeout)
	defer cancel()
	res, err := e.Tick(tCtx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("alert tick failed", "err", err)
		return
	}
	if res.AlertsFired > 0 || res.AlertsResolved > 0 || res.Errors > 0 {
		log.Info("alert tick",
			"rules", res.RulesEvaluated,
			"fired", res.AlertsFired,
			"resolved", res.AlertsResolved,
			"notifications_ok", res.NotificationsOK,
			"errors", res.Errors,
			"duration", res.Duration.String(),
		)
	}
}

// runHomologationCleanup zera tree_snapshot de sessões finalizadas há mais
// de homSnapshotTTL. Best-effort: erro só loga, não derruba o worker.
func runHomologationCleanup(ctx context.Context, sessions hom.SessionRepo, log *slog.Logger) {
	cutoff := time.Now().Add(-homSnapshotTTL)
	tickCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	n, err := sessions.PurgeOldSnapshots(tickCtx, cutoff)
	if err != nil {
		log.Error("homologation cleanup", "err", err)
		return
	}
	if n > 0 {
		log.Info("homologation cleanup", "purged_snapshots", n, "before", cutoff.Format(time.RFC3339))
	}
}

func runTelemetry(ctx context.Context, c *teleapp.Collector, log *slog.Logger) {
	if c == nil {
		return
	}
	tCtx, cancel := context.WithTimeout(ctx, telemetryTickTimeout)
	defer cancel()
	res, err := c.Tick(tCtx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("telemetry tick failed", "err", err)
		return
	}
	log.Info("telemetry tick",
		"devices", res.Devices,
		"wifi_samples", res.WifiSamples,
		"wan_samples", res.WanSamples,
		"system_samples", res.SystemSamples,
		"errors", res.Errors,
		"duration", res.Duration.String(),
	)
}
