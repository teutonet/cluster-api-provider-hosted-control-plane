package hostedcontrolplane

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

type ManagementCluster interface {
	GetWorkloadClusterClient(ctx context.Context, cluster *capiv1.Cluster) (*kubernetes.Clientset, error)
}

type Management struct {
	KubernetesClient kubernetes.Interface
	TracingWrapper   func(rt http.RoundTripper) http.RoundTripper
}

var _ ManagementCluster = &Management{}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (m *Management) GetWorkloadClusterClient(
	ctx context.Context,
	cluster *capiv1.Cluster,
) (*kubernetes.Clientset, error) {
	kubeConfigSecret, err := m.KubernetesClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, names.GetKubeconfigSecretName(cluster, ControllerKubeconfigName), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig for workload cluster: %w", err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigSecret.Data[capisecretutil.KubeconfigDataName])
	if err != nil {
		return nil, fmt.Errorf("failed to get REST config for workload cluster: %w", err)
	}
	restConfig.Timeout = 10 * time.Second
	restConfig.Wrap(m.TracingWrapper)

	workloadClusterClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client for workload cluster: %w", err)
	}

	return workloadClusterClient, nil
}
