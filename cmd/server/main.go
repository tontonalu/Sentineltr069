// Package main — binário web + API do SentinelACS.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	sa "github.com/celinet/sentinel-acs"

	// blank import força o registro do plugin Voalle (e futuros) no registry global.
	_ "github.com/celinet/sentinel-acs/internal/infrastructure/erp/voalle"
	homapp "github.com/celinet/sentinel-acs/internal/application/homologation"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	appinventory "github.com/celinet/sentinel-acs/internal/application/inventory"
	provapp "github.com/celinet/sentinel-acs/internal/application/provisioning"
	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	pgdb "github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	rds "github.com/celinet/sentinel-acs/internal/infrastructure/redis"
	"github.com/celinet/sentinel-acs/internal/platform/config"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	"github.com/celinet/sentinel-acs/internal/platform/ratelimit"
	"github.com/celinet/sentinel-acs/internal/transport/http/handlers"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	"github.com/celinet/sentinel-acs/internal/views/layouts"
)

// version é injetado via -ldflags em build (deploy/Dockerfile).
var version = "dev"

// offlineThreshold deve casar com o valor usado no worker (cmd/worker/main.go).
// Mantemos local para evitar acoplamento entre os dois binários.
const offlineThreshold = 30 * time.Minute

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
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
	log.Info("sentinel-acs starting", "version", version, "env", cfg.App.Env, "port", cfg.App.Port)

	// AssetVersion vira o sufixo ?v= em /static/css/app.css e /static/js/*.js
	// para invalidar cache do browser a cada deploy (Cache-Control: immutable
	// não recheca por 1 ano sem mudança de URL).
	layouts.AssetVersion = version

	bootCtx, bootCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bootCancel()

	pgPool, err := pgdb.New(bootCtx, cfg.Postgres.URL)
	if err != nil {
		log.Warn("postgres não conectado na inicialização", "err", err)
	}
	redisClient, err := rds.New(bootCtx, cfg.Redis.URL)
	if err != nil {
		log.Warn("redis não conectado na inicialização", "err", err)
	}
	genieClient := genieacs.New(cfg.GenieACS.NBIUrl, cfg.GenieACS.AuthUser, cfg.GenieACS.AuthPass)
	if redisClient != nil {
		// CP-2.5: cache de GetDevice com TTL 30s — writes invalidam automaticamente.
		genieClient.WithCache(redisClient, 30*time.Second)
	}

	// SecretBox para cifragem de TOTP/credenciais — chave é OBRIGATÓRIA em prod.
	var secretBox *crypto.SecretBox
	if cfg.App.AgeKeyFile != "" {
		key, err := crypto.LoadKeyFromFile(cfg.App.AgeKeyFile)
		if err != nil {
			if cfg.App.Env == "production" {
				return err
			}
			log.Warn("não foi possível carregar age key — TOTP/integrações cifradas vão falhar", "err", err)
		} else {
			secretBox, err = crypto.NewSecretBox(key)
			if err != nil {
				return err
			}
		}
	} else if cfg.App.Env == "production" {
		return errors.New("APP_AGE_KEY_FILE obrigatório em produção")
	}

	// ──────────── Repositórios + Services (DI manual) ────────────
	var (
		userRepo    *pgdb.UserRepo
		sessionRepo *pgdb.SessionRepo
		assignRepo  *pgdb.AssignmentRepo
		roleRepo    *pgdb.RoleRepo
		loginSvc    *appidentity.LoginService
		totpSvc     *appidentity.TOTPService
		adminSvc    *appidentity.AdminService
		preauth     *appidentity.PreauthStore

		// Inventário
		deviceRepo   *pgdb.DeviceRepo
		customerRepo *pgdb.CustomerRepo
		vendorRepo   *pgdb.VendorRepo
		modelRepo    *pgdb.DeviceModelRepo
		popRepo      *pgdb.POPRepo
	)
	if pgPool != nil {
		userRepo = pgdb.NewUserRepo(pgPool)
		sessionRepo = pgdb.NewSessionRepo(pgPool)
		assignRepo = pgdb.NewAssignmentRepo(pgPool)
		roleRepo = pgdb.NewRoleRepo(pgPool)
		loginSvc = appidentity.NewLoginService(userRepo, sessionRepo)
		adminSvc = appidentity.NewAdminService(userRepo, roleRepo, assignRepo)
		if secretBox != nil {
			totpSvc = appidentity.NewTOTPService(userRepo, secretBox, "SentinelACS")
		}

		deviceRepo = pgdb.NewDeviceRepo(pgPool)
		customerRepo = pgdb.NewCustomerRepo(pgPool)
		vendorRepo = pgdb.NewVendorRepo(pgPool)
		modelRepo = pgdb.NewDeviceModelRepo(pgPool)
		popRepo = pgdb.NewPOPRepo(pgPool)
	}

	// TR-069 Provisioning Config (Settings · Provisionamento) — singleton
	// que guarda a URL CWMP exposta aos CPEs e o syncer com o GenieACS.
	var (
		provConfigRepo *pgdb.ProvisioningConfigRepo
		provSyncer     *provapp.Syncer
		tr069ACLRepo   *pgdb.TR069ACLRepo
	)
	if pgPool != nil {
		provConfigRepo = pgdb.NewProvisioningConfigRepo(pgPool)
		provSyncer = provapp.NewSyncer(provConfigRepo, genieClient)
		tr069ACLRepo = pgdb.NewTR069ACLRepo(pgPool)
	}

	// Templates & Provisioning (Fase 3) — service requer pool, mas a UI/API só
	// é montada se pgPool estiver disponível.
	var (
		profileRepo  *pgdb.ProfileRepo
		paramRepo    *pgdb.ParameterRepo
		historyRepo  *pgdb.ProfileHistoryRepo
		jobRepo      *pgdb.JobRepo
		batchRepo    *pgdb.BatchRepo
		tplService   *tplapp.Service
		provService  *provapp.Service
		homService   *homapp.Service
		jobNotifier  *rds.JobNotifier
	)
	if pgPool != nil {
		profileRepo = pgdb.NewProfileRepo(pgPool)
		paramRepo = pgdb.NewParameterRepo(pgPool)
		historyRepo = pgdb.NewProfileHistoryRepo(pgPool)
		jobRepo = pgdb.NewJobRepo(pgPool)
		batchRepo = pgdb.NewBatchRepo(pgPool)
		tplService = tplapp.NewService(profileRepo, paramRepo, historyRepo)

		// Homologation service usa todas as deps de templates + GenieACS NBI.
		// Wizard delega Create do profile final ao tplService — sem duplicação.
		homSessionRepo := pgdb.NewHomologationSessionRepo(pgPool)
		homMappingRepo := pgdb.NewHomologationMappingRepo(pgPool)
		homCanonicalRepo := pgdb.NewCanonicalKeyRepo(pgPool)
		homModelRepo := pgdb.NewModelHomologationRepo(pgPool)
		homService = homapp.NewService(
			homSessionRepo, homMappingRepo, homCanonicalRepo, homModelRepo,
			deviceRepo, modelRepo, tplService, genieClient,
		)

		// Notifier é interface — se Redis falta, passa nil interface (não
		// typed-nil) para o `if notify != nil` no service ficar correto.
		var notifier provapp.Notifier
		if redisClient != nil {
			jobNotifier = rds.NewJobNotifier(redisClient)
			notifier = jobNotifier
		}
		provService = provapp.NewService(
			tplapp.NewEngine(),
			tplService,
			deviceRepo, customerRepo, popRepo,
			jobRepo, batchRepo, notifier,
		).WithHomologationGate(homModelRepo)
	}
	if redisClient != nil {
		preauth = appidentity.NewPreauthStore(redisClient)
	}

	cookieSecure := cfg.App.Env == "production"

	authH := &handlers.AuthHandler{Login: loginSvc, Preauth: preauth, CookieSecure: cookieSecure}
	totpH := &handlers.TOTPHandler{Login: loginSvc, TOTP: totpSvc, Preauth: preauth, CookieSecure: cookieSecure}
	adminUsersH := &handlers.AdminUsersHandler{Users: userRepo, Roles: roleRepo, Admin: adminSvc}

	// SyncService — exposto também no servidor HTTP para o botão "Sincronizar
	// agora" em /devices. O worker mantém o tick periódico em paralelo.
	var syncSvc *appinventory.SyncService
	if pgPool != nil {
		syncSvc = appinventory.NewSyncService(
			deviceRepo, customerRepo, vendorRepo, modelRepo,
			genieClient, offlineThreshold,
		)
	}

	devicesH := &handlers.DevicesHandler{
		Devices:   deviceRepo,
		Customers: customerRepo,
		Vendors:   vendorRepo,
		Models:    modelRepo,
		POPs:      popRepo,
		GenieACS:  genieClient,
		SyncSvc:   syncSvc,
	}

	settingsPOPsH := &handlers.SettingsPOPsHandler{POPs: popRepo}
	settingsVendorsH := &handlers.SettingsVendorsHandler{Vendors: vendorRepo}
	// homModelRepoForView é usado em vários handlers — declarado logo antes
	// do primeiro consumidor (settingsModelsH) e reusado nos demais abaixo.
	var homModelRepoForView hom.ModelHomologationRepo
	if pgPool != nil {
		homModelRepoForView = pgdb.NewModelHomologationRepo(pgPool)
	}

	settingsModelsH := &handlers.SettingsModelsHandler{
		Models:    modelRepo,
		Vendors:   vendorRepo,
		HomModel:  homModelRepoForView,
		Templates: tplService,
	}
	var settingsProvH *handlers.SettingsProvisioningHandler
	if provConfigRepo != nil {
		settingsProvH = &handlers.SettingsProvisioningHandler{Configs: provConfigRepo, Syncer: provSyncer}
	}

	var settingsTR069ACLH *handlers.SettingsTR069ACLHandler
	if tr069ACLRepo != nil {
		settingsTR069ACLH = &handlers.SettingsTR069ACLHandler{ACL: tr069ACLRepo}
	}

	var settingsCKH *handlers.SettingsCanonicalKeysHandler
	if pgPool != nil {
		settingsCKH = &handlers.SettingsCanonicalKeysHandler{Keys: pgdb.NewCanonicalKeyRepo(pgPool)}
	}

	templatesH := &handlers.TemplatesHandler{
		Service:  tplService,
		Profiles: profileRepo,
		History:  historyRepo,
		Vendors:  vendorRepo,
		Models:   modelRepo,
		HomModel: homModelRepoForView,
	}
	provH := &handlers.ProvisioningHandler{
		Service: provService,
		Jobs:    jobRepo,
		Batches: batchRepo,
		Devices: deviceRepo,
	}

	var homH *handlers.HomologationHandler
	if homService != nil {
		homH = &handlers.HomologationHandler{
			Service:   homService,
			Devices:   deviceRepo,
			Models:    modelRepo,
			Vendors:   vendorRepo,
			HomModel:  homModelRepoForView,
			Templates: tplService,
		}
	}

	// Telemetria — repo Postgres (TimescaleDB hypertables).
	var telemetryRepo *pgdb.TelemetryRepo
	var historyH *handlers.HistoryHandler
	if pgPool != nil {
		telemetryRepo = pgdb.NewTelemetryRepo(pgPool)
		historyH = &handlers.HistoryHandler{Devices: deviceRepo, Telemetry: telemetryRepo}
	}

	// Alertas — repos + handler. Engine roda no worker, não aqui.
	var (
		alertsH   *handlers.AlertsHandler
		alertRepo *pgdb.AlertRepo // hoist para reuso pelo dashboard
	)
	if pgPool != nil {
		ruleRepo := pgdb.NewRuleRepo(pgPool)
		alertRepo = pgdb.NewAlertRepo(pgPool)
		alertsH = &handlers.AlertsHandler{Rules: ruleRepo, Alerts: alertRepo}
	}

	// Integrações — server só mostra status/config; sync acontece no worker.
	enabledPlugins := map[string]handlers.EnabledPlugin{}
	if cfg.Voalle.Enabled() {
		enabledPlugins["voalle"] = handlers.EnabledPlugin{
			BaseURL:      cfg.Voalle.BaseURL,
			SyncInterval: cfg.Voalle.SyncIntervalDuration().String(),
		}
	}
	integrationsH := &handlers.IntegrationsHandler{EnabledPlugins: enabledPlugins}

	// Dashboard — home autenticada. Reusa repos já construídos; nil tolerado.
	dashboardH := &handlers.DashboardHandler{
		Devices:  deviceRepo,
		Alerts:   alertRepo,
		Jobs:     jobRepo,
		Batches:  batchRepo,
		Postgres: pgPool,
		Redis:    redisClient,
		GenieACS: genieClient,
	}

	authDeps := mw.AuthDeps{Login: loginSvc, Assignments: assignRepo, LoginURL: "/login"}

	var limiter *ratelimit.Limiter
	if redisClient != nil {
		limiter = ratelimit.New(redisClient)
	}

	// ──────────── Roteador ────────────
	r := chi.NewRouter()
	r.Use(mw.Correlation)
	r.Use(mw.Recoverer)
	r.Use(mw.Logger)
	r.Use(mw.CSRF(cookieSecure))

	// Healthz é público — não passa por auth.
	r.Get("/healthz", handlers.Healthz(handlers.HealthDeps{
		Version:  version,
		Postgres: pgPool,
		Redis:    redisClient,
		GenieACS: genieClient,
	}))

	// Static — público e cacheável.
	staticFS, err := fs.Sub(sa.StaticFS, "web/static")
	if err != nil {
		return err
	}
	r.Handle("/static/*", http.StripPrefix("/static/",
		cacheControl("public, max-age=31536000, immutable")(http.FileServerFS(staticFS))))

	// ──────────── Auth (rotas públicas com rate limit) ────────────
	if loginSvc != nil {
		// 10 tentativas por IP em janela de 5 min para login com senha;
		// 5 tentativas por IP em 5 min para verify TOTP.
		loginRL := mw.RateLimit(mw.RateLimitConfig{
			Limiter:  limiter,
			KeyFn:    mw.KeyByIP("login"),
			Limit:    10,
			Window:   5 * time.Minute,
			FailOpen: true,
			Message:  "muitas tentativas de login, aguarde alguns minutos",
		})
		totpRL := mw.RateLimit(mw.RateLimitConfig{
			Limiter:  limiter,
			KeyFn:    mw.KeyByIP("totp"),
			Limit:    5,
			Window:   5 * time.Minute,
			FailOpen: true,
			Message:  "muitas tentativas de TOTP, aguarde alguns minutos",
		})

		r.Get("/login", authH.LoginPage)
		r.With(loginRL).Post("/login", authH.LoginSubmit)
		r.Get("/logout", authH.Logout)
		r.Post("/logout", authH.Logout)

		if totpSvc != nil && preauth != nil {
			r.Get("/login/totp", totpH.LoginTOTPPage)
			r.With(totpRL).Post("/login/totp", totpH.LoginTOTPSubmit)
		}
	}

	// ──────────── Rotas protegidas ────────────
	r.Group(func(r chi.Router) {
		if loginSvc != nil {
			r.Use(mw.RequireAuth(authDeps))
		}
		// NavContext injeta r.URL.Path no contexto pra que layouts.Base possa
		// marcar o link ativo na sidebar sem mudar a assinatura Base(title).
		r.Use(mw.NavContext)

		r.Get("/", dashboardH.Index)

		// Settings — landing redireciona para a primeira aba (TOTP).
		r.Get("/settings", func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "/settings/totp", http.StatusSeeOther)
		})
		if totpSvc != nil {
			r.Get("/settings/totp", totpH.EnrollPage)
			r.Post("/settings/totp", totpH.EnrollSubmit)
		}

		// Settings · POPs — exige pop.manage (já no superadmin via migration 002).
		r.Route("/settings/pops", func(r chi.Router) {
			r.Use(mw.RequirePermission("pop", "manage"))
			r.Get("/", settingsPOPsH.List)
			r.Get("/new", settingsPOPsH.NewForm)
			r.Post("/", settingsPOPsH.Create)
			r.Get("/{id}/edit", settingsPOPsH.EditForm)
			r.Post("/{id}", settingsPOPsH.Update)
			r.Post("/{id}/toggle", settingsPOPsH.ToggleActive)
		})

		// Settings · Vendors e Modelos — vendor.manage cobre ambos por design da migration.
		r.Route("/settings/vendors", func(r chi.Router) {
			r.Use(mw.RequirePermission("vendor", "manage"))
			r.Get("/", settingsVendorsH.List)
			r.Get("/new", settingsVendorsH.NewForm)
			r.Post("/", settingsVendorsH.Create)
			r.Get("/{id}/edit", settingsVendorsH.EditForm)
			r.Post("/{id}", settingsVendorsH.Update)
		})
		r.Route("/settings/models", func(r chi.Router) {
			r.Use(mw.RequirePermission("vendor", "manage"))
			r.Get("/", settingsModelsH.List)
			r.Get("/new", settingsModelsH.NewForm)
			r.Post("/", settingsModelsH.Create)
			// Página somente-leitura: lista profiles homologados pro modelo.
			// Permissão homologation.read garante que mesmo viewer veja.
			r.With(mw.RequirePermission("homologation", "read")).
				Get("/{id}/homologations", settingsModelsH.Homologations)
		})

		// Settings · Provisionamento — config TR-069/CWMP (URL ACS, Inform,
		// credenciais default) + sync com GenieACS via NBI.
		if settingsProvH != nil {
			r.Route("/settings/provisioning", func(r chi.Router) {
				r.Use(mw.RequirePermission("provisioning_config", "read"))
				r.Get("/", settingsProvH.Show)
				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("provisioning_config", "manage"))
					r.Post("/", settingsProvH.Update)
					r.Post("/sync", settingsProvH.Sync)
				})
			})
		}

		// Settings · TR-069 ACL — lista de CIDRs autorizados a falar com a
		// porta CWMP. Persistido aqui; enforcement no kernel é feito por um
		// reconciler separado (worker → file → systemd path-unit → iptables).
		if settingsTR069ACLH != nil {
			r.Route("/settings/tr069-acl", func(r chi.Router) {
				r.Use(mw.RequirePermission("tr069_acl", "read"))
				r.Get("/", settingsTR069ACLH.Show)
				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("tr069_acl", "manage"))
					r.Post("/", settingsTR069ACLH.Create)
					r.Post("/{id}/delete", settingsTR069ACLH.Delete)
				})
			})
		}

		// Settings · Canonical keys — catálogo padronizado para profiles
		// e auto-mapeamento. Read = template.read, mutações = template.manage.
		if settingsCKH != nil {
			r.Route("/settings/canonical-keys", func(r chi.Router) {
				r.Use(mw.RequirePermission("template", "read"))
				r.Get("/", settingsCKH.List)
				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("template", "manage"))
					r.Post("/", settingsCKH.Create)
					r.Post("/{id}/delete", settingsCKH.Delete)
				})
			})
		}

		// Devices — leitura para todos autenticados com device.read.
		r.Route("/devices", func(r chi.Router) {
			r.Use(mw.RequirePermission("device", "read"))
			r.Get("/", devicesH.List)
			r.Get("/{id}", devicesH.Detail)

			// Sincronização manual com GenieACS — exige integration.manage.
			if syncSvc != nil {
				r.With(mw.RequirePermission("integration", "manage")).
					Post("/sync", devicesH.Sync)
			}

			// Histórico — exige telemetry.read.
			if historyH != nil {
				r.With(mw.RequirePermission("telemetry", "read")).
					Get("/{id}/history", historyH.History)
			}

			// Ações — exigem permissões específicas.
			r.With(mw.RequirePermission("device", "connection_req")).
				Post("/{id}/connection-request", devicesH.ConnectionRequest)
			r.With(mw.RequirePermission("device", "reboot")).
				Post("/{id}/reboot", devicesH.Reboot)
			r.With(mw.RequirePermission("device", "delete")).
				Post("/{id}/delete", devicesH.Delete)
			r.With(mw.RequirePermission("homologation", "run")).
				Post("/{id}/mark-lab", devicesH.MarkLab)
			r.With(mw.RequirePermission("homologation", "run")).
				Post("/{id}/set-model", devicesH.SetModel)

			// Aplicar profile a este device — exige provisioning.apply.
			if provService != nil {
				r.With(mw.RequirePermission("provisioning", "apply")).
					Post("/{id}/templates/{profileID}/preview", provH.PreviewToDevice)
				r.With(mw.RequirePermission("provisioning", "apply")).
					Post("/{id}/templates/{profileID}/apply", provH.ApplyToDevice)
			}
		})

		// Templates — CRUD de profiles. Read para todos com template.read,
		// mutações exigem template.manage.
		if tplService != nil {
			r.Route("/templates", func(r chi.Router) {
				r.With(mw.RequirePermission("template", "read")).Get("/", templatesH.List)
				r.With(mw.RequirePermission("template", "read")).Get("/{id}", templatesH.Detail)

				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("template", "manage"))
					r.Get("/new", templatesH.NewForm)
					r.Post("/", templatesH.Create)
					r.Get("/{id}/edit", templatesH.EditForm)
					r.Post("/{id}", templatesH.Update)
				})

				if provService != nil {
					r.With(mw.RequirePermission("provisioning", "apply_bulk")).
						Post("/{id}/apply-bulk", provH.ApplyBulk)
				}
			})
		}

		// Provisioning — fila e batches.
		if provService != nil {
			r.Route("/provisioning", func(r chi.Router) {
				r.Use(mw.RequirePermission("provisioning", "read"))
				r.Get("/jobs", provH.JobsList)
				r.Get("/jobs/{id}", provH.JobDetail)
				r.Get("/batches/{id}", provH.BatchDetail)
				r.With(mw.RequirePermission("provisioning", "approve")).
					Post("/batches/{id}/approve", provH.ApproveBatch)
			})
		}

		// Homologação — wizard guiado para gerar profiles testados.
		if homH != nil {
			r.Route("/homologation", func(r chi.Router) {
				r.Use(mw.RequirePermission("homologation", "read"))
				r.Get("/", homH.List)
				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("homologation", "run"))
					r.Post("/sessions", homH.Create)
					r.Get("/sessions/{id}", homH.Wizard)
					r.Post("/sessions/{id}/probe", homH.Probe)
				r.Post("/sessions/{id}/reset-probe", homH.ResetProbe)
					r.Post("/sessions/{id}/automap", homH.AutoMap)
					r.Post("/sessions/{id}/mappings", homH.AddMapping)
					r.Post("/sessions/{id}/mappings/{mid}/delete", homH.RemoveMapping)
					r.Post("/sessions/{id}/mappings/{mid}/template", homH.UpdateMappingTemplate)
					r.Post("/sessions/{id}/mappings/{mid}/test-read", homH.TestRead)
					r.Post("/sessions/{id}/mappings/{mid}/test-write", homH.TestWrite)
					r.Post("/sessions/{id}/abandon", homH.Abandon)
					r.With(mw.RequirePermission("homologation", "approve")).
						Post("/sessions/{id}/complete", homH.Complete)
					r.With(mw.RequirePermission("homologation", "approve")).
						Post("/model-homologations/{id}/deprecate", homH.Deprecate)
				})
			})
		}

		// Alertas — listagem + CRUD de regras + ack/resolve.
		if alertsH != nil {
			r.Route("/alerts", func(r chi.Router) {
				r.With(mw.RequirePermission("alert", "read")).Get("/", alertsH.List)

				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("alert", "manage"))
					r.Get("/rules/new", alertsH.NewForm)
					r.Post("/rules", alertsH.Create)
					r.Get("/rules/{id}/edit", alertsH.EditForm)
					r.Post("/rules/{id}", alertsH.Update)
				})

				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("alert", "acknowledge"))
					r.Post("/{id}/ack", alertsH.Acknowledge)
					r.Post("/{id}/resolve", alertsH.Resolve)
				})
			})
		}

		// Integrações — leitura por todos com integration.manage (apenas superadmin por padrão).
		r.With(mw.RequirePermission("integration", "manage")).Get("/integrations", integrationsH.List)

		// Admin — leitura precisa user.read; mutações precisam user.write.
		r.Route("/admin", func(r chi.Router) {
			r.Route("/users", func(r chi.Router) {
				r.With(mw.RequirePermission("user", "read")).Get("/", adminUsersH.List)
				r.With(mw.RequirePermission("user", "read")).Get("/{id}", adminUsersH.Detail)

				r.Group(func(r chi.Router) {
					r.Use(mw.RequirePermission("user", "write"))
					r.Get("/new", adminUsersH.NewForm)
					r.Post("/", adminUsersH.Create)
					r.Post("/{id}/toggle", adminUsersH.ToggleActive)
					r.Post("/{id}/roles", adminUsersH.AssignRole)
					r.Post("/{id}/roles/{role_id}/revoke", adminUsersH.RevokeRole)
				})
			})
		})
	})

	srv := &http.Server{
		Addr:              ":" + cfg.App.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.Shutdown())
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	if pgPool != nil {
		pgPool.Close()
	}
	if redisClient != nil {
		_ = redisClient.Close()
	}
	log.Info("bye")
	return nil
}

// cacheControl fixa um header Cache-Control nas respostas. Aplicado em
// /static/* — Tailwind gera o CSS com hash, então cache longo é seguro.
func cacheControl(value string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", value)
			next.ServeHTTP(w, r)
		})
	}
}

