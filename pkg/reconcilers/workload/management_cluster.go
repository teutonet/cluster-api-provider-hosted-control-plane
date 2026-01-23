package workload

import (
	"context"
	"errors"
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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

var ErrCiliumNotInstalled = errors.New("cilium not installed")

type WorkloadClusterClientFactory = func(
	ctx context.Context,
	managementClusterClient *alias.ManagementClusterClient,
	cluster *capiv2.Cluster,
) (*alias.WorkloadClusterClient, ciliumclient.Interface, error)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func GetWorkloadClusterClient(
	ctx context.Context,
	managementClusterClient *alias.ManagementClusterClient,
	cluster *capiv2.Cluster,
	tracingWrapper func(http.RoundTripper) http.RoundTripper,
	controllerUsername string,
) (*alias.WorkloadClusterClient, ciliumclient.Interface, error) {
	return tracing.WithSpan3(ctx, "managementCluster", "GetWorkloadClusterClient",
		func(ctx context.Context, span trace.Span) (*alias.WorkloadClusterClient, ciliumclient.Interface, error) {
			kubeconfigSecretName := names.GetKubeconfigSecretName(cluster, controllerUsername)
			span.SetAttributes(
				attribute.String(
					"kubeconfig.secret.name",
					kubeconfigSecretName,
				),
			)
			kubeConfigSecret, err := managementClusterClient.CoreV1().Secrets(cluster.Namespace).
				Get(ctx, kubeconfigSecretName, metav1.GetOptions{})
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
			restConfig.Wrap(tracingWrapper)

			workloadClusterClientSet, err := kubernetes.NewForConfig(restConfig)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create Kubernetes clientSet for workload cluster: %w", err)
			}
			workloadClusterClient := alias.WorkloadClusterClient{
				Interface: workloadClusterClientSet,
			}

			ciliumClient, err := GetCiliumClient(workloadClusterClient, restConfig)
			if err != nil && !errors.Is(err, ErrCiliumNotInstalled) {
				return nil, nil, err
			}

			return &workloadClusterClient, ciliumClient, nil
		},
	)
}

func GetCiliumClient(
	kubernetesClient kubernetes.Interface, restConfig *rest.Config,
) (ciliumclient.Interface, error) {
	groups, err := kubernetesClient.Discovery().ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("failed to discover API groups for workload cluster: %w", err)
	}
	if slices.NoneBy(groups.Groups, func(group metav1.APIGroup) bool {
		return group.Name == ciliumv2.SchemeGroupVersion.Group
	}) {
		return nil, ErrCiliumNotInstalled
	}

	var ciliumClient ciliumclient.Interface
	ciliumClient, err = ciliumclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cilium client for workload cluster: %w", err)
	}
	return ciliumClient, nil
}
