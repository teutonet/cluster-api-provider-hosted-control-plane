package emit

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type testEnv struct {
	logBuf *strings.Builder
	events *recorder.InfiniteReturningFakeRecorder
	spans  *tracetest.SpanRecorder
}

func newTestEnv(t *testing.T) (context.Context, testEnv) {
	t.Helper()

	var buf strings.Builder
	ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	))

	fakeRec, rec := recorder.NewInfiniteReturningFakeRecorder()
	ctx = recorder.IntoContext(ctx, rec)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("test").Start(ctx, "test")
	t.Cleanup(func() { span.End() })

	return ctx, testEnv{logBuf: &buf, events: fakeRec, spans: sr}
}

func (e testEnv) spanEvents() []sdktrace.Event {
	return e.spans.Started()[0].Events()
}

func spanAttrMap(event sdktrace.Event) map[string]string {
	m := make(map[string]string, len(event.Attributes))
	for _, a := range event.Attributes {
		m[string(a.Key)] = a.Value.AsString()
	}
	return m
}

func TestWarn(t *testing.T) {
	testEmit(t, true)
}

func TestInfo(t *testing.T) {
	testEmit(t, false)
}

func testEmit(t *testing.T, warn bool) {
	t.Helper()

	recorderPrefix := "Normal"
	logLevel := "level=INFO"
	if warn {
		recorderPrefix = "Warning"
		logLevel = "level=WARN"
	}

	call := func(ctx context.Context, s Sink) {
		if warn {
			Warn(ctx, s, nil, "MyReason", "MyAction", "something bad")
		} else {
			Info(ctx, s, nil, "MyReason", "MyAction", "something bad")
		}
	}

	t.Run("recorder gets event", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		call(ctx, SinkRecorder)
		g.Expect(env.events.Events).
			To(ContainElement(ContainSubstring(recorderPrefix + " MyReason MyAction something bad")))
	})

	t.Run("logger gets correct level with fields", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		call(ctx, SinkLogger)
		out := env.logBuf.String()
		g.Expect(out).To(ContainSubstring(logLevel))
		g.Expect(out).To(ContainSubstring("something bad"))
		g.Expect(out).To(ContainSubstring("reason=MyReason"))
		g.Expect(out).To(ContainSubstring("action=MyAction"))
	})

	t.Run("span event has action name and attributes", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		call(ctx, SinkSpanEvent)
		events := env.spanEvents()
		g.Expect(events).To(HaveLen(1))
		g.Expect(events[0].Name).To(Equal("MyAction"))
		attrs := spanAttrMap(events[0])
		g.Expect(attrs).To(HaveKeyWithValue("reason", "MyReason"))
		g.Expect(attrs).To(HaveKeyWithValue("message", "something bad"))
	})
}

func TestFields(t *testing.T) {
	t.Run("logger includes extra fields as structured JSON key-value pairs", func(t *testing.T) {
		g, _, _ := G(t)

		var buf strings.Builder
		ctx := logr.NewContext(context.Background(), logr.FromSlogHandler(
			slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		))

		Info(ctx, SinkLogger, nil, "R", "A", "msg", "key1", "val1", "key2", 42)

		var entry map[string]any
		g.Expect(json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry)).To(Succeed())
		g.Expect(entry).To(HaveKeyWithValue("key1", "val1"))
		g.Expect(entry).To(HaveKeyWithValue("key2", float64(42)))
		g.Expect(entry).To(HaveKeyWithValue("msg", "msg"))
		g.Expect(entry).To(HaveKeyWithValue("reason", "R"))
		g.Expect(entry).To(HaveKeyWithValue("action", "A"))
	})

	t.Run("recorder note includes extra fields as key=value pairs", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		Info(ctx, SinkRecorder, nil, "R", "A", "msg", "key1", "val1", "key2", 42)
		g.Expect(env.events.Events).To(ContainElement(ContainSubstring("msg key1=val1 key2=42")))
	})
}

func TestSinkIsolation(t *testing.T) {
	tests := []struct {
		name           string
		sinks          Sink
		expectRecorder bool
		expectLogger   bool
		expectSpan     bool
	}{
		{"recorder only", SinkRecorder, true, false, false},
		{"logger only", SinkLogger, false, true, false},
		{"span only", SinkSpanEvent, false, false, true},
		{"recorder+logger", SinkRecorder | SinkLogger, true, true, false},
		{"all", SinkAll, true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			ctx, env := newTestEnv(t)
			Warn(ctx, tt.sinks, nil, "R", "A", "msg")

			if tt.expectRecorder {
				g.Expect(env.events.Events).NotTo(BeEmpty())
			} else {
				g.Expect(env.events.Events).To(BeEmpty())
			}
			if tt.expectLogger {
				g.Expect(env.logBuf.String()).NotTo(BeEmpty())
			} else {
				g.Expect(env.logBuf.String()).To(BeEmpty())
			}
			if tt.expectSpan {
				g.Expect(env.spanEvents()).NotTo(BeEmpty())
			} else {
				g.Expect(env.spanEvents()).To(BeEmpty())
			}
		})
	}
}

func TestRelatedObject(t *testing.T) {
	related := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "mypod", Namespace: "mynamespace"}}

	t.Run("logger includes name and namespace", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		Warn(ctx, SinkLogger, related, "R", "A", "msg")
		out := env.logBuf.String()
		g.Expect(out).To(ContainSubstring("related.name=mypod"))
		g.Expect(out).To(ContainSubstring("related.namespace=mynamespace"))
	})

	t.Run("span includes name and namespace", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		Warn(ctx, SinkSpanEvent, related, "R", "A", "msg")
		attrs := spanAttrMap(env.spanEvents()[0])
		g.Expect(attrs).To(HaveKeyWithValue("related.name", "mypod"))
		g.Expect(attrs).To(HaveKeyWithValue("related.namespace", "mynamespace"))
	})

	t.Run("nil related omits related fields from logger", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		Warn(ctx, SinkLogger, nil, "R", "A", "msg")
		g.Expect(env.logBuf.String()).NotTo(ContainSubstring("related."))
	})

	t.Run("nil related omits related attributes from span", func(t *testing.T) {
		g, _, _ := G(t)
		ctx, env := newTestEnv(t)
		Warn(ctx, SinkSpanEvent, nil, "R", "A", "msg")
		attrs := spanAttrMap(env.spanEvents()[0])
		g.Expect(attrs).NotTo(HaveKey("related.name"))
		g.Expect(attrs).NotTo(HaveKey("related.namespace"))
	})
}
