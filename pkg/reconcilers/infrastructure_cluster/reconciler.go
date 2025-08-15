package infrastructure_cluster

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InfrastructureClusterReconciler interface {
	SyncControlPlaneEndpoint(ctx context.Context, cluster *capiv1.Cluster) error
}

func NewInfrastructureClusterReconciler(
	client client.Client,
	apiServerServiceLegacyPortName string,
) InfrastructureClusterReconciler {
	return &infrastructureClusterReconciler{
		client:                         client,
		apiServerServiceLegacyPortName: apiServerServiceLegacyPortName,
		tracer:                         tracing.GetTracer("infrastructureCluster"),
	}
}

type infrastructureClusterReconciler struct {
	client                         client.Client
	apiServerServiceLegacyPortName string
	tracer                         string
}

var _ InfrastructureClusterReconciler = &infrastructureClusterReconciler{}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=*,verbs=get;patch
//+kubebuilder:rbac:groups=core,resources=service,verbs=get

func (i *infrastructureClusterReconciler) SyncControlPlaneEndpoint(
	ctx context.Context,
	cluster *capiv1.Cluster,
) error {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.GetServiceName(cluster),
			Namespace: cluster.Namespace,
		},
	}
	if err := i.client.Get(ctx, client.ObjectKeyFromObject(service), service); err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}

	port := slices.SliceToMap(service.Spec.Ports, func(port corev1.ServicePort) (string, int32) {
		return port.Name, port.Port
	})[i.apiServerServiceLegacyPortName]

	infraCluster := &unstructured.Unstructured{}

	infraCluster.SetGroupVersionKind(cluster.Spec.InfrastructureRef.GroupVersionKind())
	infraCluster.SetName(cluster.Spec.InfrastructureRef.Name)
	infraCluster.SetNamespace(cluster.Spec.InfrastructureRef.Namespace)

	if err := i.client.Get(ctx, client.ObjectKeyFromObject(infraCluster), infraCluster); err != nil {
		return fmt.Errorf("failed to get infrastructure cluster: %w", err)
	}

	patchHelper, err := patch.NewHelper(infraCluster, i.client)
	if err != nil {
		return fmt.Errorf("failed to create patch helper: %w", err)
	}

	if err := unstructured.SetNestedMap(infraCluster.Object, map[string]interface{}{
		"host": service.Status.LoadBalancer.Ingress[0].IP,
		"port": int64(port),
	}, "spec", "controlPlaneEndpoint"); err != nil {
		return fmt.Errorf("failed to set control plane endpoint: %w", err)
	}

	return errorsUtil.IfErrErrorf("failed to patch infrastructure cluster: %w", patchHelper.Patch(ctx, infraCluster))
}
