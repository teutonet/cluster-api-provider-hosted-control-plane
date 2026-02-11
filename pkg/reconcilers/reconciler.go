package reconcilers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"

	cilium "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	ciliuminterfacev2 "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned/typed/cilium.io/v2"
	ciliummetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	"github.com/cilium/cilium/pkg/policy/api"
	slices "github.com/samber/lo"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	policyv1ac "k8s.io/client-go/applyconfigurations/policy/v1"
	"k8s.io/client-go/kubernetes"
	networkininterface "k8s.io/client-go/kubernetes/typed/networking/v1"
	"k8s.io/utils/ptr"
)

var (
	errServiceHasBothClusterIPAndIsHeadless = errors.New(
		"service cannot be headless and have a clusterIP set",
	)
	errStatefulSetLabelChange = errors.New(
		"statefulset: cannot change pod template label values",
	)
	workloadSpecUpdateForbiddenErrorMessageRegex = regexp.MustCompile(
		`Forbidden: updates to \S+ spec for fields other than .* are forbidden`,
	)
	secretTypeImmutableErrorMessageRegex = regexp.MustCompile(
		`Invalid value: "\S+": field is immutable`,
	)
)

type PodDisruptionBudgetType = string

var (
	PodDisruptionBudgetTypeMinAvailable   PodDisruptionBudgetType = "MinAvailable"
	PodDisruptionBudgetTypeMaxUnavailable PodDisruptionBudgetType = "MaxUnavailable"
)

type PodDisruptionBudgetMode struct {
	Type  PodDisruptionBudgetType
	Value int32
}

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
	ciliumClient ciliumclient.Interface,
	client reconcileClient[RA, R],
	getObject func(*R) runtime.Object,
	apiVersion string,
	kind string,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
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
	getUID func(resource *R) types.UID,
	podTemplateSpecApplyConfiguration *corev1ac.PodTemplateSpecApplyConfiguration,
	readinessCheck func(*R) bool,
	mutateFuncs ...func(*RA) *RA,
) (*R, bool, error) {
	if deleteResource {
		if err := client.Delete(
			ctx,
			name,
			metav1.DeleteOptions{PropagationPolicy: ptr.To(propagationPolicy)},
		); err != nil && !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("failed to delete %s %s: %w", kind, name, err)
		}
		return nil, true, nil
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
	if err != nil {
		if status, ok := err.(apierrors.APIStatus); apierrors.IsInvalid(err) && (ok || errors.As(err, &status)) &&
			slices.EveryBy(status.Status().Details.Causes, func(cause metav1.StatusCause) bool {
				return cause.Type == metav1.CauseTypeForbidden &&
					cause.Field == "spec" &&
					workloadSpecUpdateForbiddenErrorMessageRegex.MatchString(cause.Message)
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
			recorder.FromContext(ctx).Warnf(
				getObject(appliedResource),
				"ImmutableSpecField",
				fmt.Sprintf("Deleted%s", kind),
				"Deleted existing %s %s due to immutable spec fields", kind, name,
			)
			// don't retry immediately, the funcs might not be idempotent
			// (and go can't figure out the generics anyways...)
			return nil, false, nil
		}

		return nil, false, fmt.Errorf(
			"failed to apply %s %s: %w", kind, name, err,
		)
	}

	if err := reconcileNetworkPolicy(
		ctx,
		kubernetesClient,
		ciliumClient,
		namespace,
		name,
		metav1ac.OwnerReference().
			WithAPIVersion(apiVersion).
			WithKind(kind).
			WithName(name).
			WithUID(getUID(appliedResource)).
			WithController(true).
			WithBlockOwnerDeletion(true),
		labels,
		ingressPolicyTargets,
		egressPolicyTargets,
	); err != nil {
		return nil, false, fmt.Errorf("failed to reconcile network policy for %s %s/%s: %w",
			kind, namespace, name, err,
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
	ciliumClient ciliumclient.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
	replicas int32,
	podTemplateSpec *corev1ac.PodTemplateSpecApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	kind := "Deployment"
	deployment, ready, err := reconcileWorkload(
		ctx,
		kubernetesClient,
		ciliumClient,
		kubernetesClient.AppsV1().Deployments(namespace),
		func(deployment *appsv1.Deployment) runtime.Object { return deployment },
		appsv1.SchemeGroupVersion.String(),
		kind,
		namespace,
		name,
		deleteResource,
		ownerReference,
		labels,
		ingressPolicyTargets,
		egressPolicyTargets,
		metav1.DeletePropagationBackground,
		appsv1ac.Deployment,
		appsv1ac.DeploymentSpec,
		(*appsv1ac.DeploymentApplyConfiguration).WithOwnerReferences,
		(*appsv1ac.DeploymentApplyConfiguration).WithSpec,
		(*appsv1ac.DeploymentApplyConfiguration).WithLabels,
		(*appsv1ac.DeploymentSpecApplyConfiguration).WithSelector,
		(*appsv1ac.DeploymentSpecApplyConfiguration).WithTemplate,
		(*appsv1.Deployment).GetUID,
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
		metav1ac.OwnerReference().
			WithAPIVersion(appsv1.SchemeGroupVersion.String()).
			WithKind(kind).
			WithName(deployment.Name).
			WithUID(deployment.UID).
			WithController(true).
			WithBlockOwnerDeletion(true),
		labels,
		PodDisruptionBudgetMode{
			Value: slices.Ternary(replicas > 1, int32(1), int32(0)),
			Type:  PodDisruptionBudgetTypeMinAvailable,
		},
	); err != nil {
		return nil, false, fmt.Errorf("failed to reconcile pod disruption budget for deployment %s/%s: %w",
			namespace, name, err,
		)
	}

	return deployment, true, nil
}

func reconcileNetworkPolicy(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	ciliumClient ciliumclient.Interface,
	namespace string,
	name string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
) error {
	return tracing.WithSpan1(ctx, "networkPolicy", "reconcileNetworkPolicy",
		func(ctx context.Context, span trace.Span) error {
			networkPolicyInterface := kubernetesClient.NetworkingV1().NetworkPolicies(namespace)
			var ciliumNetworkPolicyInterface ciliuminterfacev2.CiliumNetworkPolicyInterface
			deleteResource := ingressPolicyTargets == nil && egressPolicyTargets == nil

			if ciliumClient != nil {
				ciliumNetworkPolicyInterface = ciliumClient.CiliumV2().CiliumNetworkPolicies(namespace)
			}

			if deleteResource || ciliumClient != nil {
				if err := networkPolicyInterface.Delete(ctx, name, metav1.DeleteOptions{}); err != nil &&
					!apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete network policy %s/%s: %w", namespace, name, err)
				}
				if deleteResource {
					if err := ciliumNetworkPolicyInterface.Delete(ctx, name, metav1.DeleteOptions{}); err != nil &&
						!apierrors.IsNotFound(err) {
						return fmt.Errorf("failed to delete cilium network policy %s/%s: %w", namespace, name, err)
					}
					return nil
				}
			}

			if ciliumClient != nil {
				return reconcileCiliumNetworkPolicy(
					ctx,
					ciliumNetworkPolicyInterface,
					name,
					namespace,
					ownerReference,
					labels,
					ingressPolicyTargets,
					egressPolicyTargets,
				)
			} else {
				return reconcileKubernetesNetworkPolicy(
					ctx,
					networkPolicyInterface,
					name,
					namespace,
					ownerReference,
					labels,
					ingressPolicyTargets,
					egressPolicyTargets,
				)
			}
		},
	)
}

//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=create;patch;delete

func reconcileKubernetesNetworkPolicy(
	ctx context.Context,
	networkPolicyInterface networkininterface.NetworkPolicyInterface,
	name string,
	namespace string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
) error {
	return tracing.WithSpan1(ctx, "networkPolicy", "reconcileKubernetesNetworkPolicy",
		func(ctx context.Context, span trace.Span) error {
			spec := networkingv1ac.NetworkPolicySpec().
				WithPodSelector(metav1ac.LabelSelector().WithMatchLabels(labels))

			if ingressPolicyTargets != nil {
				spec = spec.
					WithPolicyTypes(networkingv1.PolicyTypeIngress).
					WithIngress(convertToRules(
						networkingv1ac.NetworkPolicyIngressRule,
						networkPolicyPortApplyConfiguration,
						networkPolicyIngressPeerApplyConfiguration,
						(*networkingv1ac.NetworkPolicyIngressRuleApplyConfiguration).WithPorts,
						(*networkingv1ac.NetworkPolicyIngressRuleApplyConfiguration).WithFrom,
						ingressPolicyTargets,
					)...)
			}
			if egressPolicyTargets != nil {
				egressRules := convertToRules(
					networkingv1ac.NetworkPolicyEgressRule,
					networkPolicyPortApplyConfiguration,
					networkPolicyEgressPeerApplyConfiguration,
					(*networkingv1ac.NetworkPolicyEgressRuleApplyConfiguration).WithPorts,
					(*networkingv1ac.NetworkPolicyEgressRuleApplyConfiguration).WithTo,
					egressPolicyTargets,
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
		},
	)
}

//+kubebuilder:rbac:groups=cilium.io,resources=ciliumnetworkpolicies,verbs=get;create;update;delete

//nolint:funlen // large function due to conversion logic, no big deal
func reconcileCiliumNetworkPolicy(
	ctx context.Context,
	ciliumNetworkPolicyInterface ciliuminterfacev2.CiliumNetworkPolicyInterface,
	name string,
	namespace string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
) error {
	return tracing.WithSpan1(ctx, "networkPolicy", "reconcileCiliumNetworkPolicy",
		func(ctx context.Context, span trace.Span) error {
			spec := &api.Rule{
				EndpointSelector: api.EndpointSelector{
					LabelSelector: &ciliummetav1.LabelSelector{
						MatchLabels: labels,
					},
				},
			}

			if ingressPolicyTargets != nil {
				spec.Ingress = slices.FromSlicePtr(convertToRules(
					func() *api.IngressRule {
						return &api.IngressRule{}
					},
					func(port int32) *api.PortRule {
						return &api.PortRule{
							Ports: []api.PortProtocol{
								{
									Port: strconv.Itoa(int(port)),
								},
							},
						}
					},
					func(networkPolicyTarget networkpolicy.IngressNetworkPolicyTarget) *networkpolicy.CiliumPolicyPeer {
						return networkPolicyTarget.ApplyToCiliumIngressNetworkPolicy(&networkpolicy.CiliumPolicyPeer{})
					},
					func(applyConfiguration *api.IngressRule, ports ...*api.PortRule) *api.IngressRule {
						applyConfiguration.ToPorts = slices.FromSlicePtr(ports)
						return applyConfiguration
					},
					func(applyConfiguration *api.IngressRule, peers ...*networkpolicy.CiliumPolicyPeer) *api.IngressRule {
						applyConfiguration.FromEndpoints = append(
							applyConfiguration.FromEndpoints,
							slices.FlatMap(peers,
								func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.EndpointSelector {
									return peer.Endpoints
								},
							)...)
						applyConfiguration.FromCIDR = append(applyConfiguration.FromCIDR, slices.FlatMap(peers,
							func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.CIDR {
								return peer.CIDR
							},
						)...)
						applyConfiguration.FromEntities = append(applyConfiguration.FromEntities, slices.FlatMap(peers,
							func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.Entity {
								return peer.Identities
							},
						)...)
						return applyConfiguration
					},
					ingressPolicyTargets,
				))
			}

			if egressPolicyTargets != nil {
				egressRules := convertToRules(
					func() *api.EgressRule {
						return &api.EgressRule{}
					},
					func(port int32) *api.PortRule {
						return &api.PortRule{
							Ports: []api.PortProtocol{
								{
									Port: strconv.Itoa(int(port)),
								},
							},
						}
					},
					func(networkPolicyTarget networkpolicy.EgressNetworkPolicyTarget) *networkpolicy.CiliumPolicyPeer {
						return networkPolicyTarget.ApplyToCiliumEgressNetworkPolicy(&networkpolicy.CiliumPolicyPeer{})
					},
					func(applyConfiguration *api.EgressRule, ports ...*api.PortRule) *api.EgressRule {
						applyConfiguration.ToPorts = slices.FromSlicePtr(ports)
						return applyConfiguration
					},
					func(applyConfiguration *api.EgressRule, peers ...*networkpolicy.CiliumPolicyPeer) *api.EgressRule {
						applyConfiguration.ToEndpoints = append(applyConfiguration.ToEndpoints, slices.FlatMap(peers,
							func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.EndpointSelector {
								return peer.Endpoints
							},
						)...)
						applyConfiguration.ToCIDR = append(applyConfiguration.ToCIDR, slices.FlatMap(peers,
							func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.CIDR {
								return peer.CIDR
							},
						)...)
						applyConfiguration.ToFQDNs = append(applyConfiguration.ToFQDNs, slices.FlatMap(peers,
							func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.FQDNSelector {
								return slices.Map(peer.FQDNs, func(fqdn string, _ int) api.FQDNSelector {
									return api.FQDNSelector{
										MatchName: fqdn,
									}
								})
							},
						)...)
						applyConfiguration.ToEntities = append(applyConfiguration.ToEntities, slices.FlatMap(peers,
							func(peer *networkpolicy.CiliumPolicyPeer, _ int) []api.Entity {
								return peer.Identities
							},
						)...)
						return applyConfiguration
					},
					egressPolicyTargets,
				)

				egressRules = append(egressRules,
					&api.EgressRule{
						ToPorts: []api.PortRule{
							{
								Ports: []api.PortProtocol{
									{
										Port:     "53",
										Protocol: "UDP",
									},
								},
								Rules: &api.L7Rules{ // Always record DNS requests in Cilium for FQDN policies and visibility.
									DNS: []api.PortRuleDNS{
										{
											MatchPattern: "*",
										},
									},
								},
							},
						},
						EgressCommonRule: api.EgressCommonRule{
							ToEndpoints: []api.EndpointSelector{
								{
									LabelSelector: &ciliummetav1.LabelSelector{
										MatchLabels: map[string]ciliummetav1.MatchLabelsValue{
											"k8s-app":                "kube-dns",
											cilium.PodNamespaceLabel: "kube-system",
										},
									},
								},
							},
						},
					},
				)

				spec.Egress = slices.FromSlicePtr(egressRules)
			}

			ciliumNetworkPolicyApplyConfiguration := &ciliumv2.CiliumNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
					Labels:    labels,
				},
				Spec: spec,
			}

			if ownerReference != nil {
				ciliumNetworkPolicyApplyConfiguration.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion:         *ownerReference.APIVersion,
						Kind:               *ownerReference.Kind,
						Name:               *ownerReference.Name,
						UID:                *ownerReference.UID,
						Controller:         ownerReference.Controller,
						BlockOwnerDeletion: ownerReference.BlockOwnerDeletion,
					},
				}
			}

			ciliumNetworkPolicy, err := ciliumNetworkPolicyInterface.Get(ctx,
				ciliumNetworkPolicyApplyConfiguration.Name,
				metav1.GetOptions{},
			)
			exists := !apierrors.IsNotFound(err)
			if err != nil && exists {
				return fmt.Errorf("failed to get cilium network policy %s/%s: %w", namespace, name, err)
			}

			if !exists {
				ciliumNetworkPolicy, err = ciliumNetworkPolicyInterface.
					Create(
						ctx,
						ciliumNetworkPolicyApplyConfiguration,
						metav1.CreateOptions{FieldManager: operatorutil.ApplyOptions.FieldManager},
					)
			} else {
				ciliumNetworkPolicyApplyConfiguration.ResourceVersion = ciliumNetworkPolicy.ResourceVersion
				ciliumNetworkPolicy, err = ciliumNetworkPolicyInterface.
					Update(
						ctx,
						ciliumNetworkPolicyApplyConfiguration,
						metav1.UpdateOptions{FieldManager: operatorutil.ApplyOptions.FieldManager},
					)
			}

			if err != nil {
				return fmt.Errorf("failed to create or update cilium network policy %s/%s: %w", namespace, name, err)
			}

			if invalidConditions := slices.Filter(ciliumNetworkPolicy.Status.Conditions,
				func(condition ciliumv2.NetworkPolicyCondition, _ int) bool {
					return condition.Type == ciliumv2.PolicyConditionValid && condition.Status == corev1.ConditionFalse
				}); len(invalidConditions) > 0 {
				return fmt.Errorf("cilium network policy %s/%s is invalid: %w", namespace, name,
					errors.Join(slices.Map(invalidConditions, func(condition ciliumv2.NetworkPolicyCondition,
						_ int,
					) error {
						//nolint:err113 // we don't get a real error from the condition, therefore we create one here
						return errors.New(condition.Message)
					})...),
				)
			}
			return nil
		},
	)
}

func sortByPort[NPT any](
	portLabels map[int32][]NPT,
) []slices.Entry[int32, []NPT] {
	portLabelMappings := slices.ToPairs(portLabels)
	sort.Slice(portLabelMappings, func(i, j int) bool {
		return portLabelMappings[i].Key < portLabelMappings[j].Key
	})
	return portLabelMappings
}

func networkPolicyIngressPeerApplyConfiguration(
	networkPolicyTarget networkpolicy.IngressNetworkPolicyTarget,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return networkPolicyTarget.ApplyToKubernetesIngressNetworkPolicy(networkingv1ac.NetworkPolicyPeer())
}

func networkPolicyEgressPeerApplyConfiguration(
	networkPolicyTarget networkpolicy.EgressNetworkPolicyTarget,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return networkPolicyTarget.ApplyToKubernetesEgressNetworkPolicy(networkingv1ac.NetworkPolicyPeer())
}

func networkPolicyPortApplyConfiguration(port int32) *networkingv1ac.NetworkPolicyPortApplyConfiguration {
	return networkingv1ac.NetworkPolicyPort().
		WithPort(intstr.FromInt32(port))
}

func convertToRules[AC any, PORT any, PEER any, NPT interface{}](
	createResourceApplyConfiguration func() *AC,
	createPortApplyConfiguration func(port int32) *PORT,
	createPeerApplyConfiguration func(networkPolicyTarget NPT) *PEER,
	withPorts func(applyConfiguration *AC, ports ...*PORT) *AC,
	addPeers func(applyConfiguration *AC, peers ...*PEER) *AC,
	portNetworkPolicyTargets map[int32][]NPT,
) []*AC {
	return slices.FlatMap(sortByPort(portNetworkPolicyTargets), func(
		portMapping slices.Entry[int32, []NPT], _ int,
	) []*AC {
		port, networkPolicyTargets := portMapping.Key, portMapping.Value
		return slices.Map(networkPolicyTargets, func(networkPolicyTarget NPT, _ int) *AC {
			applyConfiguration := createResourceApplyConfiguration()
			if port != 0 { // allow all ports
				applyConfiguration = withPorts(
					applyConfiguration,
					createPortApplyConfiguration(port),
				)
			}
			applyConfiguration = addPeers(applyConfiguration, createPeerApplyConfiguration(networkPolicyTarget))
			return applyConfiguration
		})
	})
}

//+kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=create;patch;delete

func reconcilePodDisruptionBudget(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	name string,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	mode PodDisruptionBudgetMode,
) error {
	podDisruptionBudgetInterface := kubernetesClient.PolicyV1().PodDisruptionBudgets(namespace)
	if mode.Value == 0 {
		if err := podDisruptionBudgetInterface.
			Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod disruption budget %s/%s: %w", namespace, name, err)
		}
		return nil
	}

	spec := policyv1ac.PodDisruptionBudgetSpec().
		WithSelector(metav1ac.LabelSelector().WithMatchLabels(labels))

	value := intstr.FromInt32(mode.Value)
	switch mode.Type {
	case PodDisruptionBudgetTypeMinAvailable:
		spec = spec.WithMinAvailable(value)
	case PodDisruptionBudgetTypeMaxUnavailable:
		spec = spec.WithMaxUnavailable(value)
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
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	labels map[string]string,
	data map[string][]byte,
	secretType corev1.SecretType,
) error {
	secretApplyConfiguration := corev1ac.Secret(name, namespace).
		WithLabels(labels).
		WithData(data).
		WithType(secretType)

	secretInterface := kubernetesClient.CoreV1().Secrets(namespace)
	if deleteResource {
		err := secretInterface.
			Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete secret %s/%s: %w", namespace, name, err)
		}
		return nil
	}

	if ownerReference != nil {
		secretApplyConfiguration = secretApplyConfiguration.
			WithOwnerReferences(ownerReference)
	}

	appliedSecret, err := secretInterface.
		Apply(
			ctx,
			secretApplyConfiguration,
			operatorutil.ApplyOptions,
		)
	if err != nil {
		if status, ok := err.(apierrors.APIStatus); apierrors.IsInvalid(err) && (ok || errors.As(err, &status)) &&
			slices.EveryBy(status.Status().Details.Causes, func(cause metav1.StatusCause) bool {
				return cause.Type == metav1.CauseTypeFieldValueInvalid &&
					cause.Field == "type" &&
					secretTypeImmutableErrorMessageRegex.MatchString(cause.Message)
			}) {
			if err := secretInterface.Delete(
				ctx,
				name,
				metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationBackground)},
			); err != nil {
				return fmt.Errorf(
					"failed to delete existing secret %s: %w", name, err,
				)
			}
			recorder.FromContext(ctx).Normalf(
				appliedSecret,
				"ImmutableTypeField",
				"DeletedSecret",
				"Deleted existing secret %s/%s due to immutable type field", namespace, name,
			)
			return reconcileSecret(
				ctx,
				kubernetesClient,
				namespace,
				name,
				deleteResource,
				ownerReference,
				labels,
				data,
				secretType,
			)
		}
		return fmt.Errorf("failed to apply secret %s/%s: %w", namespace, name, err)
	}
	return nil
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
	ciliumClient ciliumclient.Interface,
	namespace string,
	name string,
	deleteResource bool,
	ownerReference *metav1ac.OwnerReferenceApplyConfiguration,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
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

	kind := "StatefulSet"
	statefulSet, ready, err := reconcileWorkload(
		ctx,
		kubernetesClient,
		ciliumClient,
		statefulSetInterface,
		func(statefulSet *appsv1.StatefulSet) runtime.Object { return statefulSet },
		appsv1.SchemeGroupVersion.String(),
		kind,
		namespace,
		name,
		deleteResource,
		ownerReference,
		selectorLabels,
		ingressPolicyTargets,
		egressPolicyTargets,
		metav1.DeletePropagationOrphan,
		appsv1ac.StatefulSet,
		appsv1ac.StatefulSetSpec,
		(*appsv1ac.StatefulSetApplyConfiguration).WithOwnerReferences,
		(*appsv1ac.StatefulSetApplyConfiguration).WithSpec,
		(*appsv1ac.StatefulSetApplyConfiguration).WithLabels,
		(*appsv1ac.StatefulSetSpecApplyConfiguration).WithSelector,
		(*appsv1ac.StatefulSetSpecApplyConfiguration).WithTemplate,
		(*appsv1.StatefulSet).GetUID,
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
		metav1ac.OwnerReference().
			WithAPIVersion(appsv1.SchemeGroupVersion.String()).
			WithKind(kind).
			WithName(statefulSet.Name).
			WithUID(statefulSet.UID).
			WithController(true).
			WithBlockOwnerDeletion(true),
		labels,
		PodDisruptionBudgetMode{
			Value: slices.Ternary(replicas > 1, int32(1), int32(0)),
			Type:  PodDisruptionBudgetTypeMaxUnavailable,
		},
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
