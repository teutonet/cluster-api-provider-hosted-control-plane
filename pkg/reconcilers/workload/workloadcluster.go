package workload

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/coredns"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/kubeproxy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
)

func (wr *workloadClusterReconciler) ReconcileWorkloadClusterResources(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, wr.tracer, "ReconcileWorkloadSetup",
		func(ctx context.Context, span trace.Span) error {
			type WorkloadPhase struct {
				Reconcile    func(context.Context, *capiv1.Cluster) error
				Disabled     bool
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			workloadClusterClient, err := wr.managementCluster.GetWorkloadClusterClient(ctx, cluster)
			if err != nil {
				return fmt.Errorf("failed to get workload cluster client: %w", err)
			}

			workloadClusterReconciler := &workloadClusterReconciler{
				kubernetesClient: workloadClusterClient,
			}

			kubeProxyReconciler := kubeproxy.NewKubeProxyReconciler(
				workloadClusterClient,
			)

			coreDNSReconciler := coredns.NewCoreDNSReconciler(
				workloadClusterClient,
			)

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
						return workloadClusterReconciler.ReconcileClusterInfoConfigMap(
							ctx,
							wr.kubernetesClient,
							cluster,
						)
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
				{
					Name:     "kube-proxy",
					Disabled: hostedControlPlane.Spec.KubeProxy.Disabled,
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return kubeProxyReconciler.ReconcileKubeProxy(ctx, cluster, hostedControlPlane)
					},
					Condition:    v1alpha1.WorkloadKubeProxyReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeProxyFailedReason,
				},
				{
					Name:         "coredns",
					Reconcile:    coreDNSReconciler.ReconcileCoreDNS,
					Condition:    v1alpha1.WorkloadCoreDNSReadyCondition,
					FailedReason: v1alpha1.WorkloadCoreDNSFailedReason,
				},
				{
					Name: "konnectivity-rbac",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return workloadClusterReconciler.ReconcileKonnectivityRBAC(ctx)
					},
					Condition:    v1alpha1.WorkloadKonnectivityRBACReadyCondition,
					FailedReason: v1alpha1.WorkloadKonnectivityRBACFailedReason,
				},
				{
					Name: "konnectivity-daemonset",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return workloadClusterReconciler.ReconcileKonnectivityDaemonSet(
							ctx,
							hostedControlPlane,
							cluster,
						)
					},
					Condition:    v1alpha1.WorkloadKonnectivityDaemonSetReadyCondition,
					FailedReason: v1alpha1.WorkloadKonnectivityDaemonSetFailedReason,
				},
			}

			for _, phase := range workloadPhases {
				if !phase.Disabled {
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
			}

			hostedControlPlane.Status.Initialized = true

			return nil
		},
	)
}
