package log

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		return v
	}
	return "gen_" + randomHex(8)
}

func ContextWithTrace(ctx context.Context, traceID, spanID string) context.Context {
	ctx = context.WithValue(ctx, traceIDKey, traceID)
	return context.WithValue(ctx, spanIDKey, spanID)
}

func TraceIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey).(string)
	return v
}

func SpanIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(spanIDKey).(string)
	return v
}

func NewTraceID() string { return randomHex(16) }

func NewSpanID() string { return randomHex(8) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("log: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
