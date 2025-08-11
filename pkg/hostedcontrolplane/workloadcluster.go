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
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileWorkloadSetup",
		func(ctx context.Context, span trace.Span) error {
			type WorkloadPhase struct {
				Reconcile    func(context.Context, *v1alpha1.HostedControlPlane) error
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			workloadClusterClient, err := r.ManagementCluster.GetWorkloadClusterClient(ctx, hostedControlPlane)
			if err != nil {
				return fmt.Errorf("failed to get workload cluster client: %w", err)
			}

			workloadClusterReconciler := &WorkloadClusterReconciler{
				kubernetesClient: workloadClusterClient,
			}

			workloadPhases := []WorkloadPhase{
				{
					Name:         "kubelet config RBAC",
					Reconcile:    workloadClusterReconciler.ReconcileKubeletConfigRBAC,
					Condition:    v1alpha1.KubeletConfigRBACReadyCondition,
					FailedReason: v1alpha1.KubeletConfigRBACFailedReason,
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
				if err := phase.Reconcile(ctx, hostedControlPlane); err != nil {
					conditions.MarkFalse(
						hostedControlPlane,
						phase.Condition,
						phase.FailedReason,
						capiv1.ConditionSeverityError,
						"Reconciling workload %s failed: %v", phase.Name, err,
					)
					return err
				} else {
					conditions.MarkTrue(hostedControlPlane, phase.Condition)
				}
			}

			return nil
		},
	)
}
