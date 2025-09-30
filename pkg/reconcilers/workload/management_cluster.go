package workload

import (
	"context"
	"fmt"
	"net/http"
	"time"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

type ManagementCluster interface {
	GetWorkloadClusterClient(
		ctx context.Context, cluster *capiv2.Cluster,
	) (alias.WorkloadClusterClient, ciliumclient.Interface, error)
}

func NewManagementCluster(
	kubernetesClient kubernetes.Interface,
	tracingWrapper func(rt http.RoundTripper) http.RoundTripper,
	controllerKubeconfigName string,
) ManagementCluster {
	return &management{
		kubernetesClient:         kubernetesClient,
		tracingWrapper:           tracingWrapper,
		controllerKubeconfigName: controllerKubeconfigName,
	}
}

type management struct {
	kubernetesClient         kubernetes.Interface
	tracingWrapper           func(rt http.RoundTripper) http.RoundTripper
	controllerKubeconfigName string
}

var _ ManagementCluster = &management{}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (m *management) GetWorkloadClusterClient(
	ctx context.Context,
	cluster *capiv2.Cluster,
) (alias.WorkloadClusterClient, ciliumclient.Interface, error) {
	return tracing.WithSpan3(ctx, "managementCluster", "GetWorkloadClusterClient",
		func(ctx context.Context, span trace.Span) (alias.WorkloadClusterClient, ciliumclient.Interface, error) {
			span.SetAttributes(
				attribute.String(
					"kubeconfig.secret.name",
					names.GetKubeconfigSecretName(cluster, m.controllerKubeconfigName),
				),
			)
			kubeConfigSecret, err := m.kubernetesClient.CoreV1().Secrets(cluster.Namespace).
				Get(ctx, names.GetKubeconfigSecretName(cluster, m.controllerKubeconfigName), metav1.GetOptions{})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get kubeconfig for workload cluster: %w", err)
			}

			restConfig, err := clientcmd.RESTConfigFromKubeConfig(
				kubeConfigSecret.Data[capisecretutil.KubeconfigDataName],
			)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get REST config for workload cluster: %w", err)
			}
			restConfig.Timeout = 10 * time.Second
			restConfig.Wrap(m.tracingWrapper)

			var workloadClusterClient kubernetes.Interface
			workloadClusterClient, err = kubernetes.NewForConfig(restConfig)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create Kubernetes client for workload cluster: %w", err)
			}

			groups, err := workloadClusterClient.Discovery().ServerGroups()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to discover API groups for workload cluster: %w", err)
			}
			if slices.NoneBy(groups.Groups, func(group metav1.APIGroup) bool {
				return group.Name == ciliumv2.SchemeGroupVersion.Group
			}) {
				return workloadClusterClient, nil, nil
			}

			var ciliumClient ciliumclient.Interface
			ciliumClient, err = ciliumclient.NewForConfig(restConfig)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create Cilium client for workload cluster: %w", err)
			}

			return workloadClusterClient, ciliumClient, nil
		},
	)
}
