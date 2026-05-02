// Package handlers expõe os handlers HTTP do SentinelACS.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	"github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	"github.com/celinet/sentinel-acs/internal/infrastructure/redis"
)

// HealthDeps junta tudo que o /healthz precisa para checar.
// Qualquer dep nil é tratada como "não verificada" (status "skipped"), útil
// em dev onde nem tudo está conectado.
type HealthDeps struct {
	Version  string
	Postgres postgres.Pool
	Redis    redis.Client
	GenieACS *genieacs.Client
}

type checkResult struct {
	Status  string `json:"status"`            // ok | error | skipped
	Latency string `json:"latency,omitempty"` // ex: "12ms"
	Error   string `json:"error,omitempty"`
}

type healthResponse struct {
	Status  string                 `json:"status"`  // ok | degraded
	Version string                 `json:"version"`
	Checks  map[string]checkResult `json:"checks"`
}

// Healthz responde 200 quando todos os checks essenciais passam,
// 503 quando algum check essencial falha (PG, Redis em prod).
func Healthz(deps HealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()

		resp := healthResponse{
			Status:  "ok",
			Version: deps.Version,
			Checks:  make(map[string]checkResult, 3),
		}

		// Chaves intencionalmente genéricas (db/cache/upstream) — /healthz é
		// público sem auth; expor "postgres"/"redis"/"genieacs" facilita
		// fingerprinting de stack para reconhecimento. Monitoria interna deve
		// alertar pelo status, não pelo nome do produto.
		resp.Checks["db"] = runCheck(ctx, func(ctx context.Context) error {
			return postgres.Ping(ctx, deps.Postgres)
		}, deps.Postgres == nil)

		resp.Checks["cache"] = runCheck(ctx, func(ctx context.Context) error {
			return redis.Ping(ctx, deps.Redis)
		}, deps.Redis == nil)

		resp.Checks["upstream"] = runCheck(ctx, func(ctx context.Context) error {
			return deps.GenieACS.Ping(ctx)
		}, deps.GenieACS == nil)

		statusCode := http.StatusOK
		for _, c := range resp.Checks {
			if c.Status == "error" {
				resp.Status = "degraded"
				statusCode = http.StatusServiceUnavailable
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func runCheck(ctx context.Context, fn func(context.Context) error, skip bool) checkResult {
	if skip {
		return checkResult{Status: "skipped"}
	}
	start := time.Now()
	if err := fn(ctx); err != nil {
		return checkResult{Status: "error", Error: err.Error(), Latency: time.Since(start).Round(time.Millisecond).String()}
	}
	return checkResult{Status: "ok", Latency: time.Since(start).Round(time.Millisecond).String()}
}
