package tracing

import (
	"context"
	"strings"

	slices "github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func GetTracer(components ...string) string {
	titleCaser := cases.Title(language.English)
	tracer := "HostedControlPlane" + strings.Join(slices.Map(components, func(component string, _ int) string {
		return titleCaser.String(component)
	}), "") + "Reconciler"
	return tracer
}

func WithSpan[R any](
	ctx context.Context,
	tracerName string,
	spanName string,
	block func(context.Context, trace.Span) (R, error),
) (r R, err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	defer handlePanic(span)
	r, err = block(ctx, span)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return r, err
}

func WithSpan1[R any](
	ctx context.Context,
	tracerName string,
	spanName string,
	block func(context.Context, trace.Span) R,
) (r R) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	defer handlePanic(span)
	if r = block(ctx, span); any(r) != nil {
		if err, isErr := any(r).(error); isErr {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}
	return r
}

func WithSpan3[R1 any, R2 any](
	ctx context.Context,
	tracerName string,
	spanName string,
	block func(context.Context, trace.Span) (R1, R2, error),
) (r1 R1, r2 R2, err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	defer handlePanic(span)
	r1, r2, err = block(ctx, span)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return r1, r2, err
}

func handlePanic(span trace.Span) {
	if rec := recover(); rec != nil {
		if err, isErr := rec.(error); isErr {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		panic(rec)
	}
}
