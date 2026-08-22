package observability

import (
	"context"
	"log/slog"
)

type requestIDContextKey struct{}

// WithRequestID carries the HTTP correlation identifier into services and
// background-safe helpers that only receive a context.Context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

// RequestID returns the correlation identifier attached at the HTTP boundary.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// Error emits a structured operational error and automatically includes the
// request ID when the supplied context originated from an HTTP request.
func Error(ctx context.Context, message string, err error, attrs ...any) {
	args := make([]any, 0, len(attrs)+4)
	if requestID := RequestID(ctx); requestID != "" {
		args = append(args, "request_id", requestID)
	}
	if err != nil {
		args = append(args, "error", err)
	}
	args = append(args, attrs...)
	slog.ErrorContext(ctx, message, args...)
}

// Warn emits a structured warning with the same request correlation behavior
// as Error. Use it for degraded best-effort work that does not fail a request.
func Warn(ctx context.Context, message string, err error, attrs ...any) {
	args := make([]any, 0, len(attrs)+4)
	if requestID := RequestID(ctx); requestID != "" {
		args = append(args, "request_id", requestID)
	}
	if err != nil {
		args = append(args, "error", err)
	}
	args = append(args, attrs...)
	slog.WarnContext(ctx, message, args...)
}
