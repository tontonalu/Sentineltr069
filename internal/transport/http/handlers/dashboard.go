package handlers

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	alerting "github.com/celinet/sentinel-acs/internal/domain/alerting"
	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	pgdb "github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	rds "github.com/celinet/sentinel-acs/internal/infrastructure/redis"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	dashboardpage "github.com/celinet/sentinel-acs/internal/views/pages/dashboard"
)

// DashboardHandler renderiza a home autenticada (/). Cada widget é gateado
// por permissão; nil repos são tolerados (mostra "—" no card correspondente).
type DashboardHandler struct {
	Devices *pgdb.DeviceRepo
	Alerts  *pgdb.AlertRepo
	Jobs    *pgdb.JobRepo
	Batches *pgdb.BatchRepo

	// Health checks — espelham HealthDeps mas sem importar handlers de si mesmo.
	Postgres pgdb.Pool
	Redis    rds.Client
	GenieACS *genieacs.Client
}

func (h *DashboardHandler) Index(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	user, _ := mw.UserFromContext(ctx)
	data := dashboardpage.DashboardData{
		GeneratedAt: time.Now(),
		Permissions: dashboardpage.PermissionFlags{
			CanReadDevices: mw.UserHasPermission(ctx, "device", "read"),
			CanReadAlerts:  mw.UserHasPermission(ctx, "alert", "read"),
			CanReadJobs:    mw.UserHasPermission(ctx, "provisioning", "read"),
			CanAckAlerts:   mw.UserHasPermission(ctx, "alert", "acknowledge"),
		},
	}
	if user != nil {
		data.UserName = user.FullName
	}

	g, gctx := errgroup.WithContext(ctx)
	since := time.Now().Add(-24 * time.Hour)

	if data.Permissions.CanReadDevices && h.Devices != nil {
		g.Go(func() error {
			counts, err := h.Devices.CountByStatus(gctx)
			if err != nil {
				return err
			}
			data.DeviceCounts = mapToDeviceCounts(counts)
			return nil
		})
	}

	if data.Permissions.CanReadAlerts && h.Alerts != nil {
		g.Go(func() error {
			counts, err := h.Alerts.CountOpenBySeverity(gctx)
			if err != nil {
				return err
			}
			data.AlertCounts = mapToAlertCounts(counts)
			return nil
		})
		g.Go(func() error {
			recent, err := h.Alerts.ListActive(gctx, 5)
			if err != nil {
				return err
			}
			data.RecentAlerts = recent
			return nil
		})
	}

	if data.Permissions.CanReadJobs && h.Jobs != nil {
		g.Go(func() error {
			counts, err := h.Jobs.CountByStateSince(gctx, since)
			if err != nil {
				return err
			}
			data.JobCounts = mapToJobCounts(counts)
			return nil
		})
		g.Go(func() error {
			recent, err := h.Jobs.List(gctx, pgdb.JobListFilter{Limit: 5, Since: &since})
			if err != nil {
				return err
			}
			data.RecentJobs = recent
			return nil
		})
	}

	if data.Permissions.CanReadJobs && h.Batches != nil {
		g.Go(func() error {
			counts, err := h.Batches.CountByStatus(gctx)
			if err != nil {
				return err
			}
			data.BatchCounts = mapToBatchCounts(counts)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		// Falha em uma query não derruba a página — log + segue com zero values.
		logger.FromContext(ctx).Warn("dashboard: query parcial falhou", "err", err)
	}

	data.Health = h.runHealthChecks(ctx)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardpage.Page(data).Render(ctx, w); err != nil {
		logger.FromContext(ctx).Error("dashboard render", "err", err)
	}
}

// runHealthChecks reproduz a lógica de healthz.go porém retorna o tipo do
// dashboard. Não dá pra reusar diretamente porque healthz emite JSON HTTP.
func (h *DashboardHandler) runHealthChecks(ctx context.Context) []dashboardpage.HealthRow {
	rows := make([]dashboardpage.HealthRow, 0, 3)

	rows = append(rows, runDashboardCheck(ctx, "Postgres", h.Postgres == nil, func(c context.Context) error {
		return pgdb.Ping(c, h.Postgres)
	}))
	rows = append(rows, runDashboardCheck(ctx, "Redis", h.Redis == nil, func(c context.Context) error {
		return rds.Ping(c, h.Redis)
	}))
	rows = append(rows, runDashboardCheck(ctx, "GenieACS", h.GenieACS == nil, func(c context.Context) error {
		return h.GenieACS.Ping(c)
	}))
	return rows
}

func runDashboardCheck(ctx context.Context, name string, skip bool, fn func(context.Context) error) dashboardpage.HealthRow {
	if skip {
		return dashboardpage.HealthRow{Name: name, Status: "skipped"}
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := fn(cctx); err != nil {
		return dashboardpage.HealthRow{
			Name: name, Status: "error",
			Latency: time.Since(start).Round(time.Millisecond).String(),
			Error:   err.Error(),
		}
	}
	return dashboardpage.HealthRow{
		Name: name, Status: "ok",
		Latency: time.Since(start).Round(time.Millisecond).String(),
	}
}

// ───────────────── mapping helpers ─────────────────

func mapToDeviceCounts(m map[string]int) dashboardpage.DeviceCounts {
	c := dashboardpage.DeviceCounts{
		Online:    m["online"],
		Offline:   m["offline"],
		NeverSeen: m["never_seen"],
		Unknown:   m["unknown"],
	}
	c.Total = c.Online + c.Offline + c.NeverSeen + c.Unknown
	return c
}

func mapToAlertCounts(m map[string]int) dashboardpage.AlertCounts {
	c := dashboardpage.AlertCounts{
		Critical: m[string(alerting.SeverityCritical)],
		Warning:  m[string(alerting.SeverityWarning)],
		Info:     m[string(alerting.SeverityInfo)],
	}
	c.Total = c.Critical + c.Warning + c.Info
	return c
}

func mapToJobCounts(m map[string]int) dashboardpage.JobCounts {
	c := dashboardpage.JobCounts{
		Queued:    m[string(prov.JobQueued)],
		Running:   m[string(prov.JobRunning)],
		Done:      m[string(prov.JobDone)],
		Failed:    m[string(prov.JobFailed)],
		Cancelled: m[string(prov.JobCancelled)],
	}
	c.Total = c.Queued + c.Running + c.Done + c.Failed + c.Cancelled
	return c
}

func mapToBatchCounts(m map[string]int) dashboardpage.BatchCounts {
	c := dashboardpage.BatchCounts{
		AwaitingApproval: m[string(prov.BatchAwaitingApproval)],
		Running:          m[string(prov.BatchRunning)],
		Queued:           m[string(prov.BatchQueued)],
	}
	for _, n := range m {
		c.Total += n
	}
	return c
}
