package config

import (
	"context"
	"fmt"
	"net"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
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
)

type ConfigReconciler interface {
	ReconcileClusterInfoConfigMap(
		ctx context.Context,
		managementClient kubernetes.Interface,
		cluster *capiv1.Cluster,
	) error
	ReconcileKubeadmConfig(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
	ReconcileKubeletConfig(
		ctx context.Context,
	) error
}

func NewConfigReconciler(
	kubernetesClient *alias.WorkloadClusterClient,
	serviceDomain string,
	serviceCIDR string,
	podCIDR string,
	dnsIP net.IP,
) ConfigReconciler {
	return &configReconciler{
		kubernetesClient: kubernetesClient,
		serviceDomain:    serviceDomain,
		serviceCIDR:      serviceCIDR,
		podCIDR:          podCIDR,
		dnsIP:            dnsIP,
		tracer:           tracing.GetTracer("config"),
	}
}

type configReconciler struct {
	kubernetesClient *alias.WorkloadClusterClient
	serviceDomain    string
	serviceCIDR      string
	podCIDR          string
	dnsIP            net.IP
	tracer           string
}

var _ ConfigReconciler = &configReconciler{}

func (cr *configReconciler) ReconcileClusterInfoConfigMap(
	ctx context.Context,
	managementClient kubernetes.Interface,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, cr.tracer, "ReconcileClusterInfoConfigMap",
		func(ctx context.Context, span trace.Span) error {
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

			_, err = cr.kubernetesClient.CoreV1().
				ConfigMaps(*configMap.Namespace).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply cluster info configmap: %w", err)
		},
	)
}

func (cr *configReconciler) ReconcileKubeadmConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, cr.tracer, "reconcileKubeadmConfig",
		func(ctx context.Context, span trace.Span) error {
			initConfiguration, err := config.DefaultedStaticInitConfiguration()
			if err != nil {
				return fmt.Errorf("failed to get defaulted static init configuration: %w", err)
			}
			conf := initConfiguration.ClusterConfiguration
			conf.Networking = kubeadm.Networking{
				DNSDomain:     cr.serviceDomain,
				PodSubnet:     cr.podCIDR,
				ServiceSubnet: cr.serviceCIDR,
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

			_, err = cr.kubernetesClient.CoreV1().
				ConfigMaps(*configMap.Namespace).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply kubeadm config configmap: %w", err)
		},
	)
}

func (cr *configReconciler) ReconcileKubeletConfig(
	ctx context.Context,
) error {
	return tracing.WithSpan1(ctx, cr.tracer, "reconcileKubeletConfig",
		func(ctx context.Context, span trace.Span) error {
			var kubeletConfiguration kubelettypes.KubeletConfiguration

			kubeletv1beta1.SetDefaults_KubeletConfiguration(&kubeletConfiguration)

			kubeletConfiguration.APIVersion = kubelettypes.SchemeGroupVersion.String()
			kubeletConfiguration.Kind = "KubeletConfiguration"
			kubeletConfiguration.Authentication.X509.ClientCAFile = "/etc/kubernetes/pki/ca.crt"
			kubeletConfiguration.CgroupDriver = konstants.CgroupDriverSystemd
			kubeletConfiguration.ClusterDNS = []string{cr.dnsIP.String()}
			kubeletConfiguration.ClusterDomain = cr.serviceDomain
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

			_, err = cr.kubernetesClient.CoreV1().
				ConfigMaps(*configMap.Namespace).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply kubelet config configmap: %w", err)
		},
	)
}
