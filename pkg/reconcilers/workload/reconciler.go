package workload

import (
	"context"
	"fmt"
	"net"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/config"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/coredns"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/konnectivity"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/kubeproxy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/rbac"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/kubernetes"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
)

type WorkloadClusterReconciler interface {
	ReconcileWorkloadClusterResources(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) (string, error)
}

func NewWorkloadClusterReconciler(
	kubernetesClient kubernetes.Interface,
	managementCluster ManagementCluster,
	serviceDomain string,
	serviceCIDR string,
	podCIDR string,
	dnsIP net.IP,
	konnectivityServerAudience string,
	konnectivityServicePort int32,
) WorkloadClusterReconciler {
	return &workloadClusterReconciler{
		kubernetesClient:                    kubernetesClient,
		managementCluster:                   managementCluster,
		serviceDomain:                       serviceDomain,
		serviceCIDR:                         serviceCIDR,
		podCIDR:                             podCIDR,
		dnsIP:                               dnsIP,
		konnectivityServerAudience:          konnectivityServerAudience,
		konnectivityServicePort:             konnectivityServicePort,
		konnectivityServiceAccountName:      "konnectivity-agent",
		konnectivityServiceAccountTokenName: "konnectivity-agent-token",
		tracer:                              tracing.GetTracer("workloadCluster"),
	}
}

type workloadClusterReconciler struct {
	kubernetesClient                    kubernetes.Interface
	managementCluster                   ManagementCluster
	serviceDomain                       string
	serviceCIDR                         string
	podCIDR                             string
	dnsIP                               net.IP
	konnectivityServerAudience          string
	konnectivityServicePort             int32
	konnectivityServiceAccountName      string
	konnectivityServiceAccountTokenName string
	tracer                              string
}

var _ WorkloadClusterReconciler = &workloadClusterReconciler{}

func (wr *workloadClusterReconciler) ReconcileWorkloadClusterResources(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, wr.tracer, "ReconcileWorkloadSetup",
		func(ctx context.Context, span trace.Span) (string, error) {
			type WorkloadPhase struct {
				Reconcile    func(context.Context, *capiv1.Cluster) (string, error)
				Disabled     bool
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			workloadClusterClient, err := wr.managementCluster.GetWorkloadClusterClient(ctx, cluster)
			if err != nil {
				return "", fmt.Errorf("failed to get workload cluster client: %w", err)
			}

			rbacReconciler := rbac.NewRBACReconciler(
				workloadClusterClient,
			)

			configReconciler := config.NewConfigReconciler(
				workloadClusterClient,
				wr.serviceDomain,
				wr.serviceCIDR,
				wr.podCIDR,
				wr.dnsIP,
			)

			kubeProxyReconciler := kubeproxy.NewKubeProxyReconciler(
				workloadClusterClient,
				wr.podCIDR,
			)

			coreDNSReconciler := coredns.NewCoreDNSReconciler(
				workloadClusterClient,
				wr.serviceDomain,
				wr.dnsIP,
			)

			konnectivityReconciler := konnectivity.NewKonnectivityReconciler(
				workloadClusterClient,
				wr.konnectivityServerAudience,
				wr.konnectivityServicePort,
			)

			workloadPhases := []WorkloadPhase{
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context, _ *capiv1.Cluster) (string, error) {
						return rbacReconciler.ReconcileRBAC(ctx)
					},
					Condition:    v1alpha1.WorkloadRBACReadyCondition,
					FailedReason: v1alpha1.WorkloadRBACFailedReason,
				},
				{
					Name: "cluster-info",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) (string, error) {
						return "", configReconciler.ReconcileClusterInfoConfigMap(
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
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) (string, error) {
						return "", configReconciler.ReconcileKubeadmConfig(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKubeadmConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeadmConfigFailedReason,
				},
				{
					Name: "kubelet-config",
					Reconcile: func(ctx context.Context, _ *capiv1.Cluster) (string, error) {
						return "", configReconciler.ReconcileKubeletConfig(ctx)
					},
					Condition:    v1alpha1.WorkloadKubeletConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeletConfigFailedReason,
				},
				{
					Name:     "kube-proxy",
					Disabled: hostedControlPlane.Spec.KubeProxy.Disabled,
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) (string, error) {
						return "", kubeProxyReconciler.ReconcileKubeProxy(ctx, cluster, hostedControlPlane)
					},
					Condition:    v1alpha1.WorkloadKubeProxyReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeProxyFailedReason,
				},
				{
					Name: "coredns",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) (string, error) {
						return "", coreDNSReconciler.ReconcileCoreDNS(ctx, cluster)
					},
					Condition:    v1alpha1.WorkloadCoreDNSReadyCondition,
					FailedReason: v1alpha1.WorkloadCoreDNSFailedReason,
				},
				{
					Name: "konnectivity",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) (string, error) {
						return konnectivityReconciler.ReconcileKonnectivity(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKonnectivityReadyCondition,
					FailedReason: v1alpha1.WorkloadKonnectivityFailedReason,
				},
			}

			for _, phase := range workloadPhases {
				if !phase.Disabled {
					if notReadyReason, err := phase.Reconcile(ctx, cluster); err != nil {
						conditions.MarkFalse(
							hostedControlPlane,
							phase.Condition,
							phase.FailedReason,
							capiv1.ConditionSeverityError,
							"Reconciling workload phase %s failed: %v", phase.Name, err,
						)
						return "", err
					} else if notReadyReason != "" {
						conditions.MarkFalse(
							hostedControlPlane,
							phase.Condition,
							notReadyReason,
							capiv1.ConditionSeverityInfo,
							"Reconciling workload phase %s not ready: %s", phase.Name, notReadyReason,
						)
						return notReadyReason, nil
					} else {
						conditions.MarkTrue(hostedControlPlane, phase.Condition)
					}
				}
			}

			hostedControlPlane.Status.Initialized = true

			return "", nil
		},
	)
}
