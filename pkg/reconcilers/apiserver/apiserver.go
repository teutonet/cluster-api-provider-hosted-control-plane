package apiserver

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
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	kubenames "k8s.io/kubernetes/cmd/kube-controller-manager/names"
	kubeadmv1beta4 "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta4"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

var (
	errDeploymentNotReady = fmt.Errorf("deployment is not ready: %w", operatorutil.ErrRequeueRequired)
	errServiceNotReady    = fmt.Errorf("service is not ready: %w", operatorutil.ErrRequeueRequired)
)

type ApiServerResourcesReconciler interface {
	ReconcileApiServerService(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
	ReconcileApiServerDeployments(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
}

func NewApiServerResourcesReconciler(
	kubernetesClient kubernetes.Interface,
	apiServerServicePort int32,
	apiServerServiceLegacyPortName string,
	etcdComponentLabel string,
	etcdClientPort int32,
	konnectivityServicePort int32,
	konnectivityClientKubeconfigName string,
	konnectivityServerAudience string,
) ApiServerResourcesReconciler {
	return &apiServerResourcesReconciler{
		kubernetesClient:                 kubernetesClient,
		apiServerServicePort:             apiServerServicePort,
		apiServerServiceLegacyPortName:   apiServerServiceLegacyPortName,
		etcdComponentLabel:               etcdComponentLabel,
		etcdClientPort:                   etcdClientPort,
		konnectivityServicePort:          konnectivityServicePort,
		konnectivityClientKubeconfigName: konnectivityClientKubeconfigName,
		konnectivityServerAudience:       konnectivityServerAudience,
		tracer:                           tracing.GetTracer("apiServerResources"),
	}
}

type apiServerResourcesReconciler struct {
	kubernetesClient                 kubernetes.Interface
	apiServerServicePort             int32
	apiServerServiceLegacyPortName   string
	etcdComponentLabel               string
	etcdClientPort                   int32
	konnectivityServicePort          int32
	konnectivityClientKubeconfigName string
	konnectivityServerAudience       string
	tracer                           string
}

var _ ApiServerResourcesReconciler = &apiServerResourcesReconciler{}

var (
	egressSelectorConfigMountPath       = "/etc/kubernetes/egress/configurations"
	EgressSelectorConfigurationFileName = "egress-selector-configuration.yaml"
)

var (
	componentAPIServer         = "api-server"
	componentControllerManager = "controller-manager"
	componentScheduler         = "scheduler"
)

var (
	apiContainerPortName          = intstr.FromString("api")
	konnectivityContainerPortName = intstr.FromString("konnectivity")
)

var KonnectivityKubeconfigFileName = "konnectivity-server.conf"

func (arr *apiServerResourcesReconciler) ReconcileApiServerDeployments(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	if err := arr.reconcileKonnectivityConfig(ctx, hostedControlPlane, cluster); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity config: %w", err)
	}
	if err := arr.reconcileAPIServerDeployment(ctx, hostedControlPlane, cluster); err != nil {
		return fmt.Errorf("failed to reconcile API server deployment: %w", err)
	}

	needsRequeue := false
	if err := arr.reconcileControllerManagerDeployment(ctx, hostedControlPlane, cluster); err != nil {
		if !errors.Is(err, errDeploymentNotReady) {
			return fmt.Errorf("failed to reconcile controller manager deployment: %w", err)
		}
		needsRequeue = true
	}

	if err := arr.reconcileSchedulerDeployment(ctx, hostedControlPlane, cluster); err != nil {
		if !errors.Is(err, errDeploymentNotReady) {
			return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
		}
		needsRequeue = true
	}

	if needsRequeue {
		return operatorutil.ErrRequeueRequired
	}

	hostedControlPlane.Status.Version = hostedControlPlane.Spec.Version

	return nil
}

//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;update;patch

func (arr *apiServerResourcesReconciler) reconcileKonnectivityConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.tracer, "ReconcileKonnectivityConfig",
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
									UDSName: "/run/konnectivity/konnectivity-server.sock",
								},
							},
						},
					},
				},
			}

			buf, err := operatorutil.ToYaml(egressSelectorConfig)
			if err != nil {
				return fmt.Errorf("failed to marshal egress selector configuration: %w", err)
			}

			configMap := corev1ac.ConfigMap(
				names.GetKonnectivityConfigMapName(cluster),
				hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string]string{
					EgressSelectorConfigurationFileName: buf.String(),
				})

			_, err = arr.kubernetesClient.CoreV1().ConfigMaps(hostedControlPlane.Namespace).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to patch konnectivity configmap: %w", err)
		},
	)
}

func (arr *apiServerResourcesReconciler) reconcileAPIServerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.tracer, "ReconcileAPIServerDeployment",
		func(ctx context.Context, span trace.Span) error {
			apiServerCertificatesVolume := arr.createAPIServerCertificatesVolume(cluster)
			egressSelectorConfigVolume := arr.createEgressSelectorConfigVolume(cluster)
			konnectivityUDSVolume := arr.createKonnectivityUDSVolume()
			konnectivityCertificatesVolume := arr.createKonnectivityCertificatesVolume(cluster)
			konnectivityKubeconfigVolume := arr.createKonnectivityKubeconfigVolume(cluster)

			konnectivityUDSMount := corev1ac.VolumeMount().
				WithName(*konnectivityUDSVolume.Name).
				WithMountPath("/run/konnectivity")

			volumes := []*corev1ac.VolumeApplyConfiguration{
				apiServerCertificatesVolume,
				egressSelectorConfigVolume,
				konnectivityUDSVolume,
				konnectivityCertificatesVolume,
				konnectivityKubeconfigVolume,
			}

			additionalVolumes, additionalApiServerVolumeMounts := arr.extractAdditionalVolumesAndMounts(
				hostedControlPlane.Spec.Deployment.APIServer.Mounts,
			)

			volumes = append(volumes, additionalVolumes...)

			apiServerContainer := arr.createAPIServerContainer(
				hostedControlPlane,
				cluster,
				apiServerCertificatesVolume,
				egressSelectorConfigVolume,
				konnectivityUDSMount,
				additionalApiServerVolumeMounts,
			)
			konnectivityContainer, err := arr.createKonnectivityContainer(
				hostedControlPlane,
				konnectivityCertificatesVolume,
				konnectivityKubeconfigVolume,
				konnectivityUDSMount,
			)
			if err != nil {
				return fmt.Errorf("failed to create konnectivity container: %w", err)
			}

			if deployment, err := arr.reconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				*hostedControlPlane.Spec.Replicas,
				componentAPIServer,
				arr.etcdComponentLabel,
				[]*corev1ac.ContainerApplyConfiguration{apiServerContainer, konnectivityContainer},
				volumes,
			); err != nil {
				return err
			} else {
				selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
				if err != nil {
					return fmt.Errorf("failed to convert label selector: %w", err)
				}
				hostedControlPlane.Status.Selector = selector.String()
				hostedControlPlane.Status.Replicas = deployment.Status.Replicas
				hostedControlPlane.Status.UnavailableReplicas = deployment.Status.UnavailableReplicas
				hostedControlPlane.Status.ReadyReplicas = deployment.Status.ReadyReplicas
				hostedControlPlane.Status.UpdatedReplicas = deployment.Status.UpdatedReplicas
			}

			return nil
		},
	)
}

func (arr *apiServerResourcesReconciler) extractAdditionalVolumesAndMounts(
	mounts map[string]v1alpha1.HostedControlPlaneMount,
) ([]*corev1ac.VolumeApplyConfiguration, []*corev1ac.VolumeMountApplyConfiguration) {
	entries := slices.MapEntries(mounts, func(
		name string, mount v1alpha1.HostedControlPlaneMount,
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
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.tracer, "ReconcileControllerManagerDeployment",
		func(ctx context.Context, span trace.Span) error {
			controllerManagerCertificatesVolume := arr.createControllerManagerCertificatesVolume(cluster)
			controllerManagerKubeconfigVolume := arr.createControllerManagerKubeconfigVolume(cluster)

			controllerManagerCertificatesVolumeMount := corev1ac.VolumeMount().
				WithName(*controllerManagerCertificatesVolume.Name).
				WithMountPath(kubeadmv1beta4.DefaultCertificatesDir).
				WithReadOnly(true)

			container := arr.createControllerManagerContainer(
				hostedControlPlane,
				controllerManagerCertificatesVolumeMount,
				controllerManagerKubeconfigVolume,
			)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				controllerManagerCertificatesVolume,
				controllerManagerKubeconfigVolume,
			}

			_, err := arr.reconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				*hostedControlPlane.Spec.Replicas/int32(3),
				componentControllerManager,
				componentAPIServer,
				[]*corev1ac.ContainerApplyConfiguration{container},
				volumes,
			)
			return err
		},
	)
}

func (arr *apiServerResourcesReconciler) reconcileSchedulerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.tracer, "ReconcileSchedulerDeployment",
		func(ctx context.Context, span trace.Span) error {
			schedulerKubeconfigVolume := arr.createSchedulerKubeconfigVolume(cluster)

			container := arr.createSchedulerContainer(
				hostedControlPlane,
				schedulerKubeconfigVolume,
			)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				schedulerKubeconfigVolume,
			}

			_, err := arr.reconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				*hostedControlPlane.Spec.Replicas/int32(3),
				componentScheduler,
				componentAPIServer,
				[]*corev1ac.ContainerApplyConfiguration{container},
				volumes,
			)
			return err
		},
	)
}

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;patch

func (arr *apiServerResourcesReconciler) reconcileDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	replicas int32,
	component string,
	targetComponent string,
	containers []*corev1ac.ContainerApplyConfiguration,
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, error) {
	template := corev1ac.PodTemplateSpec().
		WithLabels(names.GetControlPlaneLabels(cluster, component)).
		WithSpec(corev1ac.PodSpec().
			WithTopologySpreadConstraints(
				operatorutil.CreatePodTopologySpreadConstraints(
					names.GetControlPlaneSelector(cluster, component),
				),
			).
			WithAutomountServiceAccountToken(false).
			WithEnableServiceLinks(false).
			WithContainers(containers...).
			WithVolumes(volumes...).
			WithAffinity(corev1ac.Affinity().
				WithPodAffinity(corev1ac.PodAffinity().
					WithPreferredDuringSchedulingIgnoredDuringExecution(
						corev1ac.WeightedPodAffinityTerm().
							WithWeight(100).
							WithPodAffinityTerm(corev1ac.PodAffinityTerm().
								WithLabelSelector(names.GetControlPlaneSelector(cluster, targetComponent)).
								WithTopologyKey(corev1.LabelHostname),
							),
					),
				)),
		)

	template, err := operatorutil.SetChecksumAnnotations(ctx, arr.kubernetesClient, cluster.Namespace, template)
	if err != nil {
		return nil, fmt.Errorf("failed to set checksum annotations: %w", err)
	}

	deploymentName := fmt.Sprintf("%s-%s", cluster.Name, component)
	deployment := appsv1ac.Deployment(deploymentName, cluster.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, component)).
		WithSpec(appsv1ac.DeploymentSpec().
			WithStrategy(appsv1ac.DeploymentStrategy().
				WithType(appsv1.RollingUpdateDeploymentStrategyType).
				WithRollingUpdate(appsv1ac.RollingUpdateDeployment().
					WithMaxSurge(intstr.FromInt32(*hostedControlPlane.Spec.Replicas)),
				),
			).
			WithReplicas(replicas).
			WithSelector(names.GetControlPlaneSelector(cluster, component)).
			WithTemplate(template),
		).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane))

	if err := operatorutil.ValidateMounts(deployment.Spec.Template.Spec); err != nil {
		return nil, fmt.Errorf("deployment %s has mounts without corresponding volume: %w", deploymentName, err)
	}

	appliedDeployment, err := arr.kubernetesClient.AppsV1().Deployments(hostedControlPlane.Namespace).Apply(ctx,
		deployment,
		operatorutil.ApplyOptions,
	)
	if err != nil {
		return nil, errorsUtil.IfErrErrorf("failed to patch deployment: %w", err)
	}

	if !isDeploymentReady(appliedDeployment) {
		return nil, errDeploymentNotReady
	}

	return appliedDeployment, nil
}

func isDeploymentReady(deployment *appsv1.Deployment) bool {
	if deployment == nil {
		return false
	}

	if deployment.Spec.Replicas != nil && deployment.Status.ReadyReplicas < *deployment.Spec.Replicas {
		return false
	}

	if deployment.Spec.Replicas != nil && deployment.Status.AvailableReplicas < *deployment.Spec.Replicas {
		return false
	}

	if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
		return false
	}

	if deployment.Status.ObservedGeneration < deployment.Generation {
		return false
	}

	return true
}

//+kubebuilder:rbac:groups=core,resources=services,verbs=create;update;patch

func (arr *apiServerResourcesReconciler) ReconcileApiServerService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, arr.tracer, "ReconcileApiServerService",
		func(ctx context.Context, span trace.Span) error {
			labels := names.GetControlPlaneLabels(cluster, componentAPIServer)
			apiPort := corev1ac.ServicePort().
				WithName("api").
				WithPort(arr.apiServerServicePort).
				WithTargetPort(apiContainerPortName).
				WithProtocol(corev1.ProtocolTCP)
			legacyAPIPort := corev1ac.ServicePort().
				WithName(arr.apiServerServiceLegacyPortName).
				WithPort(6443).
				WithTargetPort(*apiPort.TargetPort).
				WithProtocol(*apiPort.Protocol)
			service := corev1ac.Service(names.GetServiceName(cluster), hostedControlPlane.Namespace).
				WithLabels(labels).
				WithSpec(corev1ac.ServiceSpec().
					WithType(corev1.ServiceTypeLoadBalancer).
					WithSelector(labels).
					WithPorts(
						apiPort,
						legacyAPIPort,
						corev1ac.ServicePort().
							WithName("konnectivity").
							WithPort(arr.konnectivityServicePort).
							WithTargetPort(konnectivityContainerPortName).
							WithProtocol(corev1.ProtocolTCP),
					),
				).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane))
			appliedService, err := arr.kubernetesClient.CoreV1().Services(hostedControlPlane.Namespace).Apply(ctx,
				service,
				operatorutil.ApplyOptions,
			)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to patch service: %w", err)
			}

			if len(appliedService.Status.LoadBalancer.Ingress) == 0 {
				return fmt.Errorf("service load balancer is not ready yet: %w", errServiceNotReady)
			}

			return nil
		},
	)
}

func (arr *apiServerResourcesReconciler) createSchedulerKubeconfigVolume(
	cluster *capiv1.Cluster,
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
	cluster *capiv1.Cluster,
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
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(cluster, arr.konnectivityClientKubeconfigName)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithPath(KonnectivityKubeconfigFileName),
			),
		)
}

func (arr *apiServerResourcesReconciler) createKonnectivityUDSVolume() *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-uds").
		WithEmptyDir(corev1ac.EmptyDirVolumeSource().WithMedium(corev1.StorageMediumMemory))
}

func (arr *apiServerResourcesReconciler) createEgressSelectorConfigVolume(
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-config").
		WithConfigMap(corev1ac.ConfigMapVolumeSource().
			WithName(names.GetKonnectivityConfigMapName(cluster)),
		)
}

func (arr *apiServerResourcesReconciler) createAPIServerCertificatesVolume(
	cluster *capiv1.Cluster,
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
					WithName(names.GetEtcdAPIServerClientSecretName(cluster)).
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
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-certificates").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetAPIServerSecretName(cluster)),
		)
}

func (arr *apiServerResourcesReconciler) createControllerManagerCertificatesVolume(
	cluster *capiv1.Cluster,
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
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	apiServerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	apiPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	certificatesDir := *apiServerCertificatesVolumeMount.MountPath
	egressSelectorConfigDir := *egressSelectorConfigVolumeMount.MountPath
	nodeAddressTypes := slices.Map([]corev1.NodeAddressType{
		corev1.NodeInternalIP,
		corev1.NodeInternalDNS,
		corev1.NodeExternalDNS,
		corev1.NodeHostName,
	},
		func(item corev1.NodeAddressType, _ int) string {
			return string(item)
		})

	args := map[string]string{
		"advertise-address":                  cluster.Spec.ControlPlaneEndpoint.Host,
		"allow-privileged":                   "true",
		"authorization-mode":                 konstants.ModeNode + "," + konstants.ModeRBAC,
		"client-ca-file":                     path.Join(certificatesDir, konstants.CACertName),
		"enable-bootstrap-token-auth":        "true",
		"egress-selector-config-file":        path.Join(egressSelectorConfigDir, EgressSelectorConfigurationFileName),
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
		"service-cluster-ip-range":           "10.96.0.0/12",
		"tls-cert-file":                      path.Join(certificatesDir, konstants.APIServerCertName),
		"tls-private-key-file":               path.Join(certificatesDir, konstants.APIServerKeyName),
		"etcd-servers": fmt.Sprintf("https://%s",
			net.JoinHostPort(names.GetEtcdClientServiceName(cluster), strconv.Itoa(int(arr.etcdClientPort))),
		),
		"etcd-cafile":   path.Join(certificatesDir, konstants.EtcdCACertName),
		"etcd-certfile": path.Join(certificatesDir, konstants.APIServerEtcdClientCertName),
		"etcd-keyfile":  path.Join(certificatesDir, konstants.APIServerEtcdClientKeyName),
	}

	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.Deployment.APIServer.Args, args)
}

func (arr *apiServerResourcesReconciler) createKonnectivityContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	konnectivityCertificatesVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) (*corev1ac.ContainerApplyConfiguration, error) {
	konnectivityPort := corev1ac.ContainerPort().
		WithName("konnectivity").
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
		WithImage(fmt.Sprintf("registry.k8s.io/kas-network-proxy/proxy-server:v0.%d.0", minorVersion)).
		WithImagePullPolicy(corev1.PullAlways).
		WithArgs(arr.buildKonnectivityServerArgs(
			hostedControlPlane,
			konnectivityCertificatesVolumeMount,
			konnectivityKubeconfigVolumeMount,
			konnectivityUDSVolumeMount,
			konnectivityPort, adminPort, healthPort,
		)...).
		WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
			hostedControlPlane.Spec.Deployment.Konnectivity.Resources,
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

func (arr *apiServerResourcesReconciler) buildKonnectivityServerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	konnectivityCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	konnectivityKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	konnectivityPort *corev1ac.ContainerPortApplyConfiguration,
	adminPort *corev1ac.ContainerPortApplyConfiguration,
	healthPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	args := map[string]string{
		"agent-namespace":         "kube-system",
		"agent-service-account":   "konnectivity-agent",
		"authentication-audience": arr.konnectivityServerAudience,
		"cluster-cert":            path.Join(*konnectivityCertificatesVolumeMount.MountPath, corev1.TLSCertKey),
		"cluster-key":             path.Join(*konnectivityCertificatesVolumeMount.MountPath, corev1.TLSPrivateKeyKey),
		"kubeconfig": path.Join(
			*konnectivityKubeconfigVolumeMount.MountPath,
			KonnectivityKubeconfigFileName,
		),
		"server-count": strconv.Itoa(int(*hostedControlPlane.Spec.Replicas)),
		"admin-port":   strconv.Itoa(int(*adminPort.ContainerPort)),
		"agent-port":   strconv.Itoa(int(*konnectivityPort.ContainerPort)),
		"health-port":  strconv.Itoa(int(*healthPort.ContainerPort)),
		"server-port":  "0",
		"uds-name":     path.Join(*konnectivityUDSVolumeMount.MountPath, "konnectivity-server.sock"),
		"mode":         "grpc",
	}

	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.Deployment.Konnectivity.Args, args)
}

func (arr *apiServerResourcesReconciler) createAPIServerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	apiServerCertificatesVolume *corev1ac.VolumeApplyConfiguration,
	egressSelectorConfigVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	additionalVolumeMounts []*corev1ac.VolumeMountApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	apiPort := corev1ac.ContainerPort().
		WithName(apiContainerPortName.String()).
		WithContainerPort(konstants.KubeAPIServerPort).
		WithProtocol(corev1.ProtocolTCP)

	apiServerCertificatesVolumeMount := corev1ac.VolumeMount().
		WithName(*apiServerCertificatesVolume.Name).
		WithMountPath(kubeadmv1beta4.DefaultCertificatesDir).
		WithReadOnly(true)
	egressSelectorConfigVolumeMount := corev1ac.VolumeMount().
		WithName(*egressSelectorConfigVolume.Name).
		WithMountPath(egressSelectorConfigMountPath).
		WithReadOnly(true)

	volumeMounts := []*corev1ac.VolumeMountApplyConfiguration{
		apiServerCertificatesVolumeMount,
		egressSelectorConfigVolumeMount,
		konnectivityUDSVolumeMount,
	}

	volumeMounts = append(volumeMounts, additionalVolumeMounts...)

	return corev1ac.Container().
		WithName(konstants.KubeAPIServer).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-apiserver:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("kube-apiserver").
		WithArgs(arr.buildAPIServerArgs(
			hostedControlPlane, cluster,
			apiServerCertificatesVolumeMount, egressSelectorConfigVolumeMount,
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
	prefix string,
	port int,
) *corev1ac.ContainerPortApplyConfiguration {
	containerPort := corev1ac.ContainerPort().
		WithName(fmt.Sprintf("%s-probe-port", prefix)). // TODO: use konstants.probePort when available
		WithContainerPort(int32(port)).                 //nolint:gosec // port is expected to be within int32 range
		WithProtocol(corev1.ProtocolTCP)
	return containerPort
}

func (arr *apiServerResourcesReconciler) createSchedulerContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	schedulerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*schedulerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	probePort := arr.createProbePort("s", konstants.KubeSchedulerPort)
	return corev1ac.Container().
		WithName(konstants.KubeScheduler).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-scheduler:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("kube-scheduler").
		WithArgs(arr.buildSchedulerArgs(hostedControlPlane, schedulerKubeconfigVolumeMount)...).
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
	hostedControlPlane *v1alpha1.HostedControlPlane,
	controllerManagerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	controllerManagerKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	controllerManagerKubeconfigVolumeMount := corev1ac.VolumeMount().
		WithName(*controllerManagerKubeconfigVolume.Name).
		WithMountPath(konstants.KubernetesDir)
	probePort := arr.createProbePort("c", konstants.KubeControllerManagerPort)
	return corev1ac.Container().
		WithName(konstants.KubeControllerManager).
		WithImage(fmt.Sprintf("registry.k8s.io/kube-controller-manager:%s", hostedControlPlane.Spec.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("kube-controller-manager").
		WithArgs(arr.buildControllerManagerArgs(
			hostedControlPlane,
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
	hostedControlPlane *v1alpha1.HostedControlPlane,
	schedulerKubeconfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	kubeconfigPath := path.Join(*schedulerKubeconfigVolumeMount.MountPath, konstants.SchedulerKubeConfigFileName)

	args := map[string]string{
		"authentication-kubeconfig": kubeconfigPath,
		"authorization-kubeconfig":  kubeconfigPath,
		"kubeconfig":                kubeconfigPath,
		"bind-address":              "0.0.0.0",
		"leader-elect":              "true",
	}

	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.Deployment.Scheduler.Args, args)
}

func (arr *apiServerResourcesReconciler) buildControllerManagerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
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
		"controllers":                      strings.Join(enabledControllers, ","),
		"service-cluster-ip-range":         "10.96.0.0/16",
		"cluster-cidr":                     "192.168.0.0/16",
		"requestheader-client-ca-file":     path.Join(certificatesDir, konstants.FrontProxyCACertName),
		"root-ca-file":                     path.Join(certificatesDir, konstants.CACertName),
		"service-account-private-key-file": path.Join(certificatesDir, konstants.ServiceAccountPrivateKeyName),
		"use-service-account-credentials":  "true",
	}

	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.Deployment.ControllerManager.Args, args)
}
