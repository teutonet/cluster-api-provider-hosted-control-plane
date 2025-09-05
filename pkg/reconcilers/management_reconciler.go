package reconcilers

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	"k8s.io/client-go/kubernetes"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type ManagementResourceReconciler struct {
	Tracer              string
	KubernetesClient    kubernetes.Interface
	WorldComponent      string
	ControllerNamespace string
	ControllerComponent string
}

func (mr *ManagementResourceReconciler) ReconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	namespace string,
	name string,
	serviceType corev1.ServiceType,
	headless bool,
	component string,
	ports []*corev1ac.ServicePortApplyConfiguration,
) (*corev1.Service, bool, error) {
	return tracing.WithSpan3(ctx, mr.Tracer, "ReconcileService",
		func(ctx context.Context, span trace.Span) (*corev1.Service, bool, error) {
			span.SetAttributes(
				attribute.String("service.namespace", namespace),
				attribute.String("service.name", name),
			)
			return reconcileService(
				ctx,
				mr.KubernetesClient,
				namespace,
				name,
				operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				names.GetControlPlaneLabels(cluster, component),
				serviceType,
				nil,
				headless,
				ports,
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileSecret(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	namespace string,
	name string,
	data map[string][]byte,
) error {
	return tracing.WithSpan1(ctx, mr.Tracer, "ReconcileSecret",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("secret.namespace", namespace),
				attribute.String("secret.name", name),
			)
			return errorsUtil.IfErrErrorf("failed to apply secret %s/%s into management cluster: %w",
				namespace,
				name,
				reconcileSecret(
					ctx,
					mr.KubernetesClient,
					namespace,
					name,
					operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
					names.GetControlPlaneLabels(cluster, ""),
					data,
				),
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileConfigmap(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	component string,
	namespace string,
	name string,
	data map[string]string,
) error {
	return tracing.WithSpan1(ctx, mr.Tracer, "ReconcileConfigmap",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("configmap.namespace", namespace),
				attribute.String("configmap.name", name),
			)
			return errorsUtil.IfErrErrorf("failed to apply configmap %s/%s into management cluster: %w",
				namespace,
				name,
				reconcileConfigmap(
					ctx,
					mr.KubernetesClient,
					namespace,
					name,
					operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
					names.GetControlPlaneLabels(cluster, component),
					data,
				),
			)
		},
	)
}

func (mr *ManagementResourceReconciler) convertToPeerApplyConfigurations(
	portComponentMappings map[int32][]string,
	cluster *capiv1.Cluster,
) map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	if portComponentMappings == nil {
		return nil
	}
	return slices.MapEntries(portComponentMappings,
		func(port int32, components []string) (int32, []*networkingv1ac.NetworkPolicyPeerApplyConfiguration) {
			return port, slices.Map(components,
				func(component string, _ int) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
					if component == mr.ControllerComponent {
						return networkingv1ac.NetworkPolicyPeer().
							WithNamespaceSelector(metav1ac.LabelSelector().
								WithMatchLabels(map[string]string{
									corev1.LabelMetadataName: mr.ControllerNamespace,
								}),
							)
					}
					if component == mr.WorldComponent {
						return networkingv1ac.NetworkPolicyPeer().
							WithIPBlock(networkingv1ac.IPBlock().WithCIDR("0.0.0.0/0"))
					}
					return networkingv1ac.NetworkPolicyPeer().
						WithPodSelector(names.GetControlPlaneSelector(cluster, component)).
						WithNamespaceSelector(metav1ac.LabelSelector().
							WithMatchLabels(map[string]string{
								corev1.LabelMetadataName: cluster.Namespace,
							}),
						)
				},
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	replicas int32,
	priorityClassName string,
	component string,
	ingressPortComponents map[int32][]string,
	egressPortComponents map[int32][]string,
	targetComponent string,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	return tracing.WithSpan3(ctx, mr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, bool, error) {
			span.SetAttributes(
				attribute.Int("deployment.replicas", int(replicas)),
				attribute.String("deployment.component", component),
				attribute.String("deployment.targetComponent", targetComponent),
				attribute.String("deployment.priorityClass", priorityClassName),
				attribute.Int("deployment.containers.count", len(containers)),
				attribute.Int("deployment.volumes.count", len(volumes)),
			)
			podOptions := PodOptions{
				PriorityClassName: priorityClassName,
			}
			if targetComponent != "" {
				podOptions.Affinity = corev1ac.Affinity().WithPodAffinity(corev1ac.PodAffinity().
					WithPreferredDuringSchedulingIgnoredDuringExecution(corev1ac.WeightedPodAffinityTerm().
						WithWeight(100).
						WithPodAffinityTerm(corev1ac.PodAffinityTerm().
							WithLabelSelector(names.GetControlPlaneSelector(cluster, targetComponent)).
							WithTopologyKey(corev1.LabelHostname),
						),
					),
				)
			}

			return reconcileDeployment(
				ctx,
				mr.KubernetesClient,
				cluster.Namespace,
				fmt.Sprintf("%s-%s", cluster.Name, component),
				operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				names.GetControlPlaneLabels(cluster, component),
				mr.convertToPeerApplyConfigurations(ingressPortComponents, cluster),
				mr.convertToPeerApplyConfigurations(egressPortComponents, cluster),
				replicas,
				createPodTemplateSpec(podOptions, containers, volumes),
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileStatefulset(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	name string,
	namespace string,
	podOptions PodOptions,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	component string,
	ingressPortComponents map[int32][]string,
	egressPortComponents map[int32][]string,
	replicas int32,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
	volumeClaimTemplates []*corev1ac.PersistentVolumeClaimApplyConfiguration,
	volumeClaimRetentionPolicy *appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicyApplyConfiguration,
) (*appsv1.StatefulSet, bool, error) {
	return tracing.WithSpan3(ctx, mr.Tracer, "ReconcileStatefulset",
		func(ctx context.Context, span trace.Span) (*appsv1.StatefulSet, bool, error) {
			span.SetAttributes(
				attribute.String("statefulset.namespace", namespace),
				attribute.String("statefulset.name", name),
				attribute.Int("statefulset.replicas", int(replicas)),
				attribute.Int("statefulset.containers.count", len(containers)),
				attribute.Int("statefulset.volumes.count", len(volumes)),
			)
			return reconcileStatefulset(
				ctx,
				mr.KubernetesClient,
				namespace,
				name,
				operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				serviceName,
				podManagementPolicy,
				updateStrategy,
				names.GetControlPlaneLabels(cluster, component),
				mr.convertToPeerApplyConfigurations(ingressPortComponents, cluster),
				mr.convertToPeerApplyConfigurations(egressPortComponents, cluster),
				replicas,
				createPodTemplateSpec(
					podOptions,
					containers,
					volumes,
				),
				volumeClaimTemplates,
				volumeClaimRetentionPolicy,
			)
		},
	)
}
