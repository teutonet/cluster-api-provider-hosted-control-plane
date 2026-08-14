package tlsroutes

import (
	"context"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1ac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	gwclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

type TLSRoutesReconciler interface {
	ReconcileTLSRoutes(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewTLSRoutesReconciler(
	gatewayClient gwclient.Interface,
	apiServerServicePort int32,
	konnectivityServicePort int32,
) TLSRoutesReconciler {
	return &tlsRoutesReconciler{
		gatewayClient:           gatewayClient,
		apiServerServicePort:    apiServerServicePort,
		konnectivityServicePort: konnectivityServicePort,
		tracer:                  tracing.GetTracer("tlsRoutes"),
	}
}

type tlsRoutesReconciler struct {
	gatewayClient           gwclient.Interface
	apiServerServicePort    int32
	konnectivityServicePort int32
	tracer                  string
}

var _ TLSRoutesReconciler = &tlsRoutesReconciler{}

//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=create;patch

func (trr *tlsRoutesReconciler) ReconcileTLSRoutes(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, trr.tracer, "ReconcileTLSRoutes",
		func(ctx context.Context, span trace.Span) (string, error) {
			apiServerTLSRoute := trr.createTLSRoute(
				names.GetTLSRouteName(cluster),
				cluster,
				hostedControlPlane,
				cluster.Spec.ControlPlaneEndpoint.Host,
				trr.apiServerServicePort,
			)

			if ready, err := trr.applyAndCheckTLSRoute(ctx, apiServerTLSRoute); err != nil {
				return "", err
			} else if !ready {
				return "Api Server TLS route not ready", nil
			}

			konnectivityTLSRoute := trr.createTLSRoute(
				names.GetKonnectivityTLSRouteName(cluster),
				cluster,
				hostedControlPlane,
				names.GetKonnectivityServerHost(cluster),
				trr.konnectivityServicePort,
			)

			if ready, err := trr.applyAndCheckTLSRoute(ctx, konnectivityTLSRoute); err != nil {
				return "", err
			} else if !ready {
				return "konnectivity TLS route not ready", nil
			}

			return "", nil
		},
	)
}

func (trr *tlsRoutesReconciler) createTLSRoute(
	name string,
	cluster *capiv2.Cluster,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	host string,
	port int32,
) *gwv1ac.TLSRouteApplyConfiguration {
	return gwv1ac.TLSRoute(name, cluster.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, "")).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(gwv1ac.TLSRouteSpec().
			WithHostnames(gwv1.Hostname(host)).
			WithParentRefs(gwv1ac.ParentReference().
				WithName(gwv1.ObjectName(hostedControlPlane.Spec.Gateway.Name)).
				WithNamespace(gwv1.Namespace(hostedControlPlane.Spec.Gateway.Namespace)),
			).
			WithRules(gwv1ac.TLSRouteRule().
				WithBackendRefs(gwv1ac.BackendRef().
					WithName(gwv1.ObjectName(names.GetServiceName(cluster))).
					WithPort(port).
					WithWeight(1),
				),
			),
		)
}

func (trr *tlsRoutesReconciler) applyAndCheckTLSRoute(
	ctx context.Context,
	tlsRoute *gwv1ac.TLSRouteApplyConfiguration,
) (bool, error) {
	return tracing.WithSpan(ctx, trr.tracer, "ApplyAndCheckTLSRoute",
		func(ctx context.Context, span trace.Span) (bool, error) {
			appliedTLSRoute, err := trr.gatewayClient.GatewayV1().TLSRoutes(*tlsRoute.Namespace).
				Apply(ctx, tlsRoute, operatorutil.ApplyOptions)
			if err != nil {
				return false, errorsUtil.IfErrErrorf("failed to apply %s TLSRoute: %w", *tlsRoute.Name, err)
			}

			if !slices.ContainsBy(appliedTLSRoute.Status.Parents, func(parent gwv1.RouteParentStatus) bool {
				return parent.ParentRef.Name == *tlsRoute.Spec.ParentRefs[0].Name &&
					slices.ContainsBy(parent.Conditions, func(condition metav1.Condition) bool {
						return condition.Type == string(gwv1.RouteConditionAccepted) &&
							condition.Status == metav1.ConditionTrue
					})
			}) {
				return false, nil
			}

			return true, nil
		},
	)
}
