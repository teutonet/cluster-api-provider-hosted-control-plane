package reconcilers

import (
	"context"
	"fmt"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

var (
	ErrResourceRecreateRequired = fmt.Errorf("resource requires recreation: %w", operatorutil.ErrRequeueRequired)
	ErrResourceNotReady         = fmt.Errorf("resource is not ready: %w", operatorutil.ErrRequeueRequired)
)

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
) (*R, error) {
	labelSelector := metav1ac.LabelSelector().WithMatchLabels(labels)
	podTemplateSpecApplyConfiguration = podTemplateSpecApplyConfiguration.
		WithLabels(labels)
	podTemplateSpecApplyConfiguration, err := operatorutil.SetChecksumAnnotations(
		ctx, kubernetesClient,
		namespace, podTemplateSpecApplyConfiguration,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to set checksum annotations on %s %s: %w", kind, name, err)
	}

	resourceApplyConfiguration := createResourceApplyConfiguration(name, namespace)
	resourceApplyConfiguration = labelFunc(resourceApplyConfiguration, labels)
	if err := operatorutil.ValidateMounts(podTemplateSpecApplyConfiguration.Spec); err != nil {
		return nil, fmt.Errorf(
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
				metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationOrphan)},
			); err != nil {
				return nil, fmt.Errorf(
					"failed to delete existing %s %s: %w", kind, name, err,
				)
			}
			return nil, ErrResourceRecreateRequired
		}

		return nil, errorsUtil.IfErrErrorf(
			"failed to patch %s %s: %w", kind, name, err,
		)
	}

	if !readinessCheck(appliedResource) {
		return nil, ErrResourceNotReady
	}

	return appliedResource, nil
}

func reconcileDeployment(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	labels map[string]string,
	replicas int32,
	podTemplateSpec *corev1ac.PodTemplateSpecApplyConfiguration,
) (*appsv1.Deployment, error) {
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
	)
}

func createDeploymentMutator(
	replicas int32,
) func(deployment *appsv1ac.DeploymentApplyConfiguration) *appsv1ac.DeploymentApplyConfiguration {
	return func(
		deployment *appsv1ac.DeploymentApplyConfiguration,
	) *appsv1ac.DeploymentApplyConfiguration {
		return deployment.
			WithSpec(deployment.Spec.
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

func createPodTemplateSpec(
	serviceAccountName string,
	priorityClassName string,
	tolerations []*corev1ac.TolerationApplyConfiguration,
	containers []*corev1ac.ContainerApplyConfiguration,
	volumes []*corev1ac.VolumeApplyConfiguration,
	mutateFuncs ...func(*corev1ac.PodSpecApplyConfiguration) *corev1ac.PodSpecApplyConfiguration,
) *corev1ac.PodTemplateSpecApplyConfiguration {
	spec := corev1ac.PodSpec().
		WithAutomountServiceAccountToken(serviceAccountName != "").
		WithServiceAccountName(serviceAccountName).
		WithPriorityClassName(priorityClassName).
		WithEnableServiceLinks(false).
		WithTolerations(tolerations...).
		WithContainers(containers...).
		WithVolumes(volumes...)

	for _, mutateFunc := range mutateFuncs {
		spec = mutateFunc(spec)
	}

	return corev1ac.PodTemplateSpec().
		WithSpec(spec)
}
