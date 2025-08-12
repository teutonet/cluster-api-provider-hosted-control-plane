package logging

import (
	"context"
	"log/slog"

	slices "github.com/samber/lo"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type deduplicatingLoggingHandler struct {
	Handler    slog.Handler
	attributes map[string]slog.Attr
}

var _ slog.Handler = deduplicatingLoggingHandler{}

func NewDeduplicatingLoggingHandler(handler slog.Handler) slog.Handler {
	return deduplicatingLoggingHandler{
		Handler:    handler,
		attributes: make(map[string]slog.Attr),
	}
}

func (h deduplicatingLoggingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Handler.Enabled(ctx, level)
}

func (h deduplicatingLoggingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return deduplicatingLoggingHandler{
		Handler: h.Handler,
		attributes: slices.Assign(h.attributes, slices.SliceToMap(attrs, func(attr slog.Attr) (string, slog.Attr) {
			return attr.Key, attr
		})),
	}
}

func (h deduplicatingLoggingHandler) WithGroup(name string) slog.Handler {
	return deduplicatingLoggingHandler{
		Handler:    h.Handler.WithGroup(name),
		attributes: h.attributes,
	}
}

func (h deduplicatingLoggingHandler) Handle(ctx context.Context, record slog.Record) error {
	numRecordAttributes := record.NumAttrs()
	attributes := make(map[string]slog.Attr, numRecordAttributes)
	if numRecordAttributes > 0 {
		record.Attrs(func(a slog.Attr) bool {
			attributes[a.Key] = a
			return true
		})
	}
	record = slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	if numRecordAttributes+len(h.attributes) > 0 {
		record.AddAttrs(slices.Values(slices.Assign(h.attributes, attributes))...)
	}

	//nolint:wrapcheck // this is just a wrapper function
	return h.Handler.Handle(ctx, record)
}

type tracingLoggingHandler struct {
	Handler slog.Handler
}

var _ slog.Handler = tracingLoggingHandler{}

func NewTracingLoggingHandler(handler slog.Handler) slog.Handler {
	return tracingLoggingHandler{
		Handler: handler,
	}
}

func (t tracingLoggingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return t.Handler.Enabled(ctx, level)
}

func (t tracingLoggingHandler) Handle(ctx context.Context, record slog.Record) error {
	handler := t.Handler

	span := trace.SpanFromContext(ctx)
	spanContext := span.SpanContext()
	if _, isNoopSpan := span.(*noop.Span); !isNoopSpan && spanContext.IsValid() {
		handler = t.Handler.WithAttrs([]slog.Attr{
			slog.String("traceID", spanContext.TraceID().String()),
			slog.String("spanID", spanContext.SpanID().String()),
		})
	}

	//nolint:wrapcheck // this is just a wrapper function
	return handler.Handle(ctx, record)
}

func (t tracingLoggingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return tracingLoggingHandler{
		Handler: t.Handler.WithAttrs(attrs),
	}
}

func (t tracingLoggingHandler) WithGroup(name string) slog.Handler {
	return tracingLoggingHandler{
		Handler: t.Handler.WithGroup(name),
	}
}

type splittingLoggingHandler struct {
	StdoutHandler slog.Handler
	StderrHandler slog.Handler
}

var _ slog.Handler = splittingLoggingHandler{}

func NewSplittingLoggingHandler(stdoutHandler, stderrHandler slog.Handler) slog.Handler {
	return splittingLoggingHandler{
		StdoutHandler: stdoutHandler,
		StderrHandler: stderrHandler,
	}
}

func (s splittingLoggingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return s.StdoutHandler.Enabled(ctx, level) || s.StderrHandler.Enabled(ctx, level)
}

func (s splittingLoggingHandler) Handle(ctx context.Context, record slog.Record) error {
	handler := s.StdoutHandler
	if record.Level >= slog.LevelError {
		handler = s.StderrHandler
	}
	//nolint:wrapcheck // this is just a wrapper function
	return handler.Handle(ctx, record)
}

func (s splittingLoggingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return splittingLoggingHandler{
		StdoutHandler: s.StdoutHandler.WithAttrs(attrs),
		StderrHandler: s.StderrHandler.WithAttrs(attrs),
	}
}

func (s splittingLoggingHandler) WithGroup(name string) slog.Handler {
	return splittingLoggingHandler{
		StdoutHandler: s.StdoutHandler.WithGroup(name),
		StderrHandler: s.StderrHandler.WithGroup(name),
	}
}
