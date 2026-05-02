package middleware

import (
	"net/http"
	"time"

	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// Logger registra cada request com método, path, status, duração e remote_addr.
// Deve vir DEPOIS de Correlation no chain — para que o correlation_id já
// esteja fixado no logger do contexto.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(ww, r)

		l := logger.FromContext(r.Context())
		l.Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
