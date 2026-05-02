// Package logger configura o logger global da aplicação usando log/slog.
//
// Convenções:
//   - Sempre JSON estruturado em produção; texto em dev se desejado.
//   - Cada handler HTTP recebe um logger filho com correlation_id já fixado
//     (ver internal/transport/http/middleware.Correlation).
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey string

const (
	correlationIDKey ctxKey = "correlation_id"
	loggerKey        ctxKey = "logger"
)

// New devolve um *slog.Logger configurado conforme level/format.
func New(level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithCorrelationID grava o id no contexto.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationIDFromContext recupera o id (ou "" se ausente).
func CorrelationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(correlationIDKey).(string); ok {
		return v
	}
	return ""
}

// WithLogger grava um logger no contexto.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext devolve o logger do contexto, ou o default se ausente.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
