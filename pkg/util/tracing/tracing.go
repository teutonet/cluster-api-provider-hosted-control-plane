package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func WithSpan[R any](
	ctx context.Context,
	tracerName string,
	spanName string,
	block func(context.Context, trace.Span) (R, error),
) (r R, err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	r, err = block(ctx, span)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return
}

func WithSpan1[R any](
	ctx context.Context,
	tracerName string,
	spanName string,
	block func(context.Context, trace.Span) R,
) (r R) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	if r = block(ctx, span); any(r) != nil {
		if err, isErr := any(r).(error); isErr {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}
	return
}

// WithSpan3 is needed, as GO doesn't support method overloading 🙄. It's a copy of WithSpan with 3 return values.
func WithSpan3[R1 any, R2 any](
	ctx context.Context,
	tracerName string,
	spanName string,
	block func(context.Context, trace.Span) (R1, R2, error),
) (r1 R1, r2 R2, err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	r1, r2, err = block(ctx, span)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return
}
