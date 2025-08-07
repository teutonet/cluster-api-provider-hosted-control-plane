package hostedcontrolplane

import (
	"context"

	etcdv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type EtcdClusterReconciler struct {
	kubernetesClient kubernetes.Interface
	client           client.Client
}

//+kubebuilder:rbac:groups=druid.gardener.cloud,resources=etcds,verbs=create;get;update;patch;delete;watch;list

func (er *EtcdClusterReconciler) ReconcileEtcdCluster(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileEtcdCluster",
		func(ctx context.Context, span trace.Span) error {
			etcdCluster := &etcdv1alpha1.Etcd{
				ObjectMeta: metav1.ObjectMeta{
					Name:      names.GetEtcdClusterName(hostedControlPlane.Name),
					Namespace: hostedControlPlane.Namespace,
					Labels:    names.GetLabels(hostedControlPlane.Name),
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         hostedControlPlane.APIVersion,
							Kind:               hostedControlPlane.Kind,
							Name:               hostedControlPlane.Name,
							UID:                hostedControlPlane.UID,
							Controller:         &[]bool{true}[0],
							BlockOwnerDeletion: &[]bool{true}[0],
						},
					},
				},
				Spec: etcdv1alpha1.EtcdSpec{
					Labels:   names.GetLabels(hostedControlPlane.Name),
					Replicas: 3,
					Etcd: etcdv1alpha1.EtcdConfig{

					},
					StorageCapacity: &[]resource.Quantity{resource.MustParse("8Gi")}[0],
				},
			}

			if err := er.client.Create(ctx, etcdCluster); err != nil {
				return errorsUtil.IfErrErrorf("failed to create etcd cluster: %w", err)
			}

			return nil
		},
	)
}
