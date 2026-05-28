package emit

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type Sink uint

const (
	SinkRecorder Sink = 1 << iota
	SinkLogger
	SinkSpanEvent
	SinkAll = SinkRecorder | SinkLogger | SinkSpanEvent
)

func Warn(ctx context.Context, sinks Sink, related runtime.Object, reason, action, msg string, fields ...any) {
	if sinks&SinkRecorder != 0 {
		writeRecorder(ctx, true, related, reason, action, msg, fields...)
	}
	if sinks&(SinkLogger|SinkSpanEvent) != 0 {
		writeLogAndSpan(ctx, sinks, true, related, reason, action, msg, fields...)
	}
}

func Info(ctx context.Context, sinks Sink, related runtime.Object, reason, action, msg string, fields ...any) {
	if sinks&SinkRecorder != 0 {
		writeRecorder(ctx, false, related, reason, action, msg, fields...)
	}
	if sinks&(SinkLogger|SinkSpanEvent) != 0 {
		writeLogAndSpan(ctx, sinks, false, related, reason, action, msg, fields...)
	}
}

func writeRecorder(ctx context.Context, warn bool, related runtime.Object, reason, action, msg string, fields ...any) {
	rec := recorder.FromContext(ctx)
	note := noteWithFields(msg, fields)
	if warn {
		rec.Warnf(related, reason, action, "%s", note)
	} else {
		rec.Normalf(related, reason, action, "%s", note)
	}
}

func noteWithFields(msg string, fields []any) string {
	if len(fields) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	for i := 0; i+1 < len(fields); i += 2 {
		fmt.Fprintf(&b, " %v=%v", fields[i], fields[i+1])
	}
	return b.String()
}

func writeLogAndSpan(
	ctx context.Context,
	sinks Sink,
	warn bool,
	related runtime.Object,
	reason, action, msg string,
	extraFields ...any,
) {
	relLogFields, relSpanAttrs := relatedFields(related)
	logFields := append(append([]any{"reason", reason, "action", action}, relLogFields...), extraFields...)
	spanAttrs := append(
		[]attribute.KeyValue{attribute.String("reason", reason), attribute.String("message", msg)},
		relSpanAttrs...,
	)

	if sinks&SinkLogger != 0 {
		logger := logr.FromContextAsSlogLogger(ctx)
		if warn {
			logger.WarnContext(ctx, msg, logFields...)
		} else {
			logger.InfoContext(ctx, msg, logFields...)
		}
	}
	if sinks&SinkSpanEvent != 0 {
		trace.SpanFromContext(ctx).AddEvent(action, trace.WithAttributes(spanAttrs...))
	}
}

func relatedFields(related runtime.Object) (logFields []any, spanAttrs []attribute.KeyValue) {
	if related == nil {
		return nil, nil
	}
	obj, ok := related.(metav1.Object)
	if !ok {
		return nil, nil
	}
	if name := obj.GetName(); name != "" {
		logFields = append(logFields, "related.name", name)
		spanAttrs = append(spanAttrs, attribute.String("related.name", name))
	}
	if ns := obj.GetNamespace(); ns != "" {
		logFields = append(logFields, "related.namespace", ns)
		spanAttrs = append(spanAttrs, attribute.String("related.namespace", ns))
	}
	return logFields, spanAttrs
}
