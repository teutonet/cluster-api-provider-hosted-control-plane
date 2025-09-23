package apiserverresources

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	kubenames "k8s.io/kubernetes/cmd/kube-controller-manager/names"
	kubeadmv1beta4 "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta4"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
	"sigs.k8s.io/yaml"
)

var errWebhookSecretIsMissingKey = errors.New("webhook authentication secret is missing key")

type ApiServerResourcesReconciler interface {
	ReconcileApiServerService(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
	ReconcileApiServerDeployments(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewApiServerResourcesReconciler(
	kubernetesClient kubernetes.Interface,
	worldComponent string,
	serviceCIDR string,
	apiServerComponentLabel string,
	apiServerServicePort int32,
	apiServerServiceLegacyPortName string,
	etcdComponentLabel string,
	etcdServerPort int32,
	konnectivityNamespace string,
	konnectivityServiceAccount string,
	konnectivityServicePort int32,
	konnectivityClientKubeconfigName string,
	konnectivityServerAudience string,
) ApiServerResourcesReconciler {
	return &apiServerResourcesReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			Tracer:           tracing.GetTracer("apiServerResources"),
			KubernetesClient: kubernetesClient,
			WorldComponent:   worldComponent,
		},
		worldComponent:                      worldComponent,
		serviceCIDR:                         serviceCIDR,
		apiServerServicePort:                apiServerServicePort,
		apiServerServiceLegacyPortName:      apiServerServiceLegacyPortName,
		etcdComponentLabel:                  etcdComponentLabel,
		etcdServerPort:                      etcdServerPort,
		konnectivityNamespace:               konnectivityNamespace,
		konnectivityServiceAccount:          konnectivityServiceAccount,
		konnectivityServicePort:             konnectivityServicePort,
		konnectivityClientKubeconfigName:    konnectivityClientKubeconfigName,
		konnectivityServerAudience:          konnectivityServerAudience,
		egressSelectorConfigMountPath:       "/etc/kubernetes/egress/configurations",
		konnectivityUDSMountPath:            "/run/konnectivity",
		konnectivityUDSSocketName:           "konnectivity-agent.sock",
		egressSelectorConfigurationFileName: "egress-selector-configuration.yaml",
		envoyConfigFileName:                 "envoy.yaml",
		envoyPort:                           8090,
		auditPolicyFileName:                 "audit-policy.yaml",
		auditWebhookConfigFileName:          "webhook-target.conf",
		componentAPIServer:                  apiServerComponentLabel,
		componentControllerManager:          "controller-manager",
		componentScheduler:                  "scheduler",
		apiContainerPortName:                intstr.FromString("api"),
		apiContainerPort:                    konstants.KubeAPIServerPort,
		konnectivityContainerPortName:       intstr.FromString("konnectivity"),
		konnectivityKubeconfigFileName:      "konnectivity-server.conf",
	}
}

type apiServerResourcesReconciler struct {
	reconcilers.ManagementResourceReconciler
	worldComponent                      string
	serviceCIDR                         string
	apiServerServicePort                int32
	apiServerServiceLegacyPortName      string
	etcdComponentLabel                  string
	etcdServerPort                      int32
	konnectivityNamespace               string
	konnectivityServiceAccount          string
	konnectivityServicePort             int32
	konnectivityClientKubeconfigName    string
	konnectivityServerAudience          string
	konnectivityUDSMountPath            string
	konnectivityUDSSocketName           string
	egressSelectorConfigMountPath       string
	egressSelectorConfigurationFileName string
	envoyConfigFileName                 string
	envoyPort                           int32
	auditPolicyFileName                 string
	auditWebhookConfigFileName          string
	componentAPIServer                  string
	componentControllerManager          string
	componentScheduler                  string
	apiContainerPortName                intstr.IntOrString
	apiContainerPort                    int32
	konnectivityContainerPortName       intstr.IntOrString
	konnectivityKubeconfigFileName      string
}

var _ ApiServerResourcesReconciler = &apiServerResourcesReconciler{}

func (arr *apiServerResourcesReconciler) ReconcileApiServerDeployments(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, arr.Tracer, "ReconcileApiServerDeployments",
		func(ctx context.Context, span trace.Span) (string, error) {
			if err := arr.reconcileKonnectivityConfig(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to reconcile konnectivity config: %w", err)
			}
			if hostedControlPlane.Spec.Deployment.APIServer.Audit != nil {
				if err := arr.reconcileAuditConfig(ctx, hostedControlPlane, cluster); err != nil {
					return "", fmt.Errorf("failed to reconcile audit config: %w", err)
				}
			}
			if ready, err := arr.reconcileAPIServerDeployment(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to reconcile API server deployment: %w", err)
			} else if !ready {
				return "Api Server Deployment not ready", nil
			}

			var notReadyReasons []string
			if ready, err := arr.reconcileControllerManagerDeployment(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to reconcile controller manager deployment: %w", err)
			} else if !ready {
				notReadyReasons = append(notReadyReasons, "Controller Manager Deployment not ready")
			}

			if ready, err := arr.reconcileSchedulerDeployment(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
			} else if !ready {
				notReadyReasons = append(notReadyReasons, "Scheduler Deployment not ready")
			}

			if len(notReadyReasons) > 0 {
				return strings.Join(notReadyReasons, ","), nil
			}

			hostedControlPlane.Status.Version = hostedControlPlane.Spec.Version

			return "", nil
		},
	)
}

func (arr *apiServerResourcesReconciler) reconcileKonnectivityConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.Tracer, "ReconcileKonnectivityConfig",
		func(ctx context.Context, span trace.Span) error {
			egressSelectorConfig := &v1beta1.EgressSelectorConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.k8s.io/v1beta1",
					Kind:       "EgressSelectorConfiguration",
				},
				EgressSelections: []v1beta1.EgressSelection{
					{
						Name: "cluster",
						Connection: v1beta1.Connection{
							ProxyProtocol: v1beta1.ProtocolGRPC,
							Transport: &v1beta1.Transport{
								UDS: &v1beta1.UDSTransport{
									UDSName: path.Join(
										arr.konnectivityUDSMountPath,
										arr.konnectivityUDSSocketName,
									),
								},
							},
						},
					},
				},
			}

			egressYaml, err := operatorutil.ToYaml(egressSelectorConfig)
			if err != nil {
				return fmt.Errorf("failed to marshal egress selector configuration: %w", err)
			}

			return arr.ReconcileConfigmap(
				ctx,
				hostedControlPlane,
				cluster,
				"konnectivity",
				hostedControlPlane.Namespace,
				names.GetKonnectivityConfigMapName(cluster),
				map[string]string{
					arr.egressSelectorConfigurationFileName: egressYaml,
				},
			)
		},
	)
}

func (arr *apiServerResourcesReconciler) reconcileAuditConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.Tracer, "ReconcileAuditWebhookConfig",
		func(ctx context.Context, span trace.Span) error {
			data := make(map[string]string)

			auditConfig := hostedControlPlane.Spec.Deployment.APIServer.Audit
			if auditConfig.Webhook != nil {
				if webhookKubeconfigString, envoyConfigString, err := arr.generateWebhookConfigs(
					ctx,
					hostedControlPlane,
				); err != nil {
					return err
				} else {
					data[arr.auditWebhookConfigFileName] = webhookKubeconfigString
					if envoyConfigString != "" {
						data[arr.envoyConfigFileName] = envoyConfigString
					}
				}
			}

			if policyYaml, err := operatorutil.PolicyToYaml(auditConfig.Policy); err != nil {
				return fmt.Errorf("failed to marshal audit policy: %w", err)
			} else {
				data[arr.auditPolicyFileName] = policyYaml
			}

			return arr.ReconcileSecret(
				ctx,
				hostedControlPlane,
				cluster,
				"audit",
				hostedControlPlane.Namespace,
				names.GetAuditWebhookSecretName(cluster),
				slices.MapValues(data, func(value string, _ string) []byte {
					return []byte(value)
				}),
			)
		})
}

func (arr *apiServerResourcesReconciler) reconcileAPIServerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (bool, error) {
	return tracing.WithSpan(ctx, arr.Tracer, "ReconcileAPIServerDeployment",
		func(ctx context.Context, span trace.Span) (bool, error) {
			apiServerCertificatesVolume := arr.createAPIServerCertificatesVolume(cluster)
			egressSelectorConfigVolume := arr.createEgressSelectorConfigVolume(cluster)
			konnectivityUDSVolume := arr.createKonnectivityUDSVolume()
			konnectivityCertificatesVolume := arr.createKonnectivityCertificatesVolume(cluster)
			konnectivityKubeconfigVolume := arr.createKonnectivityKubeconfigVolume(cluster)
			var auditConfigVolume *corev1ac.VolumeApplyConfiguration

			konnectivityUDSMount := corev1ac.VolumeMount().
				WithName(*konnectivityUDSVolume.Name).
				WithMountPath(arr.konnectivityUDSMountPath)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				apiServerCertificatesVolume,
				egressSelectorConfigVolume,
				konnectivityUDSVolume,
				konnectivityCertificatesVolume,
				konnectivityKubeconfigVolume,
			}

			auditConfig := hostedControlPlane.Spec.Deployment.APIServer.Audit
			if auditConfig != nil {
				auditConfigVolume = arr.createAuditConfigVolume(cluster)
				volumes = append(volumes, auditConfigVolume)
			}

			additionalVolumes, additionalApiServerVolumeMounts := arr.extractAdditionalVolumesAndMounts(
				hostedControlPlane.Spec.Deployment.APIServer.Mounts,
			)

			volumes = append(volumes, additionalVolumes...)

			apiServerContainer := arr.createAPIServerContainer(
				ctx,
				hostedControlPlane,
				cluster,
				apiServerCertificatesVolume,
				egressSelectorConfigVolume,
				auditConfigVolume,
				konnectivityUDSMount,
				additionalApiServerVolumeMounts,
			)
			konnectivityContainer, err := arr.createKonnectivityContainer(
				ctx,
				hostedControlPlane,
				konnectivityCertificatesVolume,
				konnectivityKubeconfigVolume,
				konnectivityUDSMount,
			)
			if err != nil {
				return false, fmt.Errorf("failed to create konnectivity container: %w", err)
			}

			var initContainers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]
			if auditConfig != nil && auditConfig.Webhook != nil && len(auditConfig.Webhook.Targets) > 1 {
				initContainers = append(
					initContainers,
					slices.T2(
						arr.createAuditWebhookSidecarContainer(ctx, auditConfig, auditConfigVolume),
						reconcilers.ContainerOptions{},
					),
				)
			}

			egressPortComponents := map[int32][]string{
				arr.etcdServerPort: {arr.etcdComponentLabel},
				443:                {arr.worldComponent}, // for stuff like OIDC
			}

			if auditConfig != nil && auditConfig.Webhook != nil {
				if err := arr.setAuditWebhookPorts(auditConfig, egressPortComponents); err != nil {
					return false, err
				}
			}

			if deployment, ready, err := arr.ReconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				*hostedControlPlane.Spec.Replicas,
				hostedControlPlane.Spec.Deployment.APIServer.PriorityClassName,
				arr.componentAPIServer,
				nil,
				egressPortComponents,
				arr.etcdComponentLabel,
				initContainers,
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(apiServerContainer, reconcilers.ContainerOptions{
						Capabilities: []corev1.Capability{"NET_BIND_SERVICE"},
					}),
					slices.T2(konnectivityContainer, reconcilers.ContainerOptions{}),
				},
				volumes,
			); err != nil {
				return false, err
			} else if !ready {
				return false, nil
			} else {
				selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
				if err != nil {
					return false, fmt.Errorf("failed to convert label selector: %w", err)
				}
				hostedControlPlane.Status.Selector = selector.String()
				hostedControlPlane.Status.Replicas = deployment.Status.Replicas
				hostedControlPlane.Status.UnavailableReplicas = deployment.Status.UnavailableReplicas
				hostedControlPlane.Status.ReadyReplicas = deployment.Status.ReadyReplicas
				hostedControlPlane.Status.UpdatedReplicas = deployment.Status.UpdatedReplicas
				return ready, nil
			}
		},
	)
}

func (arr *apiServerResourcesReconciler) extractAdditionalVolumesAndMounts(
	mounts map[string]v1alpha1.Mount,
) ([]*corev1ac.VolumeApplyConfiguration, []*corev1ac.VolumeMountApplyConfiguration) {
	entries := slices.MapEntries(mounts, func(
		name string, mount v1alpha1.Mount,
	) (*corev1ac.VolumeApplyConfiguration, *corev1ac.VolumeMountApplyConfiguration) {
		convertItems := func(items []corev1.KeyToPath) []*corev1ac.KeyToPathApplyConfiguration {
			return slices.Map(items, func(item corev1.KeyToPath, _ int) *corev1ac.KeyToPathApplyConfiguration {
				return corev1ac.KeyToPath().
					WithKey(item.Key).
					WithPath(item.Path)
			})
		}
		volume := corev1ac.Volume().
			WithName(name)
		if mount.Secret != nil {
			items := mount.Secret.Items
			volume = volume.WithSecret(corev1ac.SecretVolumeSource().
				WithSecretName(mount.Secret.SecretName).
				WithOptional(false).
				WithItems(convertItems(items)...),
			)
		}
		if mount.ConfigMap != nil {
			volume = volume.WithConfigMap(corev1ac.ConfigMapVolumeSource().
				WithName(mount.ConfigMap.Name).
				WithOptional(false).
				WithItems(convertItems(mount.ConfigMap.Items)...),
			)
		}

		return volume, corev1ac.VolumeMount().
			WithName(name).
			WithMountPath(mount.Path).
			WithReadOnly(true)
	})
	return slices.Keys(entries), slices.Values(entries)
}

func (arr *apiServerResourcesReconciler) reconcileControllerManagerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (bool, error) {
	return tracing.WithSpan(ctx, arr.Tracer, "ReconcileControllerManagerDeployment",
		func(ctx context.Context, span trace.Span) (bool, error) {
			controllerManagerCertificatesVolume := arr.createControllerManagerCertificatesVolume(cluster)
			controllerManagerKubeconfigVolume := arr.createControllerManagerKubeconfigVolume(cluster)

			controllerManagerCertificatesVolumeMount := corev1ac.VolumeMount().
				WithName(*controllerManagerCertificatesVolume.Name).
				WithMountPath(kubeadmv1beta4.DefaultCertificatesDir).
				WithReadOnly(true)

			container := arr.createControllerManagerContainer(
				ctx,
				hostedControlPlane,
				cluster,
				controllerManagerCertificatesVolumeMount,
				controllerManagerKubeconfigVolume,
			)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				controllerManagerCertificatesVolume,
				controllerManagerKubeconfigVolume,
			}

			_, ready, err := arr.ReconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				*hostedControlPlane.Spec.Deployment.ControllerManager.Replicas,
				hostedControlPlane.Spec.Deployment.ControllerManager.PriorityClassName,
				arr.componentControllerManager,
				map[int32][]string{},
				map[int32][]string{
					arr.apiContainerPort: {arr.componentAPIServer},
				},
				arr.componentAPIServer,
				nil,
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{}),
				},
				volumes,
			)
			return ready, err
		},
	)
}

func (arr *apiServerResourcesReconciler) reconcileSchedulerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (bool, error) {
	return tracing.WithSpan(ctx, arr.Tracer, "ReconcileSchedulerDeployment",
		func(ctx context.Context, span trace.Span) (bool, error) {
			schedulerKubeconfigVolume := arr.createSchedulerKubeconfigVolume(cluster)

			container := arr.createSchedulerContainer(
				ctx,
				hostedControlPlane,
				schedulerKubeconfigVolume,
			)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				schedulerKubeconfigVolume,
			}

			_, ready, err := arr.ReconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				*hostedControlPlane.Spec.Deployment.Scheduler.Replicas,
				hostedControlPlane.Spec.Deployment.Scheduler.PriorityClassName,
				arr.componentScheduler,
				map[int32][]string{},
				map[int32][]string{
					arr.apiContainerPort: {arr.componentAPIServer},
				},
				arr.componentAPIServer,
				nil,
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{}),
				},
				volumes,
			)
			return ready, err
		},
	)
}

//+kubebuilder:rbac:groups=core,resources=services,verbs=create;update;patch

func (arr *apiServerResourcesReconciler) ReconcileApiServerService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, arr.Tracer, "ReconcileApiServerService",
		func(ctx context.Context, span trace.Span) (string, error) {
			apiPort := corev1ac.ServicePort().
				WithName("api").
				WithPort(arr.apiServerServicePort).
				WithTargetPort(arr.apiContainerPortName).
				WithProtocol(corev1.ProtocolTCP)
			legacyAPIPort := corev1ac.ServicePort().
				WithName(arr.apiServerServiceLegacyPortName).
				WithPort(6443).
				WithTargetPort(*apiPort.TargetPort).
				WithProtocol(*apiPort.Protocol)
			if service, ready, err := arr.ReconcileService(
				ctx,
				hostedControlPlane,
				cluster,
				hostedControlPlane.Namespace,
				names.GetServiceName(cluster),
				corev1.ServiceTypeLoadBalancer,
				false,
				arr.componentAPIServer,
				[]*corev1ac.ServicePortApplyConfiguration{
					apiPort,
					legacyAPIPort,
					corev1ac.ServicePort().
						WithName("konnectivity").
						WithPort(arr.konnectivityServicePort).
						WithTargetPort(arr.konnectivityContainerPortName).
						WithProtocol(corev1.ProtocolTCP),
				},
			); err != nil {
				return "", err
			} else if !ready {
				return "Api Server Service is waiting on its IP", nil
			} else {
				hostedControlPlane.Status.LegacyIP = service.Status.LoadBalancer.Ingress[0].IP
			}

			return "", nil
		},
	)
}

func (arr *apiServerResourcesReconciler) createSchedulerKubeconfigVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("kube-scheduler-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(cluster, konstants.KubeScheduler)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithPath(konstants.SchedulerKubeConfigFileName),
			),
		)
}

func (arr *apiServerResourcesReconciler) createControllerManagerKubeconfigVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("kube-controller-manager-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(cluster, konstants.KubeControllerManager)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithPath(konstants.ControllerManagerKubeConfigFileName),
			),
		)
}

func (arr *apiServerResourcesReconciler) createKonnectivityKubeconfigVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(cluster, arr.konnectivityClientKubeconfigName)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithPath(arr.konnectivityKubeconfigFileName),
			),
		)
}

func (arr *apiServerResourcesReconciler) createKonnectivityUDSVolume() *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-uds").
		WithEmptyDir(corev1ac.EmptyDirVolumeSource().WithMedium(corev1.StorageMediumMemory))
}

func (arr *apiServerResourcesReconciler) createAuditConfigVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("audit-webhook-config").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetAuditWebhookSecretName(cluster)),
		)
}

func (arr *apiServerResourcesReconciler) createEgressSelectorConfigVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-config").
		WithConfigMap(corev1ac.ConfigMapVolumeSource().
			WithName(names.GetKonnectivityConfigMapName(cluster)),
		)
}

func (arr *apiServerResourcesReconciler) createAPIServerCertificatesVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("api-server-certificates").
		WithProjected(corev1ac.ProjectedVolumeSource().
			WithSources(
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetCASecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.CACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetFrontProxyCASecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.FrontProxyCACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetFrontProxySecretName(cluster)).
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
					WithName(names.GetServiceAccountSecretName(cluster)).
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
					WithName(names.GetAPIServerSecretName(cluster)).
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
					WithName(names.GetAPIServerKubeletClientSecretName(cluster)).
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
					WithName(names.GetEtcdCASecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.EtcdCACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetEtcdAPIServerClientCertificateSecretName(cluster)).
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

func (arr *apiServerResourcesReconciler) createKonnectivityCertificatesVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-certificates").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetAPIServerSecretName(cluster)),
		)
}

func (arr *apiServerResourcesReconciler) createControllerManagerCertificatesVolume(
	cluster *capiv2.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("controller-manager-certificates").
		WithProjected(corev1ac.ProjectedVolumeSource().
			WithSources(
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetCASecretName(cluster)).
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
					WithName(names.GetFrontProxyCASecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.FrontProxyCACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetServiceAccountSecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath(konstants.ServiceAccountPrivateKeyName),
					),
				),
			),
		)
}

func (arr *apiServerResourcesReconciler) buildAPIServerArgs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	apiServerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	auditConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	apiPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	certificatesDir := *apiServerCertificatesVolumeMount.MountPath
	egressSelectorConfigDir := *egressSelectorConfigVolumeMount.MountPath
	nodeAddressTypes := slices.Map([]corev1.NodeAddressType{
		corev1.NodeInternalIP,
		corev1.NodeInternalDNS,
		corev1.NodeHostName,
	},
		func(item corev1.NodeAddressType, _ int) string {
			return string(item)
		})

	args := map[string]string{
		"advertise-address":           hostedControlPlane.Status.LegacyIP,
		"allow-privileged":            "true",
		"authorization-mode":          konstants.ModeNode + "," + konstants.ModeRBAC,
		"client-ca-file":              path.Join(certificatesDir, konstants.CACertName),
		"enable-bootstrap-token-auth": "true",
		"egress-selector-config-file": path.Join(
			egressSelectorConfigDir,
			arr.egressSelectorConfigurationFileName,
		),
		"kubelet-client-certificate":         path.Join(certificatesDir, konstants.APIServerKubeletClientCertName),
		"kubelet-client-key":                 path.Join(certificatesDir, konstants.APIServerKubeletClientKeyName),
		"kubelet-preferred-address-types":    strings.Join(nodeAddressTypes, ","),
		"proxy-client-cert-file":             path.Join(certificatesDir, konstants.FrontProxyClientCertName),
		"proxy-client-key-file":              path.Join(certificatesDir, konstants.FrontProxyClientKeyName),
		"requestheader-allowed-names":        konstants.FrontProxyClientCertCommonName,
		"requestheader-client-ca-file":       path.Join(certificatesDir, konstants.FrontProxyCACertName),
		"requestheader-extra-headers-prefix": "X-Remote-Extra-",
		"requestheader-group-headers":        "X-Remote-Group",
		"requestheader-username-headers":     "X-Remote-User",
		"secure-port":                        strconv.Itoa(int(*apiPort.ContainerPort)),
		"service-account-issuer":             "https://kubernetes.default.svc",
		"service-account-key-file":           path.Join(certificatesDir, konstants.ServiceAccountPublicKeyName),
		"service-account-signing-key-file":   path.Join(certificatesDir, konstants.ServiceAccountPrivateKeyName),
		"service-cluster-ip-range":           arr.serviceCIDR,
		"tls-cert-file":                      path.Join(certificatesDir, konstants.APIServerCertName),
		"tls-private-key-file":               path.Join(certificatesDir, konstants.APIServerKeyName),
		"etcd-servers": fmt.Sprintf("https://%s",
			net.JoinHostPort(names.GetEtcdClientServiceDNSName(cluster), strconv.Itoa(int(arr.etcdServerPort))),
		),
		"etcd-cafile":   path.Join(certificatesDir, konstants.EtcdCACertName),
		"etcd-certfile": path.Join(certificatesDir, konstants.APIServerEtcdClientCertName),
		"etcd-keyfile":  path.Join(certificatesDir, konstants.APIServerEtcdClientKeyName),
	}

	if auditConfig := hostedControlPlane.Spec.Deployment.APIServer.Audit; auditConfig != nil {
		args["audit-policy-file"] = path.Join(
			*auditConfigVolumeMount.MountPath,
			arr.auditPolicyFileName,
		)
		if auditConfig.Webhook != nil {
			args["audit-webhook-mode"] = auditConfig.Mode
			args["audit-webhok-config-file"] = path.Join(
				*auditConfigVolumeMount.MountPath,
				arr.auditWebhookConfigFileName,
			)
		} else {
			args["audit-log-mode"] = auditConfig.Mode
			args["audit-log-path"] = "-"
		}
	}

	return operatorutil.ArgsToSlice(
		ctx,
		hostedControlPlane.Spec.Deployment.APIServer.Args,
		args,
	)
}

func (arr *apiServerResourcesReconciler) createKonnectivityContainer(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	konnectivityCertificatesVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) (*corev1ac.ContainerApplyConfiguration, error) {
	konnectivityPort := corev1ac.ContainerPort().
		WithName(arr.konnectivityContainerPortName.String()).
		WithContainerPort(8132).
		WithProtocol(corev1.ProtocolTCP)
	adminPort := corev1ac.ContainerPort().
		WithName("k-admin").
		WithContainerPort(8133).
		WithProtocol(corev1.ProtocolTCP)
	healthPort := corev1ac.ContainerPort().
		WithName("k-health").
		WithContainerPort(8134).
		WithProtocol(corev1.ProtocolTCP)

	konnectivityCertificatesVolumeMount := corev1ac.VolumeMount().
		WithName(*konnectivityCertificatesVolume.Name).
		WithMountPath(kubeadmv1beta4.DefaultCertificatesDir).
		WithReadOnly(true)
	konnectivityKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*konnectivityKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir).
		WithReadOnly(true)

	minorVersion, err := operatorutil.GetMinorVersion(hostedControlPlane)
	if err != nil {
		return nil, fmt.Errorf("failed to get minor version of hosted control plane: %w", err)
	}

	return corev1ac.Container().
		WithName("konnectivity-server").
		WithImage(operatorutil.ResolveKonnectivityImage(
			hostedControlPlane.Spec.Deployment.APIServer.Konnectivity.Image,
			"proxy-server",
			minorVersion,
		)).
		WithImagePullPolicy(hostedControlPlane.Spec.Deployment.APIServer.Konnectivity.ImagePullPolicy).
		WithArgs(arr.buildKonnectivityServerArgs(
			ctx,
			hostedControlPlane,
			konnectivityCertificatesVolumeMount,
			konnectivityKubeconfigVolumeMount,
			konnectivityUDSVolumeMount,
			konnectivityPort, adminPort, healthPort,
		)...).
		WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
			hostedControlPlane.Spec.Deployment.APIServer.Konnectivity.Resources,
		)).
		WithPorts(konnectivityPort, adminPort, healthPort).
		WithStartupProbe(operatorutil.CreateStartupProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
		WithReadinessProbe(operatorutil.CreateReadinessProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
		WithLivenessProbe(operatorutil.CreateLivenessProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
		WithVolumeMounts(
			konnectivityCertificatesVolumeMount,
			konnectivityKubeconfigVolumeMount,
			konnectivityUDSVolumeMount,
		), nil
}

func (arr *apiServerResourcesReconciler) createAuditWebhookSidecarContainer(
	ctx context.Context,
	auditConfig *v1alpha1.Audit,
	webhookConfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	webhookConfigVolumeMount := corev1ac.VolumeMount().
		WithName(*webhookConfigVolume.Name).
		WithMountPath("/etc/kubernetes/audit")

	return corev1ac.Container().
		WithName("audit-webhook").
		WithImage(operatorutil.ResolveAuditWebhookImage(auditConfig.Webhook.Image)).
		WithImagePullPolicy(auditConfig.Webhook.ImagePullPolicy).
		WithArgs(operatorutil.ArgsToSlice(
			ctx,
			auditConfig.Webhook.Args,
			map[string]string{
				"config-path": path.Join(*webhookConfigVolumeMount.MountPath, arr.envoyConfigFileName),
			},
		)...).
		WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
			auditConfig.Webhook.Resources,
		)).
		WithRestartPolicy(corev1.ContainerRestartPolicyAlways).
		WithVolumeMounts(webhookConfigVolumeMount)
}

func (arr *apiServerResourcesReconciler) buildKonnectivityServerArgs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	konnectivityCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	konnectivityKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	konnectivityPort *corev1ac.ContainerPortApplyConfiguration,
	adminPort *corev1ac.ContainerPortApplyConfiguration,
	healthPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	args := map[string]string{
		"agent-namespace":         arr.konnectivityNamespace,
		"agent-service-account":   arr.konnectivityServiceAccount,
		"authentication-audience": arr.konnectivityServerAudience,
		"cluster-cert":            path.Join(*konnectivityCertificatesVolumeMount.MountPath, corev1.TLSCertKey),
		"cluster-key":             path.Join(*konnectivityCertificatesVolumeMount.MountPath, corev1.TLSPrivateKeyKey),
		"kubeconfig": path.Join(
			*konnectivityKubeconfigVolumeMount.MountPath,
			arr.konnectivityKubeconfigFileName,
		),
		"server-count": strconv.Itoa(int(*hostedControlPlane.Spec.Replicas)),
		"admin-port":   strconv.Itoa(int(*adminPort.ContainerPort)),
		"agent-port":   strconv.Itoa(int(*konnectivityPort.ContainerPort)),
		"health-port":  strconv.Itoa(int(*healthPort.ContainerPort)),
		"server-port":  "0",
		"uds-name":     path.Join(*konnectivityUDSVolumeMount.MountPath, arr.konnectivityUDSSocketName),
		"mode":         "grpc",
	}

	return operatorutil.ArgsToSlice(
		ctx,
		hostedControlPlane.Spec.Deployment.APIServer.Konnectivity.Args,
		args,
	)
}

func (arr *apiServerResourcesReconciler) createAPIServerContainer(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	apiServerCertificatesVolume *corev1ac.VolumeApplyConfiguration,
	egressSelectorConfigVolume *corev1ac.VolumeApplyConfiguration,
	auditConfigVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	additionalVolumeMounts []*corev1ac.VolumeMountApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	apiPort := corev1ac.ContainerPort().
		WithName(arr.apiContainerPortName.String()).
		WithContainerPort(arr.apiContainerPort).
		WithProtocol(corev1.ProtocolTCP)

	apiServerCertificatesVolumeMount := corev1ac.VolumeMount().
		WithName(*apiServerCertificatesVolume.Name).
		WithMountPath(kubeadmv1beta4.DefaultCertificatesDir).
		WithReadOnly(true)
	egressSelectorConfigVolumeMount := corev1ac.VolumeMount().
		WithName(*egressSelectorConfigVolume.Name).
		WithMountPath(arr.egressSelectorConfigMountPath).
		WithReadOnly(true)

	volumeMounts := []*corev1ac.VolumeMountApplyConfiguration{
		apiServerCertificatesVolumeMount,
		egressSelectorConfigVolumeMount,
		konnectivityUDSVolumeMount,
	}

	var auditConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration
	if auditConfigVolume != nil {
		auditConfigVolumeMount = corev1ac.VolumeMount().
			WithName(*auditConfigVolume.Name).
			WithMountPath("/etc/kubernetes/audit").
			WithReadOnly(true)
		volumeMounts = append(volumeMounts, auditConfigVolumeMount)
	}

	volumeMounts = append(volumeMounts, additionalVolumeMounts...)

	return corev1ac.Container().
		WithName(konstants.KubeAPIServer).
		WithImage(operatorutil.ResolveKubernetesComponentImage(
			hostedControlPlane.Spec.Deployment.APIServer.Image,
			"kube-apiserver",
			hostedControlPlane.Spec.Version,
		)).
		WithImagePullPolicy(hostedControlPlane.Spec.Deployment.APIServer.ImagePullPolicy).
		WithCommand("kube-apiserver").
		WithArgs(arr.buildAPIServerArgs(
			ctx, hostedControlPlane, cluster,
			apiServerCertificatesVolumeMount, egressSelectorConfigVolumeMount, auditConfigVolumeMount,
			apiPort,
		)...).
		WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
			hostedControlPlane.Spec.Deployment.APIServer.Resources,
		)).
		WithPorts(apiPort).
		WithStartupProbe(operatorutil.CreateStartupProbe(apiPort, "/readyz", corev1.URISchemeHTTPS)).
		WithReadinessProbe(operatorutil.CreateReadinessProbe(apiPort, "/readyz", corev1.URISchemeHTTPS)).
		WithLivenessProbe(operatorutil.CreateLivenessProbe(apiPort, "/livez", corev1.URISchemeHTTPS)).
		WithVolumeMounts(volumeMounts...)
}

func (arr *apiServerResourcesReconciler) createProbePort(
	port int32,
) *corev1ac.ContainerPortApplyConfiguration {
	containerPort := corev1ac.ContainerPort().
		WithName("probe-port"). // TODO: use konstants.probePort when available
		WithContainerPort(port).
		WithProtocol(corev1.ProtocolTCP)
	return containerPort
}

func (arr *apiServerResourcesReconciler) createSchedulerContainer(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	schedulerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*schedulerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	probePort := arr.createProbePort(konstants.KubeSchedulerPort)
	return corev1ac.Container().
		WithName(konstants.KubeScheduler).
		WithImage(operatorutil.ResolveKubernetesComponentImage(
			hostedControlPlane.Spec.Deployment.Scheduler.Image,
			"kube-scheduler",
			hostedControlPlane.Spec.Version,
		)).
		WithImagePullPolicy(hostedControlPlane.Spec.Deployment.Scheduler.ImagePullPolicy).
		WithCommand("kube-scheduler").
		WithArgs(arr.buildSchedulerArgs(ctx, hostedControlPlane, schedulerKubeconfigVolumeMount)...).
		WithPorts(probePort).
		WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
			hostedControlPlane.Spec.Deployment.Scheduler.Resources,
		)).
		WithStartupProbe(operatorutil.CreateStartupProbe(probePort, "/readyz", corev1.URISchemeHTTPS)).
		WithReadinessProbe(operatorutil.CreateReadinessProbe(probePort, "/readyz", corev1.URISchemeHTTPS)).
		WithLivenessProbe(operatorutil.CreateLivenessProbe(probePort, "/livez", corev1.URISchemeHTTPS)).
		WithVolumeMounts(schedulerKubeconfigVolumeMount)
}

func (arr *apiServerResourcesReconciler) createControllerManagerContainer(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	controllerManagerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	controllerManagerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	controllerManagerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*controllerManagerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	probePort := arr.createProbePort(konstants.KubeControllerManagerPort)
	return corev1ac.Container().
		WithName(konstants.KubeControllerManager).
		WithImage(operatorutil.ResolveKubernetesComponentImage(
			hostedControlPlane.Spec.Deployment.ControllerManager.Image,
			"kube-controller-manager",
			hostedControlPlane.Spec.Version,
		)).
		WithImagePullPolicy(hostedControlPlane.Spec.Deployment.ControllerManager.ImagePullPolicy).
		WithCommand("kube-controller-manager").
		WithArgs(arr.buildControllerManagerArgs(
			ctx,
			hostedControlPlane,
			cluster,
			controllerManagerCertificatesVolumeMount,
			controllerManagerKubeconfigVolumeMount,
		)...).
		WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
			hostedControlPlane.Spec.Deployment.ControllerManager.Resources,
		)).
		WithPorts(probePort).
		WithStartupProbe(operatorutil.CreateStartupProbe(probePort, "/healthz", corev1.URISchemeHTTPS)).
		WithReadinessProbe(operatorutil.CreateReadinessProbe(probePort, "/healthz", corev1.URISchemeHTTPS)).
		WithLivenessProbe(operatorutil.CreateLivenessProbe(probePort, "/healthz", corev1.URISchemeHTTPS)).
		WithVolumeMounts(controllerManagerCertificatesVolumeMount, controllerManagerKubeconfigVolumeMount)
}

func (arr *apiServerResourcesReconciler) buildSchedulerArgs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	kubeconfigPath := path.Join(*schedulerKubeconfigVolumeMount.MountPath, konstants.SchedulerKubeConfigFileName)

	leaderElect := *hostedControlPlane.Spec.Deployment.Scheduler.Replicas > 1

	args := map[string]string{
		"authentication-kubeconfig": kubeconfigPath,
		"authorization-kubeconfig":  kubeconfigPath,
		"kubeconfig":                kubeconfigPath,
		"bind-address":              "0.0.0.0",
		"leader-elect":              strconv.FormatBool(leaderElect),
	}

	return operatorutil.ArgsToSlice(
		ctx,
		hostedControlPlane.Spec.Deployment.Scheduler.Args,
		args,
	)
}

func (arr *apiServerResourcesReconciler) buildControllerManagerArgs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	controllerManagerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	controllerManagerKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	kubeconfigPath := path.Join(
		*controllerManagerKubeconfigVolumeMount.MountPath,
		konstants.ControllerManagerKubeConfigFileName,
	)

	// TODO: use map[string]any as soon as https://github.com/kubernetes-sigs/controller-tools/issues/636 is resolved
	certificatesDir := *controllerManagerCertificatesVolumeMount.MountPath
	enabledControllers := []string{"*", kubenames.BootstrapSignerController, kubenames.TokenCleanerController}
	leaderElect := *hostedControlPlane.Spec.Deployment.ControllerManager.Replicas > 1
	args := map[string]string{
		"allocate-node-cidrs":              "false",
		"authentication-kubeconfig":        kubeconfigPath,
		"authorization-kubeconfig":         kubeconfigPath,
		"kubeconfig":                       kubeconfigPath,
		"bind-address":                     "0.0.0.0",
		"leader-elect":                     strconv.FormatBool(leaderElect),
		"cluster-name":                     cluster.Name,
		"client-ca-file":                   path.Join(certificatesDir, konstants.CACertName),
		"cluster-signing-cert-file":        path.Join(certificatesDir, konstants.CACertName),
		"cluster-signing-key-file":         path.Join(certificatesDir, konstants.CAKeyName),
		"controllers":                      strings.Join(enabledControllers, ","),
		"requestheader-client-ca-file":     path.Join(certificatesDir, konstants.FrontProxyCACertName),
		"root-ca-file":                     path.Join(certificatesDir, konstants.CACertName),
		"service-account-private-key-file": path.Join(certificatesDir, konstants.ServiceAccountPrivateKeyName),
		"use-service-account-credentials":  "true",
	}

	return operatorutil.ArgsToSlice(
		ctx,
		hostedControlPlane.Spec.Deployment.ControllerManager.Args,
		args,
	)
}

func (arr *apiServerResourcesReconciler) setAuditWebhookPorts(
	auditConfig *v1alpha1.Audit,
	egressPortComponents map[int32][]string,
) error {
	ports := slices.SliceToMap(auditConfig.Webhook.Targets,
		func(target v1alpha1.AuditWebhookTarget) (int32, error) {
			_, portStr, err := net.SplitHostPort(target.Server)
			if err != nil {
				return 0, fmt.Errorf("failed to parse audit webhook target server %q: %w", target.Server, err)
			}
			port, err := strconv.ParseInt(portStr, 10, 32)
			if err != nil {
				return 0, fmt.Errorf("failed to parse audit webhook target server port %q: %w", portStr, err)
			}
			return int32(port), nil
		})
	if errs := slices.Values(ports); len(errs) > 0 {
		return fmt.Errorf("failed to parse audit webhook target servers: %w", errors.Join(errs...))
	}
	for port := range ports {
		if egressPortComponents[port] == nil {
			egressPortComponents[port] = []string{arr.worldComponent}
		} else if !slices.Contains(egressPortComponents[port], arr.worldComponent) {
			// vanilla network policies do not support DNS names.
			egressPortComponents[port] = append(egressPortComponents[port], arr.worldComponent)
		}
	}
	return nil
}

func (arr *apiServerResourcesReconciler) generateWebhookConfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (string, string, error) {
	auditConfig := hostedControlPlane.Spec.Deployment.APIServer.Audit
	targetCount := len(auditConfig.Webhook.Targets)
	targetConfigTargetName := "webhook"
	targetServer := auditConfig.Webhook.Targets[0].Server
	envoyConfig := ""

	if targetCount > 1 {
		if envoyTargetServer, envoyConfigString, err := arr.generateEnvoyConfig(ctx, hostedControlPlane); err != nil {
			return "", "", err
		} else {
			targetServer = envoyTargetServer
			envoyConfig = envoyConfigString
		}
	}

	targetConfig := api.Config{
		Clusters: map[string]*api.Cluster{
			targetConfigTargetName: {
				Server: targetServer,
			},
		},
		CurrentContext: targetConfigTargetName,
		Contexts: map[string]*api.Context{
			targetConfigTargetName: {
				Cluster:  targetConfigTargetName,
				AuthInfo: "kube-apiserver",
			},
		},
	}

	kubeconfigBytes, err := clientcmd.Write(targetConfig)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal audit webhook kubeconfig: %w", err)
	}
	return string(kubeconfigBytes), envoyConfig, nil
}

func (arr *apiServerResourcesReconciler) generateEnvoyConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (string, string, error) {
	auditConfig := hostedControlPlane.Spec.Deployment.APIServer.Audit

	route := map[string]any{
		"cluster": "webhook_service_0",
		"request_mirror_policies": slices.RepeatBy(len(auditConfig.Webhook.Targets)-1, func(i int) map[string]any {
			return map[string]any{
				"cluster":                           fmt.Sprintf("webhook_service_%d", i+1),
				"disable_shadow_host_suffix_append": true,
			}
		}),
	}

	clusters := slices.Map(
		auditConfig.Webhook.Targets,
		func(target v1alpha1.AuditWebhookTarget, index int) slices.Tuple2[map[string]any, error] {
			host, portStr, _ := net.SplitHostPort(target.Server)
			port, _ := strconv.Atoi(portStr)
			cluster := map[string]any{
				"name": fmt.Sprintf("webhook_service_%d", index),
				"load_assignment": map[string]any{
					"cluster_name": fmt.Sprintf("webhook_service_%d", index),
					"endpoints": []map[string]any{
						{
							"lb_endpoints": []map[string]any{
								{
									"endpoint": map[string]any{
										"address": map[string]any{
											"socket_address": map[string]any{
												"address":    host,
												"port_value": port,
											},
										},
									},
								},
							},
						},
					},
				},
			}
			if target.Authentication != nil {
				secret, err := arr.KubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
					Get(ctx, target.Authentication.SecretName, metav1.GetOptions{})
				if err != nil {
					return slices.T2[map[string]any, error](nil, fmt.Errorf(
						"failed to get audit webhook target authentication secret %s/%s: %w",
						hostedControlPlane.Namespace,
						target.Authentication.SecretName,
						err,
					))
				}
				token, ok := secret.Data[target.Authentication.TokenKey]
				if !ok {
					return slices.T2[map[string]any, error](nil, fmt.Errorf(
						"failed to get audit webhook target authentication token key %s in secret %s/%s: %w",
						target.Authentication.TokenKey,
						hostedControlPlane.Namespace,
						target.Authentication.SecretName,
						errWebhookSecretIsMissingKey,
					))
				}
				cluster["filters"] = []map[string]any{
					{
						"name":  "envoy.filters.network.http_connection_manager",
						"@type": "type.googleapis.com/envoy.extensions.filters.http.header_mutation.v3.HeaderMutation",
						"mutations": []map[string]any{
							{
								"request_mutations": []map[string]any{
									{
										"append": []map[string]any{
											{
												"header": map[string]any{
													"key":   "Authorization",
													"value": fmt.Sprintf("Bearer %s", token),
												},
												"append_action": "OVERWRITE_IF_EXISTS_OR_ADD",
											},
										},
									},
								},
							},
						},
					},
				}
			}
			return slices.T2[map[string]any, error](cluster, nil)
		},
	)

	if errs := slices.Filter(clusters, func(t slices.Tuple2[map[string]any, error], _ int) bool {
		return t.B != nil
	}); len(errs) > 0 {
		return "", "", fmt.Errorf("failed to parse audit webhook target servers: %w", errors.Join(
			slices.Map(errs, func(t slices.Tuple2[map[string]any, error], _ int) error {
				return t.B
			})...),
		)
	}

	envoyConfig := map[string]any{
		"static_resources": map[string]any{
			"listeners": []map[string]any{
				{
					"address": map[string]any{
						"socket_address": map[string]any{
							"address":    "127.0.0.1",
							"port_value": arr.envoyPort,
						},
					},
					"filter_chains": []map[string]any{
						{
							"filters": []map[string]any{
								{
									"name": "envoy.filters.network.http_connection_manager",
									"typed_config": map[string]any{
										"@type": "type.googleapis.com/envoy.extensions.filters.network." +
											"http_connection_manager.v3.HttpConnectionManager",
										"stat_prefix": "ingress_http",
										"route_config": map[string]any{
											"name": "local_route",
											"virtual_hosts": []map[string]any{
												{
													"name":    "local_service",
													"domains": []string{"*"},
													"routes": []map[string]any{
														{
															"match": map[string]any{
																"path": "/",
															},
															"route": route,
														},
													},
												},
											},
										},
										"http_filters": []map[string]any{
											{
												"name": "envoy.filters.http.router",
												"typed_config": map[string]any{
													"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			"clusters": slices.Map(clusters, func(t slices.Tuple2[map[string]any, error], _ int) map[string]any {
				return t.A
			}),
		},
	}

	if envoyYaml, err := yaml.Marshal(envoyConfig); err != nil {
		return "", "", fmt.Errorf("failed to marshal envoy config: %w", err)
	} else {
		return string(envoyYaml),
			fmt.Sprintf("http://%s", net.JoinHostPort("localhost", strconv.Itoa(int(arr.envoyPort)))),
			nil
	}
}
