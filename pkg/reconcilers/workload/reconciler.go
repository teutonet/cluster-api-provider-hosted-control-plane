package workload

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/coredns"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/konnectivity"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/kubeproxy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload/rbac"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	kubelettypes "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmv1beta4 "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta4"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/config"
	kubeletv1beta1 "k8s.io/kubernetes/pkg/kubelet/apis/config/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
)

type WorkloadClusterReconciler interface {
	ReconcileWorkloadClusterResources(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
}

func NewWorkloadClusterReconciler(
	kubernetesClient kubernetes.Interface,
	managementCluster ManagementCluster,
	konnectivityServerAudience string,
	konnectivityServicePort int32,
) WorkloadClusterReconciler {
	return &workloadClusterReconciler{
		kubernetesClient:                    kubernetesClient,
		managementCluster:                   managementCluster,
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

			kubeProxyReconciler := kubeproxy.NewKubeProxyReconciler(
				workloadClusterClient,
			)

			coreDNSReconciler := coredns.NewCoreDNSReconciler(
				workloadClusterClient,
			)

			rbacReconciler := rbac.NewRBACReconciler(
				workloadClusterClient,
			)

			konnectivityReconciler := konnectivity.NewKonnectivityReconciler(
				workloadClusterClient,
				wr.konnectivityServerAudience,
				wr.konnectivityServicePort,
			)

			workloadPhases := []WorkloadPhase{
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context, _ *capiv1.Cluster) error {
						return rbacReconciler.ReconcileRBAC(ctx)
					},
					Condition:    v1alpha1.WorkloadRBACReadyCondition,
					FailedReason: v1alpha1.WorkloadRBACFailedReason,
				},
				{
					Name: "cluster-info",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return wr.reconcileClusterInfoConfigMap(
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
						return wr.reconcileKubeadmConfig(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKubeadmConfigReadyCondition,
					FailedReason: v1alpha1.WorkloadKubeadmConfigFailedReason,
				},
				{
					Name: "kubelet-config",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return wr.reconcileKubeletConfig(ctx, hostedControlPlane, cluster)
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
					Name: "konnectivity",
					Reconcile: func(ctx context.Context, cluster *capiv1.Cluster) error {
						return konnectivityReconciler.ReconcileKonnectivity(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.WorkloadKonnectivityRBACReadyCondition,
					FailedReason: v1alpha1.WorkloadKonnectivityRBACFailedReason,
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

func (wr *workloadClusterReconciler) reconcileClusterInfoConfigMap(
	ctx context.Context,
	managementClient kubernetes.Interface,
	cluster *capiv1.Cluster,
) error {
	caSecret, err := managementClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CA secret: %w", err)
	}
	kubeconfig := &api.Config{
		Clusters: map[string]*api.Cluster{
			"": {
				Server:                   fmt.Sprintf("https://%s", cluster.Spec.ControlPlaneEndpoint.String()),
				CertificateAuthorityData: caSecret.Data[corev1.TLSCertKey],
			},
		},
	}
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
	}

	configMap := corev1ac.ConfigMap(bootstrapapi.ConfigMapClusterInfo, metav1.NamespacePublic).
		WithData(map[string]string{
			bootstrapapi.KubeConfigKey: string(kubeconfigBytes),
		})

	_, err = wr.kubernetesClient.CoreV1().
		ConfigMaps(metav1.NamespacePublic).
		Apply(ctx, configMap, operatorutil.ApplyOptions)
	return errorsUtil.IfErrErrorf("failed to apply cluster info configmap: %w", err)
}

func (wr *workloadClusterReconciler) reconcileKubeadmConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, wr.tracer, "reconcileKubeadmConfig",
		func(ctx context.Context, span trace.Span) error {
			initConfiguration, err := config.DefaultedStaticInitConfiguration()
			if err != nil {
				return fmt.Errorf("failed to get defaulted static init configuration: %w", err)
			}
			conf := initConfiguration.ClusterConfiguration
			conf.Networking = kubeadm.Networking{
				DNSDomain:     "cluster.local",
				PodSubnet:     "192.168.0.0/16",
				ServiceSubnet: "10.96.0.0/12",
			}
			conf.KubernetesVersion = hostedControlPlane.Spec.Version
			conf.ControlPlaneEndpoint = cluster.Spec.ControlPlaneEndpoint.String()
			conf.ClusterName = cluster.Name

			clusterConfiguration, err := config.MarshalKubeadmConfigObject(&conf, kubeadmv1beta4.SchemeGroupVersion)
			if err != nil {
				return fmt.Errorf("failed to marshal cluster configuration: %w", err)
			}
			configMap := corev1ac.ConfigMap(konstants.KubeadmConfigConfigMap, metav1.NamespaceSystem).
				WithData(
					map[string]string{
						konstants.ClusterConfigurationKind: string(clusterConfiguration),
					},
				)

			_, err = wr.kubernetesClient.CoreV1().
				ConfigMaps(metav1.NamespaceSystem).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply kubeadm config configmap: %w", err)
		},
	)
}

func (wr *workloadClusterReconciler) reconcileKubeletConfig(
	ctx context.Context,
	_ *v1alpha1.HostedControlPlane,
	_ *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, wr.tracer, "reconcileKubeadmConfig",
		func(ctx context.Context, span trace.Span) error {
			var kubeletConfiguration kubelettypes.KubeletConfiguration

			kubeletv1beta1.SetDefaults_KubeletConfiguration(&kubeletConfiguration)

			kubeletConfiguration.APIVersion = kubelettypes.SchemeGroupVersion.String()
			kubeletConfiguration.Kind = "KubeletConfiguration"
			kubeletConfiguration.Authentication.X509.ClientCAFile = "/etc/kubernetes/pki/ca.crt"
			kubeletConfiguration.CgroupDriver = konstants.CgroupDriverSystemd
			kubeletConfiguration.ClusterDNS = []string{"10.96.0.10"}
			kubeletConfiguration.ClusterDomain = "cluster.local"
			kubeletConfiguration.RotateCertificates = true
			kubeletConfiguration.StaticPodPath = kubeadmv1beta4.DefaultManifestsDir
			kubeletConfiguration.Logging.FlushFrequency.SerializeAsString = false
			kubeletConfiguration.ResolverConfig = nil

			content, err := operatorutil.ToYaml(&kubeletConfiguration)
			if err != nil {
				return fmt.Errorf("failed to marshal kubelet configuration: %w", err)
			}

			configMap := corev1ac.ConfigMap(konstants.KubeletBaseConfigurationConfigMap, metav1.NamespaceSystem).
				WithData(
					map[string]string{
						konstants.KubeletBaseConfigurationConfigMapKey: content.String(),
					},
				)

			_, err = wr.kubernetesClient.CoreV1().
				ConfigMaps(metav1.NamespaceSystem).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply kubeadm config configmap: %w", err)
		},
	)
}
