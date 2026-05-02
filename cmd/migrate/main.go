// Package main — CLI de migrations + seed.
//
// Lê os arquivos SQL embutidos no binário (ver assets.go na raiz) e aplica
// via goose. NÃO há .sql legível no FS do servidor — tudo dentro do binário.
//
// Uso:
//
//	migrate -cmd up           # aplica todas as pendentes
//	migrate -cmd down         # reverte a última
//	migrate -cmd status       # lista status
//	migrate -cmd redo         # reverte e reaplica a última (útil em dev)
//	migrate -cmd version      # versão atual aplicada
//	migrate -cmd seed         # cria/atualiza admin (usa SEED_ADMIN_EMAIL/PASSWORD/NAME)
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // driver database/sql para pgx
	"github.com/pressly/goose/v3"

	sa "github.com/celinet/sentinel-acs"
	appidentity "github.com/celinet/sentinel-acs/internal/application/identity"
	pgdb "github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	"github.com/celinet/sentinel-acs/internal/platform/config"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

const migrationsDir = "migrations"

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var cmd string
	flag.StringVar(&cmd, "cmd", "up", "up | down | status | version | redo | reset | seed")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(log)

	if cfg.Postgres.URL == "" {
		return errors.New("DATABASE_URL não definido")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cmd == "seed" {
		return doSeed(ctx, cfg.Postgres.URL, log)
	}

	return doMigrate(ctx, cmd, cfg.Postgres.URL, log)
}

func doMigrate(ctx context.Context, cmd, dsn string, log *slog.Logger) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("abrir db: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	goose.SetBaseFS(sa.MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("dialect: %w", err)
	}
	goose.SetLogger(gooseSlog{log: log})

	switch cmd {
	case "up":
		return goose.UpContext(ctx, db, migrationsDir)
	case "down":
		return goose.DownContext(ctx, db, migrationsDir)
	case "redo":
		return goose.RedoContext(ctx, db, migrationsDir)
	case "status":
		return goose.StatusContext(ctx, db, migrationsDir)
	case "version":
		v, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			return err
		}
		log.Info("current version", "version", v)
		return nil
	case "reset":
		// Use com extrema cautela — derruba TODAS as migrations.
		return goose.ResetContext(ctx, db, migrationsDir)
	default:
		return fmt.Errorf("comando desconhecido: %q", cmd)
	}
}

// doSeed cria (ou re-ativa) o usuário superadmin a partir de variáveis de
// ambiente. Idempotente — pode rodar a cada deploy sem efeitos colaterais.
func doSeed(ctx context.Context, dsn string, log *slog.Logger) error {
	email := os.Getenv("SEED_ADMIN_EMAIL")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	fullName := os.Getenv("SEED_ADMIN_NAME")
	if email == "" {
		email = "admin@local"
	}
	if fullName == "" {
		fullName = "Administrador"
	}
	if password == "" {
		return errors.New("SEED_ADMIN_PASSWORD obrigatório (mínimo 12 caracteres)")
	}

	pool, err := pgdb.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	users := pgdb.NewUserRepo(pool)
	roles := pgdb.NewRoleRepo(pool)
	asgs := pgdb.NewAssignmentRepo(pool)

	id, err := appidentity.SeedAdmin(ctx, users, roles, asgs, appidentity.SeedAdminInput{
		Email:    email,
		FullName: fullName,
		Password: password,
	})
	if err != nil {
		return err
	}
	log.Info("seed admin ok", "user_id", id, "email", email)
	return nil
}

// gooseSlog adapta slog ao logger esperado pelo goose.
type gooseSlog struct{ log *slog.Logger }

func (g gooseSlog) Fatalf(format string, v ...any) { g.log.Error(fmt.Sprintf(format, v...)); os.Exit(1) }
func (g gooseSlog) Printf(format string, v ...any) { g.log.Info(fmt.Sprintf(format, v...)) }
