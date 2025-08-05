package hostedcontrolplane

import (
	"context"
	"fmt"
	"math"
	"path"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubeadm "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta3"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

type DeploymentReconciler struct {
	kubernetesClient kubernetes.Interface
}

var (
	egressSelectorConfigMountPath       = "/etc/kubernetes/egress/configurations"
	konnectivitySocketPath              = "/run/konnectivity"
	EgressSelectorConfigurationFileName = "egress-selector-configuration.yaml"
	APIServerPortName                   = "api"
)

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;update;patch

func (dr *DeploymentReconciler) ReconcileDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) error {
			caCertificateVolume := dr.createCACertificateVolume(hostedControlPlane)
			frontProxyCACertificateVolume := dr.createFrontProxyCACertificateVolume(hostedControlPlane)
			apiServerCertificateVolume := dr.createAPIServerCertificateVolume(hostedControlPlane)
			frontProxyClientCertificateVolume := dr.createFrontProxyClientCertificateVolume(hostedControlPlane)
			serviceAccountCertificateVolume := dr.createServiceAccountCertificateVolume(hostedControlPlane)
			egressSelectorConfigVolume := dr.createKonnectivityConfigVolume(hostedControlPlane)
			schedulerKubeconfigVolume := dr.createSchedulerKubeconfigVolume(hostedControlPlane)

			caCertificateVolumeMount := corev1ac.VolumeMount().
				WithName(*caCertificateVolume.Name).
				WithMountPath(path.Join(kubeadm.DefaultCertificatesDir, konstants.CACertAndKeyBaseName)).
				WithReadOnly(true)
			globalCACertificateVolumeMount := corev1ac.VolumeMount().
				WithName(*caCertificateVolume.Name).
				WithMountPath(path.Join("/etc/ssl/certs", konstants.CACertName)).
				WithSubPath(konstants.CACertName).
				WithReadOnly(true)
			frontProxyCACertificateVolumeMount := corev1ac.VolumeMount().
				WithName(*frontProxyCACertificateVolume.Name).
				WithMountPath(path.Join(kubeadm.DefaultCertificatesDir, konstants.FrontProxyCACertAndKeyBaseName)).
				WithReadOnly(true)
			frontProxyClientCertificateVolumeMount := corev1ac.VolumeMount().
				WithName(*frontProxyClientCertificateVolume.Name).
				WithMountPath(path.Join(kubeadm.DefaultCertificatesDir, konstants.FrontProxyClientCertAndKeyBaseName)).
				WithReadOnly(true)
			serviceAccountCertificateVolumeMount := corev1ac.VolumeMount().
				WithName(*serviceAccountCertificateVolume.Name).
				WithMountPath(path.Join(kubeadm.DefaultCertificatesDir, konstants.ServiceAccountKeyBaseName)).
				WithReadOnly(true)
			egressSelectorConfigVolumeMount := corev1ac.VolumeMount().
				WithName(*egressSelectorConfigVolume.Name).
				WithMountPath(egressSelectorConfigMountPath).
				WithReadOnly(true)

			deployment := appsv1ac.Deployment(hostedControlPlane.Name, hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithSpec(appsv1ac.DeploymentSpec().
					WithReplicas(*hostedControlPlane.Spec.Replicas).
					WithSelector(metav1ac.LabelSelector().
						WithMatchLabels(names.GetSelector(hostedControlPlane.Name)),
					).
					WithTemplate(corev1ac.PodTemplateSpec().
						WithLabels(names.GetLabels(hostedControlPlane.Name)).
						WithSpec(corev1ac.PodSpec().
							WithTopologySpreadConstraints(corev1ac.TopologySpreadConstraint().
								WithTopologyKey("kubernetes.io/hostname").
								WithLabelSelector(metav1ac.LabelSelector().
									WithMatchLabels(names.GetSelector(hostedControlPlane.Name)),
								),
							).
							WithContainers(
								dr.createAPIServerContainer(
									hostedControlPlane,
									globalCACertificateVolumeMount,
									caCertificateVolumeMount,
									frontProxyCACertificateVolumeMount,
									apiServerCertificateVolume,
									frontProxyClientCertificateVolumeMount,
									serviceAccountCertificateVolumeMount,
									egressSelectorConfigVolumeMount,
								),
								dr.createControllerManagerContainer(
									hostedControlPlane,
									caCertificateVolume,
									controllerManagerKubeconfigVolume,
								),
								dr.createSchedulerContainer(
									hostedControlPlane,
									schedulerKubeconfigVolume,
								),
							).
							WithVolumes(
								caCertificateVolume,
								egressSelectorConfigVolume,
							),
						),
					),
				).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane))

			_, err := dr.kubernetesClient.AppsV1().Deployments(hostedControlPlane.Namespace).Apply(ctx,
				deployment,
				applyOptions,
			)

			return errorsUtil.IfErrErrorf("failed to patch deployment: %w", err)
		},
	)
}

func (dr *DeploymentReconciler) createSchedulerKubeconfigVolume(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("kube-scheduler-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(hostedControlPlane.Name, konstants.KubeScheduler)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithKey(konstants.SchedulerKubeConfigFileName),
			),
		)
}

func (dr *DeploymentReconciler) createKonnectivityConfigVolume(hostedControlPlane *v1alpha1.HostedControlPlane) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-config").
		WithConfigMap(corev1ac.ConfigMapVolumeSource().
			WithName(names.GetKonnectivityConfigMapName(hostedControlPlane.Name)),
		)
}

func (dr *DeploymentReconciler) createAPIServerCertificateVolume(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("k8s-certs").
		WithProjected(corev1ac.ProjectedVolumeSource().
			WithSources(
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetAPIServerSecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.APIServerCertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.APIServerKeyName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetAPIServerKubeletClientSecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.APIServerKubeletClientCertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.APIServerKubeletClientKeyName),
					),
				),
			),
		)
}

func (dr *DeploymentReconciler) createCACertificateVolume(hostedControlPlane *v1alpha1.HostedControlPlane) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("k8s-ca-certs").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetCASecretName(hostedControlPlane.Name)).
			WithItems(corev1ac.KeyToPath().
				WithKey(corev1.TLSCertKey).
				WithPath(konstants.CACertName),
			),
		)
}

func (dr *DeploymentReconciler) buildAPIServerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	caCertsVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	frontProxyCACertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	apiServerCertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	frontProxyClientCertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	serviceAccountCertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	// TODO: add etcd flags

	frontProxyClientCertificateDir := *frontProxyClientCertificateVolumeMount.MountPath
	frontProxyCACertificateDir := *frontProxyCACertificateVolumeMount.MountPath
	serviceAccountCertificateDir := *serviceAccountCertificateVolumeMount.MountPath
	apiServerCertificateDir := *apiServerCertificateVolumeMount.MountPath
	nodeAdressTypes := slices.Map([]corev1.NodeAddressType{
		corev1.NodeInternalDNS,
		corev1.NodeExternalDNS,
		corev1.NodeHostName,
	},
		func(item corev1.NodeAddressType, _ int) string {
			return string(item)
		})

	args := map[string]string{
		"allow-privileged":   "true",
		"authorization-mode": "Node,RBAC",
		"client-ca-file":     path.Join(*caCertsVolumeMount.MountPath, konstants.CACertName),
		"egress-selector-config-file": path.Join(
			*egressSelectorConfigVolumeMount.MountPath,
			"configurations",
			EgressSelectorConfigurationFileName,
		),
		"enable-bootstrap-token-auth":        "true",
		"kubelet-client-certificate":         path.Join(apiServerCertificateDir, konstants.APIServerKubeletClientCertName),
		"kubelet-client-key":                 path.Join(apiServerCertificateDir, konstants.APIServerKubeletClientKeyName),
		"kubelet-preferred-address-types":    strings.Join(nodeAdressTypes, ","),
		"proxy-client-cert-file":             path.Join(frontProxyClientCertificateDir, konstants.FrontProxyClientCertName),
		"proxy-client-key-file":              path.Join(frontProxyClientCertificateDir, konstants.FrontProxyClientKeyName),
		"requestheader-allowed-names":        konstants.FrontProxyClientCertCommonName,
		"requestheader-client-ca-file":       path.Join(frontProxyCACertificateDir, konstants.FrontProxyCACertName),
		"requestheader-extra-headers-prefix": "X-Remote-Extra-",
		"requestheader-group-headers":        "X-Remote-Group",
		"requestheader-username-headers":     "X-Remote-User",
		"secure-port":                        "6443",
		"service-account-issuer":             "https://kubernetes.default.svc.cluster.local",
		"service-account-key-file":           path.Join(serviceAccountCertificateDir, konstants.ServiceAccountPublicKeyName),
		"service-account-signing-key-file":   path.Join(serviceAccountCertificateDir, konstants.ServiceAccountPrivateKeyName),
		"service-cluster-ip-range":           "10.96.0.0/12",
		"tls-cert-file":                      path.Join(apiServerCertificateDir, konstants.APIServerCertName),
		"tls-private-key-file":               path.Join(apiServerCertificateDir, konstants.APIServerKeyName),
	}

	return dr.argsToSlice(hostedControlPlane.Spec.Deployment.APIServer.Args, args)
}

func (dr *DeploymentReconciler) argsToSlice(args ...map[string]string) []string {
	return slices.MapToSlice(slices.Assign(args...), func(key string, value string) string {
		return fmt.Sprintf("--%s=%s", key, value)
	})
}

func (dr *DeploymentReconciler) createAPIServerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	caCertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	globalCACertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	frontProxyCACertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	apiServerCertificateVolume *corev1ac.VolumeApplyConfiguration,
	frontProxyClientCertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	serviceAccountCertificateVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	apiPort := corev1ac.ContainerPort().
		WithName(APIServerPortName).
		WithContainerPort(konstants.KubeAPIServerPort).
		WithProtocol(corev1.ProtocolTCP)
	probePort := dr.createProbePort(int(*apiPort.ContainerPort))

	apiServerCertificateVolumeMount := corev1ac.VolumeMount().
		WithName(*apiServerCertificateVolume.Name).
		WithMountPath(path.Join(kubeadm.DefaultCertificatesDir, konstants.APIServerCertAndKeyBaseName)).
		WithReadOnly(true)

	return corev1ac.Container().
		WithName(konstants.KubeAPIServer).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-apiserver:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithArgs(dr.buildAPIServerArgs(
			hostedControlPlane,
			caCertificateVolumeMount,
			frontProxyCACertificateVolumeMount,
			apiServerCertificateVolumeMount,
			frontProxyClientCertificateVolumeMount,
			serviceAccountCertificateVolumeMount,
			egressSelectorConfigVolumeMount,
		)...).
		WithPorts(apiPort, probePort).
		WithStartupProbe(dr.createStartupProbe(probePort)).
		WithReadinessProbe(dr.createReadinessProbe(probePort)).
		WithLivenessProbe(dr.createLivenessProbe(probePort)).
		WithVolumeMounts(
			globalCACertificateVolumeMount,
			caCertificateVolumeMount,
			apiServerCertificateVolumeMount,
			egressSelectorConfigVolumeMount,
		)
}

func (dr *DeploymentReconciler) createStartupProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	healthCheckTimeout := konstants.ControlPlaneComponentHealthCheckTimeout.Seconds()
	periodSeconds := int32(10)
	failureThreshold := int32(math.Ceil(healthCheckTimeout / float64(periodSeconds)))
	return dr.createProbe("/livez", probePort).
		WithInitialDelaySeconds(periodSeconds).
		WithTimeoutSeconds(15).
		WithFailureThreshold(failureThreshold).
		WithPeriodSeconds(periodSeconds)
}

func (dr *DeploymentReconciler) createReadinessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return dr.createProbe("/readyz", probePort).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(3).
		WithPeriodSeconds(1)
}

func (dr *DeploymentReconciler) createLivenessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return dr.createProbe("/livez", probePort).
		WithInitialDelaySeconds(10).
		WithTimeoutSeconds(15).
		WithFailureThreshold(8).
		WithPeriodSeconds(10)
}

func (dr *DeploymentReconciler) createProbe(path string, probePort *corev1ac.ContainerPortApplyConfiguration) *corev1ac.ProbeApplyConfiguration {
	return corev1ac.Probe().WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath(path).
		WithPort(intstr.FromString(*probePort.Name)).
		WithScheme(corev1.URISchemeHTTPS),
	)
}

func (dr *DeploymentReconciler) createProbePort(port int) *corev1ac.ContainerPortApplyConfiguration {
	containerPort := corev1ac.ContainerPort().
		WithName("probe-port"). // TODO: use konstants.probePort when available
		WithContainerPort(int32(port)).
		WithProtocol(corev1.ProtocolTCP)
	return containerPort
}

func (dr *DeploymentReconciler) createSchedulerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	probePort := dr.createProbePort(konstants.KubeSchedulerPort)
	schedulerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*schedulerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	return corev1ac.Container().
		WithName(konstants.KubeScheduler).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-scheduler:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithArgs(dr.buildSchedulerArgs(hostedControlPlane, schedulerKubeconfigVolumeMount)...).
		WithPorts(probePort).
		WithStartupProbe(dr.createStartupProbe(probePort)).
		WithReadinessProbe(dr.createReadinessProbe(probePort)).
		WithLivenessProbe(dr.createLivenessProbe(probePort)).
		WithVolumeMounts(schedulerKubeconfigVolumeMount)
}

func (dr *DeploymentReconciler) buildSchedulerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	kubeconfigPath := path.Join(*schedulerKubeconfigVolumeMount.MountPath, konstants.SchedulerKubeConfigFileName)

	// use map[string]any as soon as https://github.com/kubernetes-sigs/controller-tools/issues/636 is resolved
	args := map[string]string{
		"authentication-config":    kubeconfigPath,
		"authorization-kubeconfig": kubeconfigPath,
		"kubeconfig":               kubeconfigPath,
		"bind-address":             "0.0.0.0",
		"leader-elect":             "true",
	}

	return dr.argsToSlice(hostedControlPlane.Spec.Deployment.Scheduler.Args, args)
}
