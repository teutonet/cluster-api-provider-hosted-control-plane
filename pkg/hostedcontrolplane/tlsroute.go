package hostedcontrolplane

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1ac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	"sigs.k8s.io/gateway-api/applyconfiguration/apis/v1alpha2"
)

//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=create;patch

func (r *HostedControlPlaneReconciler) reconcileTLSRoutes(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileTLSRoute",
		func(ctx context.Context, span trace.Span) error {
			apiServerTLSRoute := r.createTLSRoute(
				names.GetTLSRouteName(cluster),
				cluster,
				hostedControlPlane,
				cluster.Spec.ControlPlaneEndpoint.Host,
				APIServerServicePort,
			)

			if err := r.applyAndCheckTLSRoute(ctx, apiServerTLSRoute, cluster.Namespace, "API server"); err != nil {
				return err
			}

			konnectivityTLSRoute := r.createTLSRoute(
				names.GetKonnectivityTLSRouteName(cluster),
				cluster,
				hostedControlPlane,
				names.GetKonnectivityServerHost(cluster),
				KonnectivityServicePort,
			)

			if err := r.applyAndCheckTLSRoute(ctx, konnectivityTLSRoute, cluster.Namespace, "Konnectivity"); err != nil {
				return err
			}

			hostedControlPlane.Status.Ready = true

			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) createTLSRoute(
	name string,
	cluster *capiv1.Cluster,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	host string,
	port int32,
) *v1alpha2.TLSRouteApplyConfiguration {
	return v1alpha2.TLSRoute(name, cluster.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, "")).
		WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(v1alpha2.TLSRouteSpec().
			WithHostnames(gwv1alpha2.Hostname(host)).
			WithParentRefs(gwv1ac.ParentReference().
				WithName("capi").
				WithNamespace(gwv1.Namespace(cluster.Namespace)),
			).
			WithRules(v1alpha2.TLSRouteRule().
				WithBackendRefs(gwv1ac.BackendRef().
					WithName(gwv1.ObjectName(names.GetServiceName(cluster))).
					WithPort(gwv1.PortNumber(port)).
					WithWeight(1),
				),
			),
		)
}

func (r *HostedControlPlaneReconciler) applyAndCheckTLSRoute(
	ctx context.Context,
	tlsRoute *v1alpha2.TLSRouteApplyConfiguration,
	namespace string,
	routeType string,
) error {
	appliedTLSRoute, err := r.GatewayClient.GatewayV1alpha2().TLSRoutes(namespace).
		Apply(ctx, tlsRoute, applyOptions)
	if err != nil {
		return errorsUtil.IfErrErrorf("failed to apply %s TLSRoute: %w", routeType, err)
	}

	if !slices.ContainsBy(appliedTLSRoute.Status.Parents, func(parent gwv1.RouteParentStatus) bool {
		return parent.ParentRef.Name == *tlsRoute.Spec.ParentRefs[0].Name &&
			slices.ContainsBy(parent.Conditions, func(condition metav1.Condition) bool {
				return condition.Type == string(gwv1.RouteConditionAccepted) &&
					condition.Status == metav1.ConditionTrue
			})
	}) {
		return fmt.Errorf("%s TLSRoute is not ready: %w", routeType, ErrRequeueRequired)
	}

	return nil
}
