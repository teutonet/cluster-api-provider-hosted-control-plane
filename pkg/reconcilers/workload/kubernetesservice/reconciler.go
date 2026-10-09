package kubernetesservice

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	discoveryv1ac "k8s.io/client-go/applyconfigurations/discovery/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

// KubernetesServiceReconciler owns the endpoints of the workload cluster's default/kubernetes Service.
//
// The kube-apiservers run with --endpoint-reconciler-type=none. Their lease reconciler keeps a single lease per
// advertised IP and removes it on every graceful shutdown. All apiservers share the load balancer IP, so each
// stopping pod would empty the slice until another pod re-adds it, which causes rejected connections on the
// Service IP although the load balancer and other apiservers are healthy.
type KubernetesServiceReconciler interface {
	ReconcileKubernetesEndpoints(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
	) (string, error)
}

func NewKubernetesServiceReconciler(workloadClusterClient *alias.WorkloadClusterClient) KubernetesServiceReconciler {
	return &kubernetesServiceReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			WorkloadClusterClient: workloadClusterClient,
			Tracer:                tracing.GetTracer("kubernetesService"),
		},
		serviceName: "kubernetes",
		namespace:   metav1.NamespaceDefault,
		portName:    "https",
		port:        konstants.KubeAPIServerPort,
	}
}

type kubernetesServiceReconciler struct {
	reconcilers.WorkloadResourceReconciler
	serviceName string
	namespace   string
	portName    string
	port        int32
}

var _ KubernetesServiceReconciler = &kubernetesServiceReconciler{}

func (kr *kubernetesServiceReconciler) ReconcileKubernetesEndpoints(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "ReconcileKubernetesEndpoints",
		func(ctx context.Context, _ trace.Span) (string, error) {
			if hostedControlPlane.Status.LegacyIP == "" {
				return "LoadBalancerIPNotReady", nil
			}
			address, err := netip.ParseAddr(hostedControlPlane.Status.LegacyIP)
			if err != nil {
				return "", fmt.Errorf("invalid load balancer IP %q: %w", hostedControlPlane.Status.LegacyIP, err)
			}

			// The slice is what kube-proxy and the CNI consume.
			if err := kr.applyEndpointSlice(ctx, address); err != nil {
				return "", err
			}
			// The legacy Endpoints object is still read by older clients and is maintained by the apiserver's
			// own reconciler otherwise.
			return "", kr.applyEndpoints(ctx, address)
		},
	)
}

func (kr *kubernetesServiceReconciler) applyEndpointSlice(ctx context.Context, address netip.Addr) error {
	addressType := discoveryv1.AddressTypeIPv4
	if address.Is6() {
		addressType = discoveryv1.AddressTypeIPv6
	}

	// Same shape as the apiserver's own reconciler produces. Notably there is no managed-by label, which keeps
	// the endpointslice and endpointslice-mirroring controllers away from it.
	_, err := kr.WorkloadClusterClient.DiscoveryV1().EndpointSlices(kr.namespace).Apply(
		ctx,
		discoveryv1ac.EndpointSlice(kr.serviceName, kr.namespace).
			WithLabels(map[string]string{discoveryv1.LabelServiceName: kr.serviceName}).
			WithAddressType(addressType).
			WithEndpoints(discoveryv1ac.Endpoint().
				WithAddresses(address.String()).
				WithConditions(discoveryv1ac.EndpointConditions().WithReady(true)),
			).
			WithPorts(discoveryv1ac.EndpointPort().
				WithName(kr.portName).
				WithProtocol(corev1.ProtocolTCP).
				WithPort(kr.port),
			),
		operatorutil.ApplyOptions,
	)
	return errorsUtil.IfErrErrorf("failed to apply endpointslice %s/%s: %w", kr.namespace, kr.serviceName, err)
}

func (kr *kubernetesServiceReconciler) applyEndpoints(ctx context.Context, address netip.Addr) error {
	_, err := kr.WorkloadClusterClient.CoreV1().Endpoints(kr.namespace).Apply(
		ctx,
		corev1ac.Endpoints(kr.serviceName, kr.namespace).
			WithLabels(map[string]string{discoveryv1.LabelSkipMirror: "true"}).
			WithSubsets(corev1ac.EndpointSubset().
				WithAddresses(corev1ac.EndpointAddress().WithIP(address.String())).
				WithPorts(corev1ac.EndpointPort().
					WithName(kr.portName).
					WithProtocol(corev1.ProtocolTCP).
					WithPort(kr.port),
				),
			),
		operatorutil.ApplyOptions,
	)
	return errorsUtil.IfErrErrorf("failed to apply endpoints %s/%s: %w", kr.namespace, kr.serviceName, err)
}
