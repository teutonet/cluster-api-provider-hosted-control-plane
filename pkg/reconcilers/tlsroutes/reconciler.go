package tlsroutes

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1ac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	"sigs.k8s.io/gateway-api/applyconfiguration/apis/v1alpha2"
	gwclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

type TLSRoutesReconciler interface {
	ReconcileTLSRoutes(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
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
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, trr.tracer, "ReconcileTLSRoutes",
		func(ctx context.Context, span trace.Span) error {
			apiServerTLSRoute := trr.createTLSRoute(
				names.GetTLSRouteName(cluster),
				cluster,
				hostedControlPlane,
				cluster.Spec.ControlPlaneEndpoint.Host,
				trr.apiServerServicePort,
			)

			if err := trr.applyAndCheckTLSRoute(ctx, apiServerTLSRoute); err != nil {
				return err
			}

			konnectivityTLSRoute := trr.createTLSRoute(
				names.GetKonnectivityTLSRouteName(cluster),
				cluster,
				hostedControlPlane,
				names.GetKonnectivityServerHost(cluster),
				trr.konnectivityServicePort,
			)

			if err := trr.applyAndCheckTLSRoute(ctx, konnectivityTLSRoute); err != nil {
				return err
			}

			hostedControlPlane.Status.Ready = true

			return nil
		},
	)
}

func (trr *tlsRoutesReconciler) createTLSRoute(
	name string,
	cluster *capiv1.Cluster,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	host string,
	port int32,
) *v1alpha2.TLSRouteApplyConfiguration {
	return v1alpha2.TLSRoute(name, cluster.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, "")).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
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

func (trr *tlsRoutesReconciler) applyAndCheckTLSRoute(
	ctx context.Context,
	tlsRoute *v1alpha2.TLSRouteApplyConfiguration,
) error {
	return tracing.WithSpan1(ctx, trr.tracer, "ApplyAndCheckTLSRoute",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("TLSRouteName", *tlsRoute.Name),
				attribute.String("TLSRouteNamespace", *tlsRoute.Namespace),
			)

			appliedTLSRoute, err := trr.gatewayClient.GatewayV1alpha2().TLSRoutes(*tlsRoute.Namespace).
				Apply(ctx, tlsRoute, operatorutil.ApplyOptions)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to apply %s TLSRoute: %w", *tlsRoute.Name, err)
			}

			if !slices.ContainsBy(appliedTLSRoute.Status.Parents, func(parent gwv1.RouteParentStatus) bool {
				return parent.ParentRef.Name == *tlsRoute.Spec.ParentRefs[0].Name &&
					slices.ContainsBy(parent.Conditions, func(condition metav1.Condition) bool {
						return condition.Type == string(gwv1.RouteConditionAccepted) &&
							condition.Status == metav1.ConditionTrue
					})
			}) {
				return fmt.Errorf("%s TLSRoute is not ready: %w", *tlsRoute.Name, operatorutil.ErrRequeueRequired)
			}

			return nil
		},
	)
}
