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
	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	pgdb "github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	rds "github.com/celinet/sentinel-acs/internal/infrastructure/redis"
	"github.com/celinet/sentinel-acs/internal/platform/config"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	"github.com/celinet/sentinel-acs/internal/platform/ratelimit"
	"github.com/celinet/sentinel-acs/internal/transport/http/handlers"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
)

// version é injetado via -ldflags em build (deploy/Dockerfile).
var version = "dev"

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
	if redisClient != nil {
		preauth = appidentity.NewPreauthStore(redisClient)
	}

	cookieSecure := cfg.App.Env == "production"

	authH := &handlers.AuthHandler{Login: loginSvc, Preauth: preauth, CookieSecure: cookieSecure}
	totpH := &handlers.TOTPHandler{Login: loginSvc, TOTP: totpSvc, Preauth: preauth, CookieSecure: cookieSecure}
	adminUsersH := &handlers.AdminUsersHandler{Users: userRepo, Roles: roleRepo, Admin: adminSvc}
	devicesH := &handlers.DevicesHandler{
		Devices:   deviceRepo,
		Customers: customerRepo,
		Vendors:   vendorRepo,
		Models:    modelRepo,
		POPs:      popRepo,
		GenieACS:  genieClient,
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

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			user, _ := mw.UserFromContext(r.Context())
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if user != nil {
				_, _ = w.Write([]byte("<h1>SentinelACS</h1><p>Bem-vindo, " + user.FullName + "</p>"))
			} else {
				_, _ = w.Write([]byte("<h1>SentinelACS</h1>"))
			}
		})

		// Settings — TOTP enroll
		if totpSvc != nil {
			r.Get("/settings/totp", totpH.EnrollPage)
			r.Post("/settings/totp", totpH.EnrollSubmit)
		}

		// Devices — leitura para todos autenticados com device.read.
		r.Route("/devices", func(r chi.Router) {
			r.Use(mw.RequirePermission("device", "read"))
			r.Get("/", devicesH.List)
			r.Get("/{id}", devicesH.Detail)

			// Ações — exigem permissões específicas.
			r.With(mw.RequirePermission("device", "connection_req")).
				Post("/{id}/connection-request", devicesH.ConnectionRequest)
			r.With(mw.RequirePermission("device", "reboot")).
				Post("/{id}/reboot", devicesH.Reboot)
		})

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

