// Package observability provides structured logging and lightweight metrics
// for the local diting daemon. It uses only the Go standard library so the
// project keeps its minimal dependency footprint.
package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Logger is the process-wide structured logger. Replace the default with
// Init before starting services so every module emits consistent records.
var Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// Init configures the global logger from a textual level (debug, info, warn,
// error). Unknown values fall back to info.
func Init(level string) {
	var l slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
	slog.SetDefault(Logger)
}

// With returns a logger with the given attributes, useful for attaching a
// component name once at package init or construction time.
func With(args ...any) *slog.Logger {
	return Logger.With(args...)
}

// Error logs an error with a stable component and operation field, avoiding
// accidental inclusion of sensitive payloads in the message.
func Error(component, operation string, err error, args ...any) {
	if err == nil {
		return
	}
	attrs := []any{"component", component, "operation", operation, "error", err}
	attrs = append(attrs, args...)
	Logger.Error("operation failed", attrs...)
}

// Info logs an informational event with component context.
func Info(component, msg string, args ...any) {
	attrs := []any{"component", component}
	attrs = append(attrs, args...)
	Logger.Info(msg, attrs...)
}

// Debug logs a debug event with component context.
func Debug(component, msg string, args ...any) {
	attrs := []any{"component", component}
	attrs = append(attrs, args...)
	Logger.Debug(msg, attrs...)
}

// ContextWithTrace returns a context carrying a trace identifier. It is a
// minimal replacement for full distributed tracing and lets logs correlate
// events that belong to the same user action.
type traceKey struct{}

func ContextWithTrace(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// TraceFromContext extracts the trace identifier, or empty string if absent.
func TraceFromContext(ctx context.Context) string {
	if v := ctx.Value(traceKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
