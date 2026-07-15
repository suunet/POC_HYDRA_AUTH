package log

import (
	"context"
	"io"
	"log/slog"
	"time"
)

const LevelCritical = slog.Level(12)

type ctxKey int

const (
	loggerKey ctxKey = iota
	requestIDKey
	traceIDKey
	spanIDKey
)

func New(w io.Writer, svc string) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				a.Key = "ts"
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.TimeValue(t.UTC())
				}
			case slog.LevelKey:
				a.Key = "lvl"
				if lv, ok := a.Value.Any().(slog.Level); ok && lv == LevelCritical {
					a.Value = slog.StringValue("CRITICAL")
				}
			case slog.MessageKey:
				a.Key = "msg"
			}
			return a
		},
	})
	return slog.New(&contextHandler{Handler: handler}).With("svc", svc)
}

type contextHandler struct {
	slog.Handler
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(
		slog.String("trace_id", TraceIDFromContext(ctx)),
		slog.String("span_id", SpanIDFromContext(ctx)),
		slog.String("req_id", RequestIDFromContext(ctx)),
	)
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
