package reconcilers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"

	slices "github.com/samber/lo"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	policyv1ac "k8s.io/client-go/applyconfigurations/policy/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

var (
	errPodDisruptionBudgetHasBothMinAvailableAndMaxUnavailable = errors.New(
		"pod disruption budget cannot have both minAvailable and maxUnavailable set",
	)
	errServiceHasBothClusterIPAndIsHeadless = errors.New(
		"service cannot be headless and have a clusterIP set",
	)
	errStatefulSetLabelChange = errors.New(
		"statefulset: cannot change pod template label values",
	)
	specUpdateForbiddenErrorMessageRegex = regexp.MustCompile(
		`Forbidden: updates to \S+ spec for fields other than .* are forbidden`,
	)
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

func reconcileWorkload[RA any, RSA any, R any](
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	client reconcileClient[RA, R],
	kind string,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	egressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	propagationPolicy metav1.DeletionPropagation,
	createResourceApplyConfiguration func(name string, namespace string) *RA,
	createResourceSpecApplyConfiguration func() *RSA,
	setOwnerReference func(
		resourceApplyConfiguration *RA, ownerReference ...*metav1ac.OwnerReferenceApplyConfiguration,
	) *RA,
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
	if err := reconcileNetworkPolicy(
		ctx,
		kubernetesClient,
		namespace,
		name,
		deleteResource,
		ownerReference,
		labels,
		ingressPortLabels,
		egressPortLabels,
	); err != nil {
		return nil, false, fmt.Errorf("failed to reconcile network policy for %s %s/%s: %w",
			kind, namespace, name, err,
		)
	}

	if deleteResource {
		if err := client.Delete(
			ctx,
			name,
			metav1.DeleteOptions{PropagationPolicy: ptr.To(propagationPolicy)},
		); err != nil && !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("failed to delete %s %s: %w", kind, name, err)
		}
	}

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
	if ownerReference != nil {
		resourceApplyConfiguration = setOwnerReference(resourceApplyConfiguration, ownerReference)
	}
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
	//nolint:nestif // need to handle special case of statefulset update forbidden error
	if err != nil {
		if status, ok := err.(apierrors.APIStatus); apierrors.IsInvalid(err) && (ok || errors.As(err, &status)) {
			if slices.EveryBy(status.Status().Details.Causes, func(cause metav1.StatusCause) bool {
				return cause.Type == metav1.CauseTypeForbidden &&
					cause.Field == "spec" &&
					specUpdateForbiddenErrorMessageRegex.MatchString(cause.Message)
			}) {
				if err := client.Delete(
					ctx,
					name,
					metav1.DeleteOptions{PropagationPolicy: ptr.To(propagationPolicy)},
				); err != nil {
					return nil, false, fmt.Errorf(
						"failed to delete existing %s %s: %w", kind, name, err,
					)
				}
				return nil, false, nil
			}
		}

		return nil, false, errorsUtil.IfErrErrorf(
			"failed to apply %s %s: %w", kind, name, err,
		)
	}

	if !readinessCheck(appliedResource) {
		return appliedResource, false, nil
	}

	return appliedResource, true, nil
}

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;patch;delete

func reconcileDeployment(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	egressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	replicas int32,
	podTemplateSpec *corev1ac.PodTemplateSpecApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	deployment, ready, err := reconcileWorkload(
		ctx,
		kubernetesClient,
		kubernetesClient.AppsV1().Deployments(namespace),
		"Deployment",
		namespace,
		name,
		deleteResource,
		ownerReference,
		labels,
		ingressPortLabels,
		egressPortLabels,
		metav1.DeletePropagationBackground,
		appsv1ac.Deployment,
		appsv1ac.DeploymentSpec,
		(*appsv1ac.DeploymentApplyConfiguration).WithOwnerReferences,
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
	if err != nil {
		return nil, false, fmt.Errorf("failed to reconcile deployment %s/%s: %w", namespace, name, err)
	}
	if !ready {
		return nil, false, nil
	}

	if err := reconcilePodDisruptionBudget(
		ctx,
		kubernetesClient,
		namespace,
		name,
		deleteResource,
		ownerReference,
		labels,
		slices.Ternary(replicas > 2, int32(1), int32(0)),
		0,
	); err != nil {
		return nil, false, fmt.Errorf("failed to reconcile pod disruption budget for deployment %s/%s: %w",
			namespace, name, err,
		)
	}

	return deployment, true, nil
}

//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=create;patch

func reconcileNetworkPolicy(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	egressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) error {
	networkPolicyInterface := kubernetesClient.NetworkingV1().NetworkPolicies(namespace)

	if deleteResource || ingressPortLabels == nil && egressPortLabels == nil {
		if err := networkPolicyInterface.Delete(ctx, name, metav1.DeleteOptions{}); err != nil &&
			!apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete network policy %s/%s: %w", namespace, name, err)
		}
		return nil
	}

	spec := networkingv1ac.NetworkPolicySpec().
		WithPodSelector(metav1ac.LabelSelector().WithMatchLabels(labels))

	if ingressPortLabels != nil {
		spec = spec.
			WithPolicyTypes(networkingv1.PolicyTypeIngress).
			WithIngress(convertToRule(
				networkingv1ac.NetworkPolicyIngressRule,
				(*networkingv1ac.NetworkPolicyIngressRuleApplyConfiguration).WithPorts,
				(*networkingv1ac.NetworkPolicyIngressRuleApplyConfiguration).WithFrom,
				ingressPortLabels,
			)...)
	}
	if egressPortLabels != nil {
		egressRules := convertToRule(
			networkingv1ac.NetworkPolicyEgressRule,
			(*networkingv1ac.NetworkPolicyEgressRuleApplyConfiguration).WithPorts,
			(*networkingv1ac.NetworkPolicyEgressRuleApplyConfiguration).WithTo,
			egressPortLabels,
		)
		egressRules = append(egressRules,
			networkingv1ac.NetworkPolicyEgressRule().
				WithPorts(networkingv1ac.NetworkPolicyPort().
					WithProtocol(corev1.ProtocolUDP).
					WithPort(intstr.FromInt32(53)),
				).
				WithTo(networkingv1ac.NetworkPolicyPeer().
					WithNamespaceSelector(metav1ac.LabelSelector().
						WithMatchLabels(map[string]string{
							corev1.LabelMetadataName: "kube-system",
						}),
					).
					WithPodSelector(metav1ac.LabelSelector().
						WithMatchLabels(map[string]string{
							"k8s-app": "kube-dns",
						}),
					),
				),
		)
		spec = spec.
			WithPolicyTypes(networkingv1.PolicyTypeEgress).
			WithEgress(egressRules...)
	}

	networkPolicyApplyConfiguration := networkingv1ac.NetworkPolicy(name, namespace).
		WithLabels(labels).
		WithSpec(spec)

	if ownerReference != nil {
		networkPolicyApplyConfiguration = networkPolicyApplyConfiguration.
			WithOwnerReferences(ownerReference)
	}

	_, err := networkPolicyInterface.
		Apply(
			ctx,
			networkPolicyApplyConfiguration,
			operatorutil.ApplyOptions,
		)
	return errorsUtil.IfErrErrorf("failed to apply network policy %s/%s: %w", namespace, name, err)
}

func sortByPort(
	portLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) []slices.Entry[int32, []*networkingv1ac.NetworkPolicyPeerApplyConfiguration] {
	portLabelMappings := slices.ToPairs(portLabels)
	sort.Slice(portLabelMappings, func(i, j int) bool {
		return portLabelMappings[i].Key < portLabelMappings[j].Key
	})
	return portLabelMappings
}

func convertToRule[AC any](
	createResourceApplyConfiguration func() *AC,
	withPorts func(applyConfiguration *AC, ports ...*networkingv1ac.NetworkPolicyPortApplyConfiguration) *AC,
	addPeers func(applyConfiguration *AC, peers ...*networkingv1ac.NetworkPolicyPeerApplyConfiguration) *AC,
	egressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) []*AC {
	return slices.Map(sortByPort(egressPortLabels), func(
		egressPortMapping slices.Entry[int32, []*networkingv1ac.NetworkPolicyPeerApplyConfiguration], _ int,
	) *AC {
		port, peerLabels := egressPortMapping.Key, egressPortMapping.Value
		applyConfiguration := createResourceApplyConfiguration()
		applyConfiguration = withPorts(
			applyConfiguration,
			networkingv1ac.NetworkPolicyPort().
				WithPort(intstr.FromInt32(port)),
		)
		applyConfiguration = addPeers(applyConfiguration, peerLabels...)
		return applyConfiguration
	})
}

//+kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=create;patch;delete

func reconcilePodDisruptionBudget(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	minAvailable int32,
	maxUnavailable int32,
) error {
	podDisruptionBudgetInterface := kubernetesClient.PolicyV1().PodDisruptionBudgets(namespace)
	if deleteResource || minAvailable == 0 && maxUnavailable == 0 {
		if err := podDisruptionBudgetInterface.
			Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod disruption budget %s/%s: %w", namespace, name, err)
		}
		return nil
	}
	if minAvailable > 0 && maxUnavailable > 0 {
		return fmt.Errorf("%s/%s: %w",
			namespace, name, errPodDisruptionBudgetHasBothMinAvailableAndMaxUnavailable,
		)
	}

	spec := policyv1ac.PodDisruptionBudgetSpec().
		WithSelector(metav1ac.LabelSelector().WithMatchLabels(labels))

	switch {
	case minAvailable > 0:
		spec = spec.WithMinAvailable(intstr.FromInt32(minAvailable))
	case maxUnavailable > 0:
		spec = spec.WithMaxUnavailable(intstr.FromInt32(maxUnavailable))
	}

	podDisruptionBudgetApplyConfiguration := policyv1ac.PodDisruptionBudget(name, namespace).
		WithLabels(labels).
		WithSpec(spec)

	if ownerReference != nil {
		podDisruptionBudgetApplyConfiguration = podDisruptionBudgetApplyConfiguration.
			WithOwnerReferences(ownerReference)
	}

	_, err := podDisruptionBudgetInterface.
		Apply(
			ctx,
			podDisruptionBudgetApplyConfiguration,
			operatorutil.ApplyOptions,
		)
	return errorsUtil.IfErrErrorf("failed to apply pod disruption budget %s/%s: %w", namespace, name, err)
}

//+kubebuilder:rbac:groups="",resources=services,verbs=create;patch;delete

func reconcileService(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	serviceType corev1.ServiceType,
	serviceIP net.IP,
	headless bool,
	ports []*corev1ac.ServicePortApplyConfiguration,
) (*corev1.Service, bool, error) {
	spec := corev1ac.ServiceSpec().
		WithType(serviceType).
		WithSelector(labels).
		WithPorts(ports...)

	if headless && serviceIP != nil {
		return nil, false, fmt.Errorf("%s/%s: %w",
			namespace, name, errServiceHasBothClusterIPAndIsHeadless,
		)
	}

	switch {
	case headless:
		spec = spec.WithClusterIP(corev1.ClusterIPNone).WithPublishNotReadyAddresses(true)
	case serviceIP != nil:
		spec = spec.WithClusterIP(serviceIP.String())
	}

	serviceApplyConfiguration := corev1ac.Service(name, namespace).
		WithLabels(labels).
		WithSpec(spec)

	if ownerReference != nil {
		serviceApplyConfiguration = serviceApplyConfiguration.
			WithOwnerReferences(ownerReference)
	}

	service, err := kubernetesClient.CoreV1().Services(namespace).
		Apply(
			ctx,
			serviceApplyConfiguration,
			operatorutil.ApplyOptions,
		)
	if err != nil {
		return nil, false, fmt.Errorf("failed to apply service %s/%s: %w", namespace, name, err)
	}

	ready := false
	switch serviceType {
	case corev1.ServiceTypeLoadBalancer:
		ready = len(service.Status.LoadBalancer.Ingress) > 0
	case corev1.ServiceTypeClusterIP:
		ready = service.Spec.ClusterIP != ""
	case corev1.ServiceTypeNodePort, corev1.ServiceTypeExternalName:
		ready = true
	}

	return service, ready, nil
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=create;patch;delete

func reconcileSecret(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	data map[string][]byte,
) error {
	secretApplyConfiguration := corev1ac.Secret(name, namespace).
		WithLabels(labels).
		WithType(corev1.SecretTypeOpaque).
		WithData(data)

	if ownerReference != nil {
		secretApplyConfiguration = secretApplyConfiguration.
			WithOwnerReferences(ownerReference)
	}

	_, err := kubernetesClient.CoreV1().Secrets(namespace).
		Apply(
			ctx,
			secretApplyConfiguration,
			operatorutil.ApplyOptions,
		)
	return errorsUtil.IfErrErrorf("failed to apply secret %s/%s: %w", namespace, name, err)
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=create;patch;delete

func reconcileConfigmap(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	data map[string]string,
) error {
	configMapApplyConfiguration := corev1ac.ConfigMap(name, namespace).
		WithLabels(labels).
		WithData(data)

	configMapInterface := kubernetesClient.CoreV1().ConfigMaps(namespace)
	if deleteResource {
		err := configMapInterface.
			Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete configmap %s/%s: %w", namespace, name, err)
		}
		return nil
	}
	if ownerReference != nil {
		configMapApplyConfiguration = configMapApplyConfiguration.
			WithOwnerReferences(ownerReference)
	}

	_, err := configMapInterface.
		Apply(
			ctx,
			configMapApplyConfiguration,
			operatorutil.ApplyOptions,
		)
	return errorsUtil.IfErrErrorf("failed to apply configmap %s/%s: %w", namespace, name, err)
}

func isDeploymentReady(deployment *appsv1.Deployment) bool {
	if deployment == nil {
		return false
	}

	return arePodsReady(
		*deployment.Spec.Replicas,
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

//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=create;get;patch;delete

func reconcileStatefulset(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	labels map[string]string,
	ingressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	egressPortLabels map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	replicas int32,
	podTemplateSpec *corev1ac.PodTemplateSpecApplyConfiguration,
	volumeClaimTemplates []*corev1ac.PersistentVolumeClaimApplyConfiguration,
	volumeClaimRetentionPolicy *appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicyApplyConfiguration,
) (*appsv1.StatefulSet, bool, error) {
	statefulSetInterface := kubernetesClient.AppsV1().StatefulSets(namespace)
	existingStatefulSetExists := true
	existingStatefulSet, err := statefulSetInterface.
		Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		existingStatefulSetExists = false
	} else if err != nil {
		return nil, false, fmt.Errorf("failed to get existing statefulset %s/%s: %w", namespace, name, err)
	}

	selectorLabels := labels

	if existingStatefulSetExists {
		existingPodLabels := existingStatefulSet.Spec.Template.Labels
		existingSelector := existingStatefulSet.Spec.Selector.MatchLabels
		mergedLabels := slices.Assign(labels, existingPodLabels)

		switch {
		case slices.Every(slices.ToPairs(existingPodLabels), slices.ToPairs(labels)):
			selectorLabels = labels
		case slices.Every(slices.ToPairs(mergedLabels), slices.ToPairs(existingSelector)):
			selectorLabels = existingSelector
			labels = mergedLabels
		case len(labels) == len(existingPodLabels):
			return nil, false, fmt.Errorf(
				"statefulset %s/%s: %w",
				namespace, name, errStatefulSetLabelChange,
			)
		}

		if !slices.ElementsMatch(
			slices.ToPairs(existingSelector),
			slices.ToPairs(selectorLabels),
		) && !isStatefulsetReady(existingStatefulSet) {
			// wait until the existing statefulset is ready before changing the selector
			return nil, false, nil
		}
	}

	statefulSet, ready, err := reconcileWorkload(
		ctx,
		kubernetesClient,
		statefulSetInterface,
		"StatefulSet",
		namespace,
		name,
		deleteResource,
		ownerReference,
		selectorLabels,
		ingressPortLabels,
		egressPortLabels,
		metav1.DeletePropagationOrphan,
		appsv1ac.StatefulSet,
		appsv1ac.StatefulSetSpec,
		(*appsv1ac.StatefulSetApplyConfiguration).WithOwnerReferences,
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
			return statefulset.
				WithSpec(statefulset.Spec.
					WithTemplate(statefulset.Spec.Template.
						WithLabels(
							labels,
						),
					),
				)
		},
	)
	if err != nil {
		return nil, false, fmt.Errorf("failed to reconcile statefulset %s/%s: %w", namespace, name, err)
	}
	if !ready {
		return nil, false, nil
	}

	if err := reconcilePodDisruptionBudget(
		ctx,
		kubernetesClient,
		namespace,
		name,
		deleteResource,
		ownerReference,
		labels,
		0,
		slices.Ternary(replicas > 1, int32(1), int32(0)),
	); err != nil {
		return nil, false, fmt.Errorf("failed to reconcile pod disruption budget for statefulset %s/%s: %w",
			namespace, name, err,
		)
	}

	return statefulSet, ready, err
}

func isStatefulsetReady(statefulset *appsv1.StatefulSet) bool {
	if statefulset == nil {
		return false
	}
	return arePodsReady(
		*statefulset.Spec.Replicas,
		statefulset.Status.ReadyReplicas,
		statefulset.Status.AvailableReplicas,
		statefulset.Status.UpdatedReplicas,
		statefulset.Status.ObservedGeneration,
		statefulset.Generation,
	) && statefulset.Status.CurrentRevision == statefulset.Status.UpdateRevision
}

func arePodsReady(
	replicas int32,
	readyReplicas int32,
	availableReplicas int32,
	updatedReplicas int32,
	observedGeneration int64,
	generation int64,
) bool {
	if readyReplicas < replicas ||
		availableReplicas < replicas ||
		updatedReplicas < replicas ||
		observedGeneration < generation {
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
	initContainers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) *corev1ac.PodTemplateSpecApplyConfiguration {
	spec := corev1ac.PodSpec().
		WithAutomountServiceAccountToken(options.ServiceAccountName != "").
		WithServiceAccountName(options.ServiceAccountName).
		WithPriorityClassName(options.PriorityClassName).
		WithEnableServiceLinks(false).
		WithTolerations(options.Tolerations...).
		WithAffinity(options.Affinity).
		WithDNSPolicy(slices.Ternary(options.DNSPolicy == "", corev1.DNSClusterFirst, options.DNSPolicy)).
		WithHostNetwork(options.HostNetwork).
		WithInitContainers(convertToContainerApplyConfiguration(initContainers)...).
		WithContainers(convertToContainerApplyConfiguration(containers)...).
		WithSecurityContext(corev1ac.PodSecurityContext().
			WithRunAsUser(1000).
			WithRunAsGroup(1000).
			WithRunAsNonRoot(true).
			WithFSGroup(1000),
		).
		WithVolumes(volumes...)

	return corev1ac.PodTemplateSpec().
		WithAnnotations(options.Annotations).
		WithSpec(spec)
}

func convertToContainerApplyConfiguration(
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
) []*corev1ac.ContainerApplyConfiguration {
	return slices.Map(containers, func(
		containerSetting slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
		_ int,
	) *corev1ac.ContainerApplyConfiguration {
		container, options := containerSetting.Unpack()
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
				WithCapabilities(corev1ac.Capabilities().
					WithDrop("ALL").
					WithAdd(options.Capabilities...),
				),
			)
	})
}
