package reconcilers

import (
	"context"
	"fmt"

	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type ManagementResourceReconciler struct {
	Tracer                  string
	ManagementClusterClient *alias.ManagementClusterClient
	CiliumClient            ciliumclient.Interface
	WorldComponent          string
	ControllerNamespace     string
	ControllerComponent     string
}

func (mr *ManagementResourceReconciler) ReconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
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
			service, ready, err := reconcileService(
				ctx,
				mr.ManagementClusterClient,
				namespace,
				name,
				operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				names.GetControlPlaneLabels(cluster, component),
				serviceType,
				nil,
				headless,
				ports,
			)
			if err != nil {
				return nil, false, fmt.Errorf("failed to reconcile service %s/%s into management cluster: %w",
					namespace,
					name,
					err,
				)
			}
			return service, ready, err
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileSecret(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	component string,
	namespace string,
	name string,
	deleteResource bool,
	data map[string][]byte,
) error {
	return tracing.WithSpan1(ctx, mr.Tracer, "ReconcileSecret",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("secret.namespace", namespace),
				attribute.String("secret.name", name),
			)
			return errorsUtil.IfErrErrorf("failed to reconcile secret %s/%s into management cluster: %w",
				namespace,
				name,
				reconcileSecret(
					ctx,
					mr.ManagementClusterClient,
					namespace,
					name,
					deleteResource,
					operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
					names.GetControlPlaneLabels(cluster, component),
					data,
				),
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileConfigmap(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
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
			return errorsUtil.IfErrErrorf("failed to reconcile configmap %s/%s into management cluster: %w",
				namespace,
				name,
				reconcileConfigmap(
					ctx,
					mr.ManagementClusterClient,
					namespace,
					name,
					false,
					operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
					names.GetControlPlaneLabels(cluster, component),
					data,
				),
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	replicas int32,
	priorityClassName string,
	component string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
	targetComponent string,
	initContainers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	return tracing.WithSpan3(ctx, mr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, bool, error) {
			span.SetAttributes(
				attribute.Int("deployment.replicas", int(replicas)),
				attribute.String("deployment.component", component),
				attribute.String("deployment.targetComponent", targetComponent),
				attribute.Int("deployment.initContainers.count", len(initContainers)),
				attribute.Int("deployment.containers.count", len(containers)),
				attribute.Int("deployment.volumes.count", len(volumes)),
			)
			podOptions := PodOptions{
				PriorityClassName: priorityClassName,
			}
			span.SetAttributes(
				attribute.String("deployment.priorityClass", podOptions.PriorityClassName),
			)
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

			name := fmt.Sprintf("%s-%s", cluster.Name, component)
			deployment, ready, err := reconcileDeployment(
				ctx,
				mr.ManagementClusterClient,
				mr.CiliumClient,
				cluster.Namespace,
				name,
				false,
				operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				names.GetControlPlaneLabels(cluster, component),
				ingressPolicyTargets,
				egressPolicyTargets,
				replicas,
				createPodTemplateSpec(podOptions, initContainers, containers, volumes),
			)
			if err != nil {
				return nil, false, fmt.Errorf("failed to reconcile deployment %s/%s into management cluster: %w",
					cluster.Namespace,
					name,
					err,
				)
			}
			return deployment, ready, err
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileStatefulset(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	name string,
	namespace string,
	podOptions PodOptions,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	component string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
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
			statefulset, ready, err := reconcileStatefulset(
				ctx,
				mr.ManagementClusterClient,
				mr.CiliumClient,
				namespace,
				name,
				false,
				operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				serviceName,
				podManagementPolicy,
				updateStrategy,
				names.GetControlPlaneLabels(cluster, component),
				ingressPolicyTargets,
				egressPolicyTargets,
				replicas,
				createPodTemplateSpec(
					podOptions,
					nil,
					containers,
					volumes,
				),
				volumeClaimTemplates,
				volumeClaimRetentionPolicy,
			)
			if err != nil {
				return nil, false, fmt.Errorf("failed to reconcile statefulset %s/%s into management cluster: %w",
					namespace,
					name,
					err,
				)
			}
			return statefulset, ready, err
		},
	)
}
