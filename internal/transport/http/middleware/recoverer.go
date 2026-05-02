package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// Recoverer captura panics, loga com stack trace e devolve 500.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.FromContext(r.Context()).Error("panic recovered",
					"err", rec,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
