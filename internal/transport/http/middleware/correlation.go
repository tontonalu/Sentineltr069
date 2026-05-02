// Package middleware contém middlewares HTTP cross-cutting.
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

const correlationHeader = "X-Correlation-ID"

// Correlation injeta um correlation_id em cada request:
//   - Se o cliente enviar o header X-Correlation-ID, reutiliza.
//   - Caso contrário, gera um UUIDv4.
//
// O id é colocado no contexto e ecoado no header da resposta — útil para
// rastrear logs em sistemas distribuídos (Loki, Tempo, etc).
func Correlation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(correlationHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(correlationHeader, id)

		ctx := logger.WithCorrelationID(r.Context(), id)
		l := logger.FromContext(ctx).With("correlation_id", id)
		ctx = logger.WithLogger(ctx, l)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
