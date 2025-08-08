package hostedcontrolplane

import (
	"context"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsacv1 "k8s.io/client-go/applyconfigurations/apps/v1"
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
			apiServerCertificatesVolume := dr.createAPIServerCertificatesVolume(hostedControlPlane)
			controllerManagerCertificatesVolume := dr.createControllerManagerCertificatesVolume(hostedControlPlane)
			egressSelectorConfigVolume := dr.createKonnectivityConfigVolume(hostedControlPlane)
			schedulerKubeconfigVolume := dr.createSchedulerKubeconfigVolume(hostedControlPlane)
			controllerManagerKubeconfigVolume := dr.createControllerManagerKubeconfigVolume(hostedControlPlane)

			apiServerCertificatesVolumeMount := corev1ac.VolumeMount().
				WithName(*apiServerCertificatesVolume.Name).
				WithMountPath(kubeadm.DefaultCertificatesDir).
				WithReadOnly(true)
			controllerManagerCertificatesVolumeMount := corev1ac.VolumeMount().
				WithName(*controllerManagerCertificatesVolume.Name).
				WithMountPath(kubeadm.DefaultCertificatesDir).
				WithReadOnly(true)
			egressSelectorConfigVolumeMount := corev1ac.VolumeMount().
				WithName(*egressSelectorConfigVolume.Name).
				WithMountPath(egressSelectorConfigMountPath).
				WithReadOnly(true)

			template := corev1ac.PodTemplateSpec().
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithSpec(corev1ac.PodSpec().
					WithTopologySpreadConstraints(corev1ac.TopologySpreadConstraint().
						WithTopologyKey("kubernetes.io/hostname").
						WithLabelSelector(metav1ac.LabelSelector().
							WithMatchLabels(names.GetSelector(hostedControlPlane.Name)),
						).
						WithMaxSkew(1).
						WithWhenUnsatisfiable(corev1.ScheduleAnyway),
					).
					WithAutomountServiceAccountToken(false).
					WithEnableServiceLinks(false).
					WithContainers(
						dr.createAPIServerContainer(
							hostedControlPlane,
							apiServerCertificatesVolumeMount,
							egressSelectorConfigVolumeMount,
						),
						dr.createControllerManagerContainer(
							hostedControlPlane,
							controllerManagerCertificatesVolumeMount,
							controllerManagerKubeconfigVolume,
						),
						dr.createSchedulerContainer(
							hostedControlPlane,
							schedulerKubeconfigVolume,
						),
						// TODO: add konnectivity container
					).
					WithVolumes(
						apiServerCertificatesVolume,
						controllerManagerCertificatesVolume,
						egressSelectorConfigVolume,
						schedulerKubeconfigVolume,
						controllerManagerKubeconfigVolume,
					),
				)

			secretChecksum, err := util.CalculateSecretChecksum(ctx, dr.kubernetesClient,
				hostedControlPlane.Namespace,
				extractSecretNames(template.Spec.Volumes),
			)
			if err != nil {
				return fmt.Errorf("failed to calculate secret checksum: %w", err)
			}

			configMapChecksum, err := util.CalculateConfigMapChecksum(ctx, dr.kubernetesClient,
				hostedControlPlane.Namespace,
				extractConfigMapNames(template.Spec.Volumes),
			)
			if err != nil {
				return fmt.Errorf("failed to calculate configmap checksum: %w", err)
			}

			deployment := appsacv1.Deployment(hostedControlPlane.Name, hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithSpec(appsacv1.DeploymentSpec().
					WithReplicas(*hostedControlPlane.Spec.Replicas).
					WithSelector(metav1ac.LabelSelector().
						WithMatchLabels(names.GetSelector(hostedControlPlane.Name)),
					).
					WithTemplate(template.WithAnnotations(map[string]string{
						"checksum/secrets":    secretChecksum,
						"checksum/configmaps": configMapChecksum,
					})),
				).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane))

			_, err = dr.kubernetesClient.AppsV1().Deployments(hostedControlPlane.Namespace).Apply(ctx,
				deployment,
				applyOptions,
			)

			return errorsUtil.IfErrErrorf("failed to patch deployment: %w", err)
		},
	)
}

func extractNames(
	volumes []corev1ac.VolumeApplyConfiguration,
	directAccess func(corev1ac.VolumeApplyConfiguration) string,
	projectedAccess func(configuration *corev1ac.VolumeProjectionApplyConfiguration) string,
) []string {
	return slices.Flatten(slices.Map(volumes, func(volume corev1ac.VolumeApplyConfiguration, _ int) []string {
		if value := directAccess(volume); value != "" {
			return []string{value}
		}
		if volume.Projected != nil && volume.Projected.Sources != nil {
			return slices.FilterMap(volume.Projected.Sources,
				func(source corev1ac.VolumeProjectionApplyConfiguration, _ int) (string, bool) {
					if value := projectedAccess(&source); value != "" {
						return value, true
					}
					return "", false
				},
			)
		}
		return nil
	}))
}

func extractSecretNames(volumes []corev1ac.VolumeApplyConfiguration) []string {
	return extractNames(volumes, func(volume corev1ac.VolumeApplyConfiguration) string {
		if volume.Secret != nil {
			return *volume.Secret.SecretName
		}
		return ""
	}, func(configuration *corev1ac.VolumeProjectionApplyConfiguration) string {
		if configuration.Secret != nil {
			return *configuration.Secret.Name
		}
		return ""
	})
}

func extractConfigMapNames(volumes []corev1ac.VolumeApplyConfiguration) []string {
	return extractNames(volumes, func(volume corev1ac.VolumeApplyConfiguration) string {
		if volume.ConfigMap != nil {
			return *volume.ConfigMap.Name
		}
		return ""
	}, func(configuration *corev1ac.VolumeProjectionApplyConfiguration) string {
		if configuration.ConfigMap != nil {
			return *configuration.ConfigMap.Name
		}
		return ""
	})
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
					WithPath(konstants.SchedulerKubeConfigFileName),
			),
		)
}

func (dr *DeploymentReconciler) createControllerManagerKubeconfigVolume(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("kube-controller-manager-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(hostedControlPlane.Name, konstants.KubeControllerManager)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithPath(konstants.ControllerManagerKubeConfigFileName),
			),
		)
}

func (dr *DeploymentReconciler) createKonnectivityConfigVolume(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-config").
		WithConfigMap(corev1ac.ConfigMapVolumeSource().
			WithName(names.GetKonnectivityConfigMapName(hostedControlPlane.Name)),
		)
}

func (dr *DeploymentReconciler) createAPIServerCertificatesVolume(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("api-server-certificates").
		WithProjected(corev1ac.ProjectedVolumeSource().
			WithSources(
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetCASecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.CACertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.CAKeyName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetFrontProxyCASecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.FrontProxyCACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetFrontProxySecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.FrontProxyClientCertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.FrontProxyClientKeyName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetServiceAccountSecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.ServiceAccountPublicKeyName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.ServiceAccountPrivateKeyName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetAPIServerSecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.APIServerCertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
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
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetEtcdCASecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.EtcdCACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetEtcdAPIServerClientSecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.APIServerEtcdClientCertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.APIServerEtcdClientKeyName),
					),
				),
			),
		)
}

func (dr *DeploymentReconciler) createControllerManagerCertificatesVolume(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("controller-manager-certificates").
		WithProjected(corev1ac.ProjectedVolumeSource().
			WithSources(
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetCASecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.CACertName),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.CAKeyName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetFrontProxyCASecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.FrontProxyCACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetServiceAccountSecretName(hostedControlPlane.Name)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.ServiceAccountPrivateKeyName),
					),
				),
			),
		)
}

func (dr *DeploymentReconciler) buildAPIServerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	apiServerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	certificatesDir := *apiServerCertificatesVolumeMount.MountPath
	egressSelectorConfigDir := *egressSelectorConfigVolumeMount.MountPath
	nodeAdressTypes := slices.Map([]corev1.NodeAddressType{
		corev1.NodeInternalDNS,
		corev1.NodeExternalDNS,
		corev1.NodeHostName,
	},
		func(item corev1.NodeAddressType, _ int) string {
			return string(item)
		})

	args := map[string]string{
		"egress-selector-config-file": path.Join(
			egressSelectorConfigDir,
			EgressSelectorConfigurationFileName,
		),
		"allow-privileged":                   "true",
		"authorization-mode":                 "Node,RBAC",
		"client-ca-file":                     path.Join(certificatesDir, konstants.CACertName),
		"enable-bootstrap-token-auth":        "true",
		"kubelet-client-certificate":         path.Join(certificatesDir, konstants.APIServerKubeletClientCertName),
		"kubelet-client-key":                 path.Join(certificatesDir, konstants.APIServerKubeletClientKeyName),
		"kubelet-preferred-address-types":    strings.Join(nodeAdressTypes, ","),
		"proxy-client-cert-file":             path.Join(certificatesDir, konstants.FrontProxyClientCertName),
		"proxy-client-key-file":              path.Join(certificatesDir, konstants.FrontProxyClientKeyName),
		"requestheader-allowed-names":        konstants.FrontProxyClientCertCommonName,
		"requestheader-client-ca-file":       path.Join(certificatesDir, konstants.FrontProxyCACertName),
		"requestheader-extra-headers-prefix": "X-Remote-Extra-",
		"requestheader-group-headers":        "X-Remote-Group",
		"requestheader-username-headers":     "X-Remote-User",
		"secure-port":                        "6443",
		"service-account-issuer":             "https://kubernetes.default.svc.cluster.local",
		"service-account-key-file":           path.Join(certificatesDir, konstants.ServiceAccountPublicKeyName),
		"service-account-signing-key-file":   path.Join(certificatesDir, konstants.ServiceAccountPrivateKeyName),
		"service-cluster-ip-range":           "10.96.0.0/12",
		"tls-cert-file":                      path.Join(certificatesDir, konstants.APIServerCertName),
		"tls-private-key-file":               path.Join(certificatesDir, konstants.APIServerKeyName),
		"etcd-servers":                       fmt.Sprintf("https://e-%s-client:2379", hostedControlPlane.Name),
		"etcd-cafile":                        path.Join(certificatesDir, konstants.EtcdCACertName),
		"etcd-certfile":                      path.Join(certificatesDir, konstants.APIServerEtcdClientCertName),
		"etcd-keyfile":                       path.Join(certificatesDir, konstants.APIServerEtcdClientKeyName),
	}

	return dr.argsToSlice(hostedControlPlane.Spec.Deployment.APIServer.Args, args)
}

func (dr *DeploymentReconciler) argsToSlice(args ...map[string]string) []string {
	argsSlice := slices.MapToSlice(slices.Assign(args...), func(key string, value string) string {
		return fmt.Sprintf("--%s=%s", key, value)
	})
	sort.Strings(argsSlice)
	return argsSlice
}

func (dr *DeploymentReconciler) createAPIServerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	apiServerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	apiPort := corev1ac.ContainerPort().
		WithName(APIServerPortName).
		WithContainerPort(konstants.KubeAPIServerPort).
		WithProtocol(corev1.ProtocolTCP)

	return corev1ac.Container().
		WithName(konstants.KubeAPIServer).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-apiserver:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("kube-apiserver").
		WithArgs(dr.buildAPIServerArgs(
			hostedControlPlane,
			apiServerCertificatesVolumeMount,
			egressSelectorConfigVolumeMount,
		)...).
		WithPorts(apiPort).
		WithStartupProbe(dr.createStartupProbe(apiPort)).
		WithReadinessProbe(dr.createReadinessProbe(apiPort)).
		WithLivenessProbe(dr.createLivenessProbe(apiPort)).
		WithVolumeMounts(
			apiServerCertificatesVolumeMount,
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

func (dr *DeploymentReconciler) createProbe(
	path string,
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return corev1ac.Probe().WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath(path).
		WithPort(intstr.FromString(*probePort.Name)).
		WithScheme(corev1.URISchemeHTTPS),
	)
}

func (dr *DeploymentReconciler) createProbePort(
	prefix string,
	port int,
) *corev1ac.ContainerPortApplyConfiguration {
	containerPort := corev1ac.ContainerPort().
		WithName(fmt.Sprintf("%s-probe-port", prefix)). // TODO: use konstants.probePort when available
		WithContainerPort(int32(port)).                 //nolint:gosec // port is expected to be within int32 range
		WithProtocol(corev1.ProtocolTCP)
	return containerPort
}

func (dr *DeploymentReconciler) createSchedulerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	schedulerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*schedulerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	probePort := dr.createProbePort("s", konstants.KubeSchedulerPort)
	return corev1ac.Container().
		WithName(konstants.KubeScheduler).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-scheduler:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("kube-scheduler").
		WithArgs(dr.buildSchedulerArgs(hostedControlPlane, schedulerKubeconfigVolumeMount)...).
		WithPorts(probePort).
		WithStartupProbe(dr.createStartupProbe(probePort)).
		WithReadinessProbe(dr.createReadinessProbe(probePort)).
		WithLivenessProbe(dr.createLivenessProbe(probePort)).
		WithVolumeMounts(schedulerKubeconfigVolumeMount)
}

func (dr *DeploymentReconciler) createControllerManagerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	controllerManagerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	controllerManagerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	controllerManagerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*controllerManagerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	probePort := dr.createProbePort("c", konstants.KubeControllerManagerPort)
	return corev1ac.Container().
		WithName(konstants.KubeControllerManager).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-controller-manager:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("kube-controller-manager").
		WithArgs(dr.buildControllerManagerArgs(
			hostedControlPlane,
			controllerManagerCertificatesVolumeMount,
			controllerManagerKubeconfigVolumeMount,
		)...).
		WithPorts(probePort).
		WithStartupProbe(dr.createStartupProbe(probePort)).
		WithReadinessProbe(dr.createReadinessProbe(probePort)).
		WithLivenessProbe(dr.createLivenessProbe(probePort)).
		WithVolumeMounts(controllerManagerCertificatesVolumeMount, controllerManagerKubeconfigVolumeMount)
}

func (dr *DeploymentReconciler) buildSchedulerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	kubeconfigPath := path.Join(*schedulerKubeconfigVolumeMount.MountPath, konstants.SchedulerKubeConfigFileName)

	// use map[string]any as soon as https://github.com/kubernetes-sigs/controller-tools/issues/636 is resolved
	args := map[string]string{
		"authentication-kubeconfig": kubeconfigPath,
		"authorization-kubeconfig":  kubeconfigPath,
		"kubeconfig":                kubeconfigPath,
		"bind-address":              "0.0.0.0",
		"leader-elect":              "true",
	}

	return dr.argsToSlice(hostedControlPlane.Spec.Deployment.Scheduler.Args, args)
}

func (dr *DeploymentReconciler) buildControllerManagerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	controllerManagerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	controllerManagerKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	kubeconfigPath := path.Join(
		*controllerManagerKubeconfigVolumeMount.MountPath,
		konstants.ControllerManagerKubeConfigFileName,
	)

	// use map[string]any as soon as https://github.com/kubernetes-sigs/controller-tools/issues/636 is resolved
	certificatesDir := *controllerManagerCertificatesVolumeMount.MountPath
	args := map[string]string{
		"allocate-node-cidrs":              "true",
		"authentication-kubeconfig":        kubeconfigPath,
		"authorization-kubeconfig":         kubeconfigPath,
		"kubeconfig":                       kubeconfigPath,
		"bind-address":                     "0.0.0.0",
		"leader-elect":                     "true",
		"cluster-name":                     hostedControlPlane.Name,
		"client-ca-file":                   path.Join(certificatesDir, konstants.CACertName),
		"cluster-signing-cert-file":        path.Join(certificatesDir, konstants.CACertName),
		"cluster-signing-key-file":         path.Join(certificatesDir, konstants.CAKeyName),
		"controllers":                      "*,bootstrapsigner,tokencleaner",
		"service-cluster-ip-range":         "10.96.0.0/16",
		"cluster-cidr":                     "10.244.0.0/16",
		"requestheader-client-ca-file":     path.Join(certificatesDir, konstants.FrontProxyCACertName),
		"root-ca-file":                     path.Join(certificatesDir, konstants.CACertName),
		"service-account-private-key-file": path.Join(certificatesDir, konstants.ServiceAccountPrivateKeyName),
		"use-service-account-credentials":  "true",
	}

	return dr.argsToSlice(hostedControlPlane.Spec.Deployment.ControllerManager.Args, args)
}
