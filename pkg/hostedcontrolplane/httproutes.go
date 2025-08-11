package hostedcontrolplane

import (
	"context"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
)

func (r *HostedControlPlaneReconciler) reconcileHTTPRoute(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileHTTPRoute",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement HTTPRoute reconciliation
			return nil
		},
	)
}
