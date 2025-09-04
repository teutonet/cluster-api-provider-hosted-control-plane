package infrastructure_cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type InfrastructureClusterReconciler interface {
	SyncControlPlaneEndpoint(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) (string, error)
}

func NewInfrastructureClusterReconciler(
	client client.Client,
	apiServerServicePort int32,
) InfrastructureClusterReconciler {
	return &infrastructureClusterReconciler{
		client:               client,
		apiServerServicePort: apiServerServicePort,
		tracer:               tracing.GetTracer("infrastructureCluster"),
	}
}

type infrastructureClusterReconciler struct {
	client               client.Client
	apiServerServicePort int32
	tracer               string
}

var _ InfrastructureClusterReconciler = &infrastructureClusterReconciler{}

var errGatewayMissingListener = errors.New("gateway is missing a TLS listener with a wildcard hostname")

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=*,verbs=get;patch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get

func (i *infrastructureClusterReconciler) SyncControlPlaneEndpoint(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, i.tracer, "SyncControlPlaneEndpoint",
		func(ctx context.Context, span trace.Span) (string, error) {
			span.SetAttributes(
				attribute.String("gateway.namespace", hostedControlPlane.Spec.Gateway.Namespace),
				attribute.String("gateway.name", hostedControlPlane.Spec.Gateway.Name),
			)
			gateway := &gwv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hostedControlPlane.Spec.Gateway.Namespace,
					Name:      hostedControlPlane.Spec.Gateway.Name,
				},
			}
			if err := i.client.Get(ctx, client.ObjectKeyFromObject(gateway), gateway); err != nil {
				return "", fmt.Errorf("failed to get gateway: %w", err)
			}

			listener, found := slices.Find(gateway.Spec.Listeners, func(listener gwv1.Listener) bool {
				return listener.Protocol == gwv1.TLSProtocolType && listener.Hostname != nil &&
					strings.HasPrefix(string(*listener.Hostname), "*.")
			})
			if !found {
				return "", fmt.Errorf(
					"no tls listener with wildcard hostname found in gateway %s/%s: %w",
					gateway.Namespace, gateway.Name,
					errGatewayMissingListener,
				)
			}

			endpoint := capiv1.APIEndpoint{
				Host: fmt.Sprintf("%s.%s.%s",
					cluster.Name, cluster.Namespace, strings.TrimPrefix(string(*listener.Hostname), "*."),
				),
				Port: i.apiServerServicePort,
			}

			infraCluster := &unstructured.Unstructured{}

			infraCluster.SetGroupVersionKind(cluster.Spec.InfrastructureRef.GroupVersionKind())
			infraCluster.SetName(cluster.Spec.InfrastructureRef.Name)
			infraCluster.SetNamespace(cluster.Spec.InfrastructureRef.Namespace)

			if err := i.client.Get(ctx, client.ObjectKeyFromObject(infraCluster), infraCluster); err != nil {
				return "", fmt.Errorf("failed to get infrastructure cluster: %w", err)
			}

			patchHelper, err := patch.NewHelper(infraCluster, i.client)
			if err != nil {
				return "", fmt.Errorf("failed to create patch helper: %w", err)
			}

			if err := unstructured.SetNestedMap(infraCluster.Object, map[string]interface{}{
				"host": endpoint.Host,
				"port": int64(endpoint.Port),
			}, "spec", "controlPlaneEndpoint"); err != nil {
				return "", fmt.Errorf("failed to set control plane endpoint: %w", err)
			}

			if err = patchHelper.Patch(ctx, infraCluster); err != nil {
				return "", fmt.Errorf("failed to patch infrastructure cluster: %w", err)
			}

			if cluster.Spec.ControlPlaneEndpoint.IsZero() ||
				cluster.Spec.ControlPlaneEndpoint.Host != endpoint.Host ||
				cluster.Spec.ControlPlaneEndpoint.Port != endpoint.Port {
				return "control plane endpoint is not yet set", nil
			}

			return "", nil
		},
	)
}
