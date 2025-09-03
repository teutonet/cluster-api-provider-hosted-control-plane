package reconcilers

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	operatorutil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

type PodOptions struct {
	ServiceAccountName string
	DNSPolicy          corev1.DNSPolicy
	HostNetwork        bool
	Annotations        map[string]string
	PriorityClassName  string
	Affinity           *corev1ac.AffinityApplyConfiguration
	Tolerations        []*corev1ac.TolerationApplyConfiguration
}

type ContainerOptions struct {
	Root                    bool
	ReadWriteRootFilesystem bool
	Capabilities            []corev1.Capability
}

type reconcileClient[applyConfiguration any, result any] interface {
	Apply(ctx context.Context, configuration *applyConfiguration, options metav1.ApplyOptions) (*result, error)
	Delete(ctx context.Context, name string, options metav1.DeleteOptions) error
}

func reconcile[RA any, RSA any, R any](
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	client reconcileClient[RA, R],
	kind string,
	namespace string,
	name string,
	labels map[string]string,
	createResourceApplyConfiguration func(name string, namespace string) *RA,
	createResourceSpecApplyConfiguration func() *RSA,
	setSpec func(resourceApplyConfiguration *RA, resourceSpecApplyConfiguration *RSA) *RA,
	labelFunc func(resourceApplyConfiguration *RA, labels map[string]string) *RA,
	setSpecSelector func(
		resourceSpecApplyConfiguration *RSA, labelSelector *metav1ac.LabelSelectorApplyConfiguration,
	) *RSA,
	setSpecTemplate func(resourceSpecApplyConfiguration *RSA, template *corev1ac.PodTemplateSpecApplyConfiguration) *RSA,
	podTemplateSpecApplyConfiguration *corev1ac.PodTemplateSpecApplyConfiguration,
	readinessCheck func(*R) bool,
	mutateFuncs ...func(*RA) *RA,
) (*R, bool, error) {
	labelSelector := metav1ac.LabelSelector().WithMatchLabels(labels)
	podTemplateSpecApplyConfiguration = podTemplateSpecApplyConfiguration.
		WithLabels(labels)
	podTemplateSpecApplyConfiguration, err := operatorutil.SetChecksumAnnotations(
		ctx, kubernetesClient,
		namespace, podTemplateSpecApplyConfiguration,
	)
	if err != nil {
		return nil, false, fmt.Errorf("failed to set checksum annotations on %s %s: %w", kind, name, err)
	}

	resourceApplyConfiguration := createResourceApplyConfiguration(name, namespace)
	resourceApplyConfiguration = labelFunc(resourceApplyConfiguration, labels)
	if err := operatorutil.ValidateMounts(podTemplateSpecApplyConfiguration.Spec); err != nil {
		return nil, false, fmt.Errorf(
			"%s %s has mounts without corresponding volume: %w", kind, name, err,
		)
	}

	resourceSpecApplyConfiguration := createResourceSpecApplyConfiguration()
	resourceSpecApplyConfiguration = setSpecSelector(resourceSpecApplyConfiguration, labelSelector)
	resourceSpecApplyConfiguration = setSpecTemplate(resourceSpecApplyConfiguration, podTemplateSpecApplyConfiguration)
	resourceApplyConfiguration = setSpec(resourceApplyConfiguration, resourceSpecApplyConfiguration)

	for _, mutateFunc := range mutateFuncs {
		resourceApplyConfiguration = mutateFunc(resourceApplyConfiguration)
	}

	appliedResource, err := client.Apply(ctx, resourceApplyConfiguration, operatorutil.ApplyOptions)
	if err != nil {
		if apierrors.IsInvalid(err) {
			if err := client.Delete(
				ctx,
				name,
				metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)}, // TODO: make this configurable
			); err != nil {
				return nil, false, fmt.Errorf(
					"failed to delete existing %s %s: %w", kind, name, err,
				)
			}
			return nil, false, nil
		}

		return nil, false, errorsUtil.IfErrErrorf(
			"failed to patch %s %s: %w", kind, name, err,
		)
	}

	if !readinessCheck(appliedResource) {
		return nil, false, nil
	}

	return appliedResource, true, nil
}

func reconcileDeployment(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	replicas int32,
	podTemplateSpec *corev1ac.PodTemplateSpecApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	return reconcile(
		ctx,
		kubernetesClient,
		kubernetesClient.AppsV1().Deployments(namespace),
		"Deployment",
		namespace,
		name,
		labels,
		appsv1ac.Deployment,
		appsv1ac.DeploymentSpec,
		(*appsv1ac.DeploymentApplyConfiguration).WithSpec,
		(*appsv1ac.DeploymentApplyConfiguration).WithLabels,
		(*appsv1ac.DeploymentSpecApplyConfiguration).WithSelector,
		(*appsv1ac.DeploymentSpecApplyConfiguration).WithTemplate,
		podTemplateSpec.WithSpec(podTemplateSpec.Spec.
			WithTopologySpreadConstraints(
				operatorutil.CreatePodTopologySpreadConstraints(metav1ac.LabelSelector().WithMatchLabels(labels)),
			),
		),
		isDeploymentReady,
		createDeploymentMutator(replicas),
		func(deployment *appsv1ac.DeploymentApplyConfiguration) *appsv1ac.DeploymentApplyConfiguration {
			if ownerReference != nil {
				return deployment.WithOwnerReferences(ownerReference)
			}
			return deployment
		},
	)
}

func isDeploymentReady(deployment *appsv1.Deployment) bool {
	if deployment == nil {
		return false
	}

	return arePodsReady(
		deployment.Spec.Replicas,
		deployment.Status.ReadyReplicas,
		deployment.Status.AvailableReplicas,
		deployment.Status.UpdatedReplicas,
		deployment.Status.ObservedGeneration,
		deployment.Generation,
	)
}

func createDeploymentMutator(
	replicas int32,
) func(deployment *appsv1ac.DeploymentApplyConfiguration) *appsv1ac.DeploymentApplyConfiguration {
	return func(
		deployment *appsv1ac.DeploymentApplyConfiguration,
	) *appsv1ac.DeploymentApplyConfiguration {
		return deployment.WithSpec(deployment.Spec.
			WithStrategy(appsv1ac.DeploymentStrategy().
				WithType(appsv1.RollingUpdateDeploymentStrategyType).
				WithRollingUpdate(appsv1ac.RollingUpdateDeployment().
					WithMaxSurge(intstr.FromInt32(replicas)),
				),
			).
			WithReplicas(replicas),
		)
	}
}

func reconcileStatefulset(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	labels map[string]string,
	replicas int32,
	podTemplateSpec *corev1ac.PodTemplateSpecApplyConfiguration,
	volumeClaimTemplates []*corev1ac.PersistentVolumeClaimApplyConfiguration,
	volumeClaimRetentionPolicy *appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicyApplyConfiguration,
) (*appsv1.StatefulSet, bool, error) {
	return reconcile(
		ctx,
		kubernetesClient,
		kubernetesClient.AppsV1().StatefulSets(namespace),
		"StatefulSet",
		namespace,
		name,
		labels,
		appsv1ac.StatefulSet,
		appsv1ac.StatefulSetSpec,
		(*appsv1ac.StatefulSetApplyConfiguration).WithSpec,
		(*appsv1ac.StatefulSetApplyConfiguration).WithLabels,
		(*appsv1ac.StatefulSetSpecApplyConfiguration).WithSelector,
		(*appsv1ac.StatefulSetSpecApplyConfiguration).WithTemplate,
		podTemplateSpec.WithSpec(podTemplateSpec.Spec.
			WithTopologySpreadConstraints(
				operatorutil.CreatePodTopologySpreadConstraints(metav1ac.LabelSelector().WithMatchLabels(labels)),
			),
		),
		isStatefulsetReady,
		createStatefulsetMutator(
			replicas,
			serviceName,
			podManagementPolicy,
			updateStrategy,
			volumeClaimTemplates,
			volumeClaimRetentionPolicy,
		),
		func(statefulset *appsv1ac.StatefulSetApplyConfiguration) *appsv1ac.StatefulSetApplyConfiguration {
			return statefulset.WithOwnerReferences(ownerReference)
		},
	)
}

func isStatefulsetReady(statefulset *appsv1.StatefulSet) bool {
	if statefulset == nil {
		return false
	}
	return arePodsReady(
		statefulset.Spec.Replicas,
		statefulset.Status.ReadyReplicas,
		statefulset.Status.AvailableReplicas,
		statefulset.Status.UpdatedReplicas,
		statefulset.Status.ObservedGeneration,
		statefulset.Generation,
	)
}

func arePodsReady(
	replicas *int32,
	readyReplicas int32,
	availableReplicas int32,
	updatedReplicas int32,
	observedGeneration int64,
	generation int64,
) bool {
	if replicas != nil && readyReplicas < *replicas {
		return false
	}

	if replicas != nil && availableReplicas < *replicas {
		return false
	}

	if replicas != nil && updatedReplicas < *replicas {
		return false
	}

	if observedGeneration < generation {
		return false
	}
	return true
}

func createStatefulsetMutator(
	replicas int32,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	volumeClaimTemplates []*corev1ac.PersistentVolumeClaimApplyConfiguration,
	volumeClaimRetentionPolicy *appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicyApplyConfiguration,
) func(statefulset *appsv1ac.StatefulSetApplyConfiguration) *appsv1ac.StatefulSetApplyConfiguration {
	return func(
		statefulset *appsv1ac.StatefulSetApplyConfiguration,
	) *appsv1ac.StatefulSetApplyConfiguration {
		return statefulset.WithSpec(statefulset.Spec.
			WithServiceName(serviceName).
			WithPodManagementPolicy(podManagementPolicy).
			WithUpdateStrategy(updateStrategy).
			WithVolumeClaimTemplates(volumeClaimTemplates...).
			WithPersistentVolumeClaimRetentionPolicy(volumeClaimRetentionPolicy).
			WithReplicas(replicas),
		)
	}
}

func createPodTemplateSpec(
	options PodOptions,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) *corev1ac.PodTemplateSpecApplyConfiguration {
	fsGroup := int64(1000)
	if slices.SomeBy(containers,
		func(containerSetting slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions]) bool {
			return containerSetting.B.Root
		},
	) {
		fsGroup = 0
	}
	spec := corev1ac.PodSpec().
		WithAutomountServiceAccountToken(options.ServiceAccountName != "").
		WithServiceAccountName(options.ServiceAccountName).
		WithPriorityClassName(options.PriorityClassName).
		WithEnableServiceLinks(false).
		WithTolerations(options.Tolerations...).
		WithAffinity(options.Affinity).
		WithDNSPolicy(slices.Ternary(options.DNSPolicy == "", corev1.DNSClusterFirst, options.DNSPolicy)).
		WithHostNetwork(options.HostNetwork).
		WithContainers(
			slices.Map(containers, func(
				containerSetting slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
				_ int,
			) *corev1ac.ContainerApplyConfiguration {
				container, options := containerSetting.A, containerSetting.B
				user := int64(1000)
				if options.Root {
					user = 0
				}
				return container.
					WithSecurityContext(corev1ac.SecurityContext().
						WithPrivileged(options.Root).
						WithAllowPrivilegeEscalation(options.Root).
						WithReadOnlyRootFilesystem(!options.ReadWriteRootFilesystem).
						WithRunAsUser(user).
						WithRunAsGroup(user).
						WithRunAsNonRoot(!options.Root).
						WithCapabilities(
							corev1ac.Capabilities().
								WithDrop("ALL").
								WithAdd(options.Capabilities...),
						))
			})...,
		).
		WithSecurityContext(corev1ac.PodSecurityContext().
			WithRunAsUser(1000).
			WithRunAsGroup(1000).
			WithRunAsNonRoot(true).
			WithFSGroup(fsGroup),
		).
		WithVolumes(volumes...)

	return corev1ac.PodTemplateSpec().
		WithAnnotations(options.Annotations).
		WithSpec(spec)
}
