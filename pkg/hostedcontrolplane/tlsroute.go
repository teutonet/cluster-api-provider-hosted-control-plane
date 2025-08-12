package hostedcontrolplane

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1ac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	gwv1alpha2ac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1alpha2"
)

//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=create;patch

func (r *HostedControlPlaneReconciler) reconcileTLSRoute(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileTLSRoute",
		func(ctx context.Context, span trace.Span) error {
			tlsRoute := gwv1alpha2ac.TLSRoute(names.GetTLSRouteName(cluster), cluster.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(gwv1alpha2ac.TLSRouteSpec().
					WithHostnames(gwv1alpha2.Hostname(cluster.Spec.ControlPlaneEndpoint.Host)).
					WithParentRefs(gwv1ac.ParentReference().
						WithName("capi").
						WithNamespace(gwv1.Namespace(cluster.Namespace)),
					).
					WithRules(gwv1alpha2ac.TLSRouteRule().
						WithBackendRefs(gwv1ac.BackendRef().
							WithName(gwv1.ObjectName(names.GetServiceName(cluster))).
							WithPort(gwv1.PortNumber(443)).
							WithWeight(1),
						),
					),
				)

			appliedTLSRoute, err := r.GatewayClient.GatewayV1alpha2().TLSRoutes(cluster.Namespace).
				Apply(ctx, tlsRoute, applyOptions)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to apply TLSRoute: %w", err)
			}

			if !slices.ContainsBy(appliedTLSRoute.Status.RouteStatus.Parents, func(parent gwv1.RouteParentStatus) bool {
				return parent.ParentRef.Name == *tlsRoute.Spec.ParentRefs[0].Name &&
					slices.ContainsBy(parent.Conditions, func(condition metav1.Condition) bool {
						return condition.Type == string(gwv1.RouteConditionAccepted) && condition.Status == metav1.ConditionTrue
					})
			}) {
				return fmt.Errorf("TLSRoute is not ready: %w", ErrRequeueRequired)
			}

			hostedControlPlane.Status.Ready = true

			return nil
		},
	)
}
