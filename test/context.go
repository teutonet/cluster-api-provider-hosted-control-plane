package test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/runtime"
)

// G combines NewWithT and NewTestContext into one call.
// It replaces the two-line pattern: g := NewWithT(t); ctx, rec := NewTestContext(t, ...).
func G(
	t *testing.T,
	related ...runtime.Object,
) (gomega.Gomega, context.Context, *recorder.InfiniteReturningFakeRecorder) {
	t.Helper()
	ctx, rec := NewTestContext(t, related...)
	return gomega.NewWithT(t), ctx, rec //nolint:forbidigo // implements G(), the wrapper the rule enforces
}

// NewTestContext returns a context wired with:
//   - a slog logger writing to t.Log (visible with -v or on failure)
//   - an event recorder (optionally bound to related objects for event assertions)
//   - an OTEL span named after the test (ended on t.Cleanup)
//
// Usage: ctx, rec := NewTestContext(t, hcp).
func NewTestContext(
	t *testing.T,
	related ...runtime.Object,
) (context.Context, *recorder.InfiniteReturningFakeRecorder) {
	t.Helper()

	fakeRec, rec := recorder.NewInfiniteReturningFakeRecorder(related...)

	slogLogger := slog.New(slog.NewTextHandler(&testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := recorder.IntoContext(
		logr.NewContext(context.Background(), logr.FromSlogHandler(slogLogger.Handler())),
		rec,
	)

	ctx, span := otel.Tracer("test").Start(ctx, t.Name())
	t.Cleanup(func() { span.End() })

	return ctx, fakeRec
}

// Run wraps t.Run so that the subtest body always receives a pre-wired context
// and recorder — the same setup as NewTestContext — without the caller having
// to remember to do it manually.
//
// Usage: Run(t, "name", func(t *testing.T, ctx context.Context, rec *recorder.InfiniteReturningFakeRecorder) { ... }).
func Run(
	t *testing.T,
	name string,
	fn func(*testing.T, context.Context, *recorder.InfiniteReturningFakeRecorder),
	related ...runtime.Object,
) bool {
	t.Helper()
	return t.Run(name, func(t *testing.T) {
		t.Helper()
		ctx, rec := NewTestContext(t, related...)
		fn(t, ctx, rec)
	})
}

type testLogWriter struct{ t *testing.T }

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}
