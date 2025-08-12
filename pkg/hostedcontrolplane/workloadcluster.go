package hostedcontrolplane

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
)

func (r *HostedControlPlaneReconciler) reconcileWorkloadClusterResources(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileWorkloadSetup",
		func(ctx context.Context, span trace.Span) error {
			type WorkloadPhase struct {
				Reconcile    func(context.Context, *capiv1.Cluster) error
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			workloadClusterClient, err := r.ManagementCluster.GetWorkloadClusterClient(ctx, cluster)
			if err != nil {
				return fmt.Errorf("failed to get workload cluster client: %w", err)
			}

			workloadClusterReconciler := &WorkloadClusterReconciler{
				kubernetesClient: workloadClusterClient,
			}

			workloadPhases := []WorkloadPhase{
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context, _ *capiv1.Cluster) error {
						return workloadClusterReconciler.ReconcileWorkloadRBAC(ctx)
					},
					Condition:    v1alpha1.WorkloadRBACReadyCondition,
					FailedReason: v1alpha1.WorkloadRBACFailedReason,
				},
				{
					Name: "cluster-info",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return workloadClusterReconciler.ReconcileClusterInfoConfigMap(ctx, r.KubernetesClient, cluster)
					},
					Condition:    v1alpha1.WorkloadClusterInfoReadyCondition,
					FailedReason: v1alpha1.WorkloadClusterInfoFailedReason,
				},
				{
					Name: "kubeadm-config",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return workloadClusterReconciler.ReconcileKubeadmConfig(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKubeadmConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeadmConfigFailedReason,
				},
				{
					Name: "kubelet-config",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return workloadClusterReconciler.ReconcileKubeletConfig(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKubeletConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeletConfigFailedReason,
				},
				// TODO: Add more workload phases here
				// {
				//     Name: "kube-proxy",
				//     Reconcile: func(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane,
				//         workloadClusterClient WorkloadCluster) error {
				//         return workloadClusterClient.ReconcileKubeProxy(ctx, hostedControlPlane)
				//     },
				//     Condition:    v1alpha1.KubeProxyReadyCondition,
				//     FailedReason: v1alpha1.KubeProxyFailedReason,
				// },
			}

			for _, phase := range workloadPhases {
				if err := phase.Reconcile(ctx, cluster); err != nil {
					conditions.MarkFalse(
						hostedControlPlane,
						phase.Condition,
						phase.FailedReason,
						capiv1.ConditionSeverityError,
						"Reconciling phase %s failed: %v", phase.Name, err,
					)
					return err
				} else {
					conditions.MarkTrue(hostedControlPlane, phase.Condition)
				}
			}

			hostedControlPlane.Status.Initialized = true

			return nil
		},
	)
}
