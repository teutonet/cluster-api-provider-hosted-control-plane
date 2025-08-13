package hostedcontrolplane

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"path"
	"strconv"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	kubenames "k8s.io/kubernetes/cmd/kube-controller-manager/names"
	kubeadmv1beta4 "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta4"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"
)

var ErrDeploymentNotReady = fmt.Errorf("deployment is not ready: %w", ErrRequeueRequired)

type APIServerResourcesReconciler struct {
	kubernetesClient kubernetes.Interface
}

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
	APIServerServicePort    = int32(443)
	KonnectivityServicePort = int32(8132)
)

var (
	apiContainerPortName          = intstr.FromString("api")
	konnectivityContainerPortName = intstr.FromString("konnectivity")
)

var KonnectivityKubeconfigFileName = "konnectivity-server.conf"

func (dr *APIServerResourcesReconciler) ReconcileAPIServerResources(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	if err := dr.reconcileAPIServerDeployment(ctx, hostedControlPlane, cluster); err != nil {
		return fmt.Errorf("failed to reconcile API server deployment: %w", err)
	}

	if err := dr.reconcileAPIServerService(ctx, hostedControlPlane, cluster); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}

	needsRequeue := false
	if err := dr.reconcileControllerManagerDeployment(ctx, hostedControlPlane, cluster); err != nil {
		if !errors.Is(err, ErrDeploymentNotReady) {
			return fmt.Errorf("failed to reconcile controller manager deployment: %w", err)
		}
		needsRequeue = true
	}

	if err := dr.reconcileSchedulerDeployment(ctx, hostedControlPlane, cluster); err != nil {
		if !errors.Is(err, ErrDeploymentNotReady) {
			return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
		}
		needsRequeue = true
	}

	if needsRequeue {
		return ErrRequeueRequired
	}

	hostedControlPlane.Status.Version = hostedControlPlane.Spec.Version

	return nil
}

func (dr *APIServerResourcesReconciler) reconcileAPIServerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileAPIServerDeployment",
		func(ctx context.Context, span trace.Span) error {
			apiServerCertificatesVolume := dr.createAPIServerCertificatesVolume(cluster)
			egressSelectorConfigVolume := dr.createEgressSelectorConfigVolume(cluster)
			konnectivityUDSVolume := dr.createKonnectivityUDSVolume()
			konnectivityCertificatesVolume := dr.createKonnectivityCertificatesVolume(cluster)
			konnectivityKubeconfigVolume := dr.createKonnectivityKubeconfigVolume(cluster)

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

			additionalVolumes, additionalApiServerVolumeMounts := dr.extractAdditionalVolumesAndMounts(
				hostedControlPlane.Spec.Deployment.APIServer.Mounts,
			)

			volumes = append(volumes, additionalVolumes...)

			apiServerContainer := dr.createAPIServerContainer(
				hostedControlPlane,
				cluster,
				apiServerCertificatesVolume,
				egressSelectorConfigVolume,
				konnectivityUDSMount,
				additionalApiServerVolumeMounts,
			)
			konnectivityContainer := dr.createKonnectivityContainer(
				hostedControlPlane,
				konnectivityCertificatesVolume,
				konnectivityKubeconfigVolume,
				konnectivityUDSMount,
			)

			if deployment, err := dr.reconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				componentAPIServer,
				ComponentETCD,
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

func (dr *APIServerResourcesReconciler) extractAdditionalVolumesAndMounts(
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

func (dr *APIServerResourcesReconciler) reconcileControllerManagerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileControllerManagerDeployment",
		func(ctx context.Context, span trace.Span) error {
			controllerManagerCertificatesVolume := dr.createControllerManagerCertificatesVolume(cluster)
			controllerManagerKubeconfigVolume := dr.createControllerManagerKubeconfigVolume(cluster)

			controllerManagerCertificatesVolumeMount := corev1ac.VolumeMount().
				WithName(*controllerManagerCertificatesVolume.Name).
				WithMountPath(kubeadmv1beta4.DefaultCertificatesDir).
				WithReadOnly(true)

			container := dr.createControllerManagerContainer(
				hostedControlPlane,
				controllerManagerCertificatesVolumeMount,
				controllerManagerKubeconfigVolume,
			)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				controllerManagerCertificatesVolume,
				controllerManagerKubeconfigVolume,
			}

			_, err := dr.reconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
				componentControllerManager,
				componentAPIServer,
				[]*corev1ac.ContainerApplyConfiguration{container},
				volumes,
			)
			return err
		},
	)
}

func (dr *APIServerResourcesReconciler) reconcileSchedulerDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileSchedulerDeployment",
		func(ctx context.Context, span trace.Span) error {
			schedulerKubeconfigVolume := dr.createSchedulerKubeconfigVolume(cluster)

			container := dr.createSchedulerContainer(
				hostedControlPlane,
				schedulerKubeconfigVolume,
			)

			volumes := []*corev1ac.VolumeApplyConfiguration{
				schedulerKubeconfigVolume,
			}

			_, err := dr.reconcileDeployment(
				ctx,
				hostedControlPlane,
				cluster,
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

func (dr *APIServerResourcesReconciler) reconcileDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
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

	if secretChecksum, err := util.CalculateSecretChecksum(ctx, dr.kubernetesClient,
		hostedControlPlane.Namespace,
		extractSecretNames(template.Spec.Volumes),
	); err != nil {
		return nil, fmt.Errorf("failed to calculate secret checksum: %w", err)
	} else if secretChecksum != "" {
		template = template.WithAnnotations(map[string]string{
			"checksum/secrets": secretChecksum,
		})
	}

	if configMapChecksum, err := util.CalculateConfigMapChecksum(ctx, dr.kubernetesClient,
		hostedControlPlane.Namespace,
		extractConfigMapNames(template.Spec.Volumes),
	); err != nil {
		return nil, fmt.Errorf("failed to calculate configmap checksum: %w", err)
	} else if configMapChecksum != "" {
		template = template.WithAnnotations(map[string]string{
			"checksum/configmaps": configMapChecksum,
		})
	}

	deploymentName := fmt.Sprintf("%s-%s", cluster.Name, component)
	deployment := appsv1ac.Deployment(
		deploymentName,
		cluster.Namespace,
	).
		WithLabels(names.GetControlPlaneLabels(cluster, component)).
		WithSpec(appsv1ac.DeploymentSpec().
			WithReplicas(*hostedControlPlane.Spec.Replicas).
			WithSelector(names.GetControlPlaneSelector(cluster, component)).
			WithTemplate(template),
		).
		WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane))

	if err := operatorutil.ValidateMounts(deployment.Spec.Template.Spec); err != nil {
		return nil, fmt.Errorf("deployment %s has mounts without corresponding volume: %w", deploymentName, err)
	}

	appliedDeployment, err := dr.kubernetesClient.AppsV1().Deployments(hostedControlPlane.Namespace).Apply(ctx,
		deployment,
		applyOptions,
	)
	if err != nil {
		return nil, errorsUtil.IfErrErrorf("failed to patch deployment: %w", err)
	}

	if !isDeploymentReady(appliedDeployment) {
		return nil, ErrDeploymentNotReady
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

func (dr *APIServerResourcesReconciler) reconcileAPIServerService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileAPIServerService",
		func(ctx context.Context, span trace.Span) error {
			labels := names.GetControlPlaneLabels(cluster, componentAPIServer)
			service := corev1ac.Service(names.GetServiceName(cluster), hostedControlPlane.Namespace).
				WithLabels(labels).
				WithSpec(corev1ac.ServiceSpec().
					WithType(corev1.ServiceTypeClusterIP).
					WithSelector(labels).
					WithPorts(
						corev1ac.ServicePort().
							WithName("api").
							WithPort(APIServerServicePort).
							WithTargetPort(apiContainerPortName).
							WithProtocol(corev1.ProtocolTCP),
						corev1ac.ServicePort().
							WithName("konnectivity").
							WithPort(KonnectivityServicePort).
							WithTargetPort(konnectivityContainerPortName).
							WithProtocol(corev1.ProtocolTCP),
					),
				).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane))
			_, err := dr.kubernetesClient.CoreV1().Services(hostedControlPlane.Namespace).Apply(ctx,
				service,
				applyOptions,
			)

			return errorsUtil.IfErrErrorf("failed to patch service: %w", err)
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

func (dr *APIServerResourcesReconciler) createSchedulerKubeconfigVolume(
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

func (dr *APIServerResourcesReconciler) createControllerManagerKubeconfigVolume(
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

func (dr *APIServerResourcesReconciler) createKonnectivityKubeconfigVolume(
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-kubeconfig").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetKubeconfigSecretName(cluster, KonnectivityClientKubeconfigName)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(capisecretutil.KubeconfigDataName).
					WithPath(KonnectivityKubeconfigFileName),
			),
		)
}

func (dr *APIServerResourcesReconciler) createKonnectivityUDSVolume() *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-uds").
		WithEmptyDir(corev1ac.EmptyDirVolumeSource().WithMedium(corev1.StorageMediumMemory))
}

func (dr *APIServerResourcesReconciler) createEgressSelectorConfigVolume(
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-config").
		WithConfigMap(corev1ac.ConfigMapVolumeSource().
			WithName(names.GetKonnectivityConfigMapName(cluster)),
		)
}

func (dr *APIServerResourcesReconciler) createAPIServerCertificatesVolume(
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

func (dr *APIServerResourcesReconciler) createKonnectivityCertificatesVolume(
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("konnectivity-certificates").
		WithSecret(corev1ac.SecretVolumeSource().
			WithSecretName(names.GetCASecretName(cluster)).
			WithItems(
				corev1ac.KeyToPath().
					WithKey(corev1.TLSCertKey).
					WithPath(konstants.CACertName),
				corev1ac.KeyToPath().
					WithKey(corev1.TLSPrivateKeyKey).
					WithPath(konstants.CAKeyName),
			),
		)
}

func (dr *APIServerResourcesReconciler) createControllerManagerCertificatesVolume(
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

func (dr *APIServerResourcesReconciler) buildAPIServerArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	apiServerCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	egressSelectorConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	apiPort *corev1ac.ContainerPortApplyConfiguration,
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
		"authorization-mode":                 konstants.ModeNode + "," + konstants.ModeRBAC,
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
		"secure-port":                        strconv.Itoa(int(*apiPort.ContainerPort)),
		"service-account-issuer":             "https://kubernetes.default.svc",
		"service-account-key-file":           path.Join(certificatesDir, konstants.ServiceAccountPublicKeyName),
		"service-account-signing-key-file":   path.Join(certificatesDir, konstants.ServiceAccountPrivateKeyName),
		"service-cluster-ip-range":           "10.96.0.0/12",
		"tls-cert-file":                      path.Join(certificatesDir, konstants.APIServerCertName),
		"tls-private-key-file":               path.Join(certificatesDir, konstants.APIServerKeyName),
		"etcd-servers": fmt.Sprintf("https://%s",
			net.JoinHostPort(names.GetEtcdServiceName(cluster), "2379"),
		),
		"etcd-cafile":   path.Join(certificatesDir, konstants.EtcdCACertName),
		"etcd-certfile": path.Join(certificatesDir, konstants.APIServerEtcdClientCertName),
		"etcd-keyfile":  path.Join(certificatesDir, konstants.APIServerEtcdClientKeyName),
	}

	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.Deployment.APIServer.Args, args)
}

func (dr *APIServerResourcesReconciler) createKonnectivityContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	konnectivityCertificatesVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityKubeconfigVolume *corev1ac.VolumeApplyConfiguration,
	konnectivityUDSVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
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

	return corev1ac.Container().
		WithName("konnectivity-server").
		WithImage("registry.k8s.io/kas-network-proxy/proxy-server:v0.33.0").
		WithImagePullPolicy(corev1.PullAlways).
		WithArgs(dr.buildKonnectivityServerArgs(
			hostedControlPlane,
			konnectivityCertificatesVolumeMount,
			konnectivityKubeconfigVolumeMount,
			konnectivityUDSVolumeMount,
			konnectivityPort, adminPort, healthPort,
		)...).
		WithPorts(konnectivityPort, adminPort, healthPort).
		WithStartupProbe(dr.createStartupProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
		WithReadinessProbe(dr.createReadinessProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
		WithLivenessProbe(dr.createLivenessProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
		WithVolumeMounts(
			konnectivityCertificatesVolumeMount,
			konnectivityKubeconfigVolumeMount,
			konnectivityUDSVolumeMount,
		)
}

func (dr *APIServerResourcesReconciler) buildKonnectivityServerArgs(
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
		"authentication-audience": KonnectivityServerAudience,
		"cluster-cert":            path.Join(*konnectivityCertificatesVolumeMount.MountPath, konstants.CACertName),
		"cluster-key":             path.Join(*konnectivityCertificatesVolumeMount.MountPath, konstants.CAKeyName),
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

	return operatorutil.ArgsToSlice(args)
}

func (dr *APIServerResourcesReconciler) createAPIServerContainer(
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
		WithArgs(dr.buildAPIServerArgs(
			hostedControlPlane, cluster,
			apiServerCertificatesVolumeMount, egressSelectorConfigVolumeMount,
			apiPort,
		)...).
		WithPorts(apiPort).
		WithStartupProbe(dr.createStartupProbe(apiPort, "/livez", corev1.URISchemeHTTPS)).
		WithReadinessProbe(dr.createReadinessProbe(apiPort, "/readyz", corev1.URISchemeHTTPS)).
		WithLivenessProbe(dr.createLivenessProbe(apiPort, "/livez", corev1.URISchemeHTTPS)).
		WithVolumeMounts(volumeMounts...)
}

func (dr *APIServerResourcesReconciler) createStartupProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
	path string,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	healthCheckTimeout := konstants.ControlPlaneComponentHealthCheckTimeout.Seconds()
	periodSeconds := int32(10)
	failureThreshold := int32(math.Ceil(healthCheckTimeout / float64(periodSeconds)))
	return dr.createProbe(path, probePort, scheme).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(failureThreshold).
		WithPeriodSeconds(periodSeconds)
}

func (dr *APIServerResourcesReconciler) createReadinessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
	path string,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	return dr.createProbe(path, probePort, scheme).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(3).
		WithPeriodSeconds(1)
}

func (dr *APIServerResourcesReconciler) createLivenessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
	path string,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	return dr.createProbe(path, probePort, scheme).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(8).
		WithPeriodSeconds(10)
}

func (dr *APIServerResourcesReconciler) createProbe(
	path string,
	probePort *corev1ac.ContainerPortApplyConfiguration,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	return corev1ac.Probe().WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath(path).
		WithPort(intstr.FromString(*probePort.Name)).
		WithScheme(scheme),
	)
}

func (dr *APIServerResourcesReconciler) createProbePort(
	prefix string,
	port int,
) *corev1ac.ContainerPortApplyConfiguration {
	containerPort := corev1ac.ContainerPort().
		WithName(fmt.Sprintf("%s-probe-port", prefix)). // TODO: use konstants.probePort when available
		WithContainerPort(int32(port)).                 //nolint:gosec // port is expected to be within int32 range
		WithProtocol(corev1.ProtocolTCP)
	return containerPort
}

func (dr *APIServerResourcesReconciler) createSchedulerContainer(
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
		WithStartupProbe(dr.createStartupProbe(probePort, "/livez", corev1.URISchemeHTTPS)).
		WithReadinessProbe(dr.createReadinessProbe(probePort, "/readyz", corev1.URISchemeHTTPS)).
		WithLivenessProbe(dr.createLivenessProbe(probePort, "/livez", corev1.URISchemeHTTPS)).
		WithVolumeMounts(schedulerKubeconfigVolumeMount)
}

func (dr *APIServerResourcesReconciler) createControllerManagerContainer(
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
		WithStartupProbe(dr.createStartupProbe(probePort, "/healthz", corev1.URISchemeHTTPS)).
		WithReadinessProbe(dr.createReadinessProbe(probePort, "/healthz", corev1.URISchemeHTTPS)).
		WithLivenessProbe(dr.createLivenessProbe(probePort, "/healthz", corev1.URISchemeHTTPS)).
		WithVolumeMounts(controllerManagerCertificatesVolumeMount, controllerManagerKubeconfigVolumeMount)
}

func (dr *APIServerResourcesReconciler) buildSchedulerArgs(
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

func (dr *APIServerResourcesReconciler) buildControllerManagerArgs(
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
