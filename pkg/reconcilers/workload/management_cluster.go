package workload

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

type ManagementCluster interface {
	GetWorkloadClusterClient(ctx context.Context, cluster *capiv1.Cluster) (*alias.WorkloadClusterClient, error)
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
	cluster *capiv1.Cluster,
) (*kubernetes.Clientset, error) {
	return tracing.WithSpan(ctx, "managementCluster", "GetWorkloadClusterClient",
		func(ctx context.Context, span trace.Span) (*kubernetes.Clientset, error) {
			kubeConfigSecret, err := m.kubernetesClient.CoreV1().Secrets(cluster.Namespace).
				Get(ctx, names.GetKubeconfigSecretName(cluster, m.controllerKubeconfigName), metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to get kubeconfig for workload cluster: %w", err)
			}

			restConfig, err := clientcmd.RESTConfigFromKubeConfig(
				kubeConfigSecret.Data[capisecretutil.KubeconfigDataName],
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get REST config for workload cluster: %w", err)
			}
			restConfig.Timeout = 10 * time.Second
			restConfig.Wrap(m.tracingWrapper)

			workloadClusterClient, err := kubernetes.NewForConfig(restConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to create Kubernetes client for workload cluster: %w", err)
			}

			return workloadClusterClient, nil
		},
	)
}
