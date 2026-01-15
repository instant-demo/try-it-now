// Package logging provides structured logging using Go's slog.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Logger wraps slog.Logger with convenience methods.
type Logger struct {
	*slog.Logger
}

// New creates a new Logger with the specified level and format.
// Level: "debug", "info", "warn", "error" (default: "info")
// Format: "json", "text" (default: "json")
func New(level, format string) *Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.ToLower(format) == "text" {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	return &Logger{Logger: slog.New(handler)}
}

// With returns a new Logger with the given attributes added.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{Logger: l.Logger.With(args...)}
}

// Fatal logs at error level and exits with code 1.
func (l *Logger) Fatal(msg string, args ...any) {
	l.Logger.Error(msg, args...)
	os.Exit(1)
}

// Default returns a default logger (info level, JSON format).
func Default() *Logger {
	return New("info", "json")
}

// Nop returns a logger that discards all output (useful for tests).
func Nop() *Logger {
	return &Logger{
		Logger: slog.New(slog.NewTextHandler(nopWriter{}, nil)),
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (n int, err error) { return len(p), nil }

// Context methods for request tracing

// WithContext returns a new Logger with context values extracted.
func (l *Logger) WithContext(ctx context.Context) *Logger {
	// Placeholder for future request ID extraction from context
	return l
}
