package observability

import (
	"log/slog"
	"os"
)

// NewLogger builds the process-wide structured logger.
func NewLogger(level, format, serviceName, version string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	log := slog.New(handler).With(
		slog.String("service", serviceName),
		slog.String("version", version),
	)
	slog.SetDefault(log)
	return log
}
