package workload

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/config"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/coredns"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/konnectivity"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/kubeproxy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/rbac"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
)

type WorkloadClusterReconciler interface {
	ReconcileWorkloadClusterResources(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewWorkloadClusterReconciler(
	kubernetesClient kubernetes.Interface,
	managementCluster ManagementCluster,
	caCertificateDuration time.Duration,
	certificateDuration time.Duration,
	serviceDomain string,
	serviceCIDR string,
	podCIDR string,
	dnsIP net.IP,
	konnectivityNamespace string,
	konnectivityServiceAccount string,
	konnectivityServerAudience string,
	konnectivityServicePort int32,
) WorkloadClusterReconciler {
	return &workloadClusterReconciler{
		kubernetesClient:                    kubernetesClient,
		managementCluster:                   managementCluster,
		caCertificateDuration:               caCertificateDuration,
		certificateDuration:                 certificateDuration,
		serviceDomain:                       serviceDomain,
		serviceCIDR:                         serviceCIDR,
		podCIDR:                             podCIDR,
		dnsIP:                               dnsIP,
		konnectivityNamespace:               konnectivityNamespace,
		konnectivityServiceAccount:          konnectivityServiceAccount,
		konnectivityServerAudience:          konnectivityServerAudience,
		konnectivityServicePort:             konnectivityServicePort,
		konnectivityServiceAccountName:      "konnectivity-agent",
		konnectivityServiceAccountTokenName: "konnectivity-agent-token",
		kubeadmKubeletConfigMapNamespace:    metav1.NamespaceSystem,
		kubeadmConfigConfigMapName:          konstants.KubeadmConfigConfigMap,
		kubeletConfigMapName:                konstants.KubeletBaseConfigurationConfigMap,
		clusterInfoConfigMapNamespace:       metav1.NamespacePublic,
		clusterInfoConfigMapName:            bootstrapapi.ConfigMapClusterInfo,
		tracer:                              tracing.GetTracer("workloadCluster"),
	}
}

type workloadClusterReconciler struct {
	kubernetesClient                    kubernetes.Interface
	managementCluster                   ManagementCluster
	caCertificateDuration               time.Duration
	certificateDuration                 time.Duration
	serviceDomain                       string
	serviceCIDR                         string
	podCIDR                             string
	dnsIP                               net.IP
	konnectivityNamespace               string
	konnectivityServiceAccount          string
	konnectivityServerAudience          string
	konnectivityServicePort             int32
	konnectivityServiceAccountName      string
	konnectivityServiceAccountTokenName string
	kubeadmKubeletConfigMapNamespace    string
	kubeadmConfigConfigMapName          string
	kubeletConfigMapName                string
	clusterInfoConfigMapNamespace       string
	clusterInfoConfigMapName            string
	tracer                              string
}

var _ WorkloadClusterReconciler = &workloadClusterReconciler{}

func (wr *workloadClusterReconciler) ReconcileWorkloadClusterResources(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, wr.tracer, "ReconcileWorkloadSetup",
		func(ctx context.Context, span trace.Span) (string, error) {
			type WorkloadPhase struct {
				Reconcile    func(context.Context, *capiv2.Cluster) (string, error)
				Condition    capiv2.ConditionType
				FailedReason string
				Name         string
			}

			workloadClusterClient, err := wr.managementCluster.GetWorkloadClusterClient(ctx, cluster)
			if err != nil {
				return "", fmt.Errorf("failed to get workload cluster client: %w", err)
			}

			rbacReconciler := rbac.NewRBACReconciler(
				workloadClusterClient,
				wr.kubeadmKubeletConfigMapNamespace,
				wr.kubeadmConfigConfigMapName,
				wr.kubeletConfigMapName,
				wr.clusterInfoConfigMapNamespace,
				wr.clusterInfoConfigMapName,
			)

			configReconciler := config.NewConfigReconciler(
				workloadClusterClient,
				wr.caCertificateDuration,
				wr.certificateDuration,
				wr.serviceDomain,
				wr.serviceCIDR,
				wr.podCIDR,
				wr.dnsIP,
				wr.kubeadmConfigConfigMapName,
				wr.kubeadmKubeletConfigMapNamespace,
				wr.clusterInfoConfigMapName,
				wr.clusterInfoConfigMapNamespace,
				wr.kubeletConfigMapName,
				wr.kubeadmKubeletConfigMapNamespace,
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
				wr.konnectivityNamespace,
				wr.konnectivityServiceAccount,
				wr.konnectivityServerAudience,
				wr.konnectivityServicePort,
			)

			workloadPhases := []WorkloadPhase{
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context, _ *capiv2.Cluster) (string, error) {
						return rbacReconciler.ReconcileRBAC(ctx)
					},
					Condition:    v1alpha1.WorkloadRBACReadyCondition,
					FailedReason: v1alpha1.WorkloadRBACFailedReason,
				},
				{
					Name: "cluster-info",
					Reconcile: func(ctx context.Context, cluster *capiv2.Cluster) (string, error) {
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
					Reconcile: func(ctx context.Context, cluster *capiv2.Cluster) (string, error) {
						return "", configReconciler.ReconcileKubeadmConfig(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKubeadmConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeadmConfigFailedReason,
				},
				{
					Name: "kubelet-config",
					Reconcile: func(ctx context.Context, _ *capiv2.Cluster) (string, error) {
						return "", configReconciler.ReconcileKubeletConfig(ctx)
					},
					Condition:    v1alpha1.WorkloadKubeletConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeletConfigFailedReason,
				},
				{
					Name: "kube-proxy",
					Reconcile: func(ctx context.Context, cluster *capiv2.Cluster) (string, error) {
						return kubeProxyReconciler.ReconcileKubeProxy(
							ctx,
							hostedControlPlane, cluster,
							hostedControlPlane.Spec.KubeProxy.Disabled,
						)
					},
					Condition:    v1alpha1.WorkloadKubeProxyReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeProxyFailedReason,
				},
				{
					Name: "coredns",
					Reconcile: func(ctx context.Context, _ *capiv2.Cluster) (string, error) {
						return coreDNSReconciler.ReconcileCoreDNS(ctx)
					},
					Condition:    v1alpha1.WorkloadCoreDNSReadyCondition,
					FailedReason: v1alpha1.WorkloadCoreDNSFailedReason,
				},
				{
					Name: "konnectivity",
					Reconcile: func(ctx context.Context, cluster *capiv2.Cluster) (string, error) {
						return konnectivityReconciler.ReconcileKonnectivity(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKonnectivityReadyCondition,
					FailedReason: v1alpha1.WorkloadKonnectivityFailedReason,
				},
			}

			for _, phase := range workloadPhases {
				switch notReadyReason, err := phase.Reconcile(ctx, cluster); {
				case err != nil:
					conditions.Set(hostedControlPlane, metav1.Condition{
						Type:    string(phase.Condition),
						Status:  metav1.ConditionFalse,
						Reason:  phase.FailedReason,
						Message: fmt.Sprintf("Reconciling workload phase %s failed: %v", phase.Name, err),
					})
					return "", err
				case notReadyReason != "":
					conditions.Set(hostedControlPlane, metav1.Condition{
						Type:    string(phase.Condition),
						Status:  metav1.ConditionFalse,
						Reason:  notReadyReason,
						Message: fmt.Sprintf("Reconciling workload phase %s not ready: %s", phase.Name, notReadyReason),
					})
					return notReadyReason, nil
				default:
					conditions.Set(hostedControlPlane, metav1.Condition{
						Type:   string(phase.Condition),
						Status: metav1.ConditionTrue,
						Reason: "ReconcileSucceeded",
					})
				}
			}

			hostedControlPlane.Status.Initialized = true

			return "", nil
		},
	)
}
