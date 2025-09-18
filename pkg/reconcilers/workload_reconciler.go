package reconcilers

import (
	"context"
	"net"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
)

type WorkloadResourceReconciler struct {
	Tracer           string
	KubernetesClient alias.WorkloadClusterClient
}

func (wr *WorkloadResourceReconciler) convertToPeerApplyConfigurations(
	ingressPortComponents map[int32][]map[string]string,
) map[int32][]*networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	if ingressPortComponents == nil {
		return nil
	}
	return slices.MapEntries(
		ingressPortComponents,
		func(port int32, labelsSelectors []map[string]string) (int32, []*networkingv1ac.NetworkPolicyPeerApplyConfiguration) {
			return port, slices.Map(labelsSelectors,
				func(labelSelector map[string]string, _ int) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
					return networkingv1ac.NetworkPolicyPeer().
						WithPodSelector(metav1ac.LabelSelector().WithMatchLabels(labelSelector))
				},
			)
		},
	)
}

func (wr *WorkloadResourceReconciler) ReconcileService(
	ctx context.Context,
	namespace string,
	name string,
	labels map[string]string,
	serviceType corev1.ServiceType,
	serviceIP net.IP,
	headless bool,
	ports []*corev1ac.ServicePortApplyConfiguration,
) (*corev1.Service, bool, error) {
	return tracing.WithSpan3(ctx, wr.Tracer, "ReconcileService",
		func(ctx context.Context, span trace.Span) (*corev1.Service, bool, error) {
			span.SetAttributes(
				attribute.String("service.namespace", namespace),
				attribute.String("service.name", name),
			)
			return reconcileService(
				ctx,
				wr.KubernetesClient,
				namespace,
				name,
				nil,
				labels,
				serviceType,
				serviceIP,
				headless,
				ports,
			)
		},
	)
}

func (wr *WorkloadResourceReconciler) ReconcileConfigmap(
	ctx context.Context,
	namespace string,
	name string,
	deleteResource bool,
	labels map[string]string,
	data map[string]string,
) error {
	return tracing.WithSpan1(ctx, wr.Tracer, "ReconcileConfigmap",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("configmap.namespace", namespace),
				attribute.String("configmap.name", name),
			)
			return errorsUtil.IfErrErrorf("failed to apply configmap %s/%s into workloadcluster: %w",
				namespace,
				name,
				reconcileConfigmap(
					ctx,
					wr.KubernetesClient,
					namespace,
					name,
					deleteResource,
					nil,
					labels,
					data,
				),
			)
		},
	)
}

func (wr *WorkloadResourceReconciler) ReconcileDeployment(
	ctx context.Context,
	name string,
	namespace string,
	deleteResource bool,
	replicas int32,
	podOptions PodOptions,
	labels map[string]string,
	ingressPortLabels map[int32][]map[string]string,
	egressPortLabels map[int32][]map[string]string,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	return tracing.WithSpan3(ctx, wr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, bool, error) {
			span.SetAttributes(
				attribute.String("deployment.namespace", namespace),
				attribute.String("deployment.name", name),
				attribute.Int("deployment.replicas", int(replicas)),
				attribute.Int("deployment.containers.count", len(containers)),
				attribute.Int("deployment.volumes.count", len(volumes)),
			)
			return reconcileDeployment(
				ctx,
				wr.KubernetesClient,
				namespace,
				name,
				deleteResource,
				nil,
				labels,
				wr.convertToPeerApplyConfigurations(ingressPortLabels),
				wr.convertToPeerApplyConfigurations(egressPortLabels),
				replicas,
				createPodTemplateSpec(podOptions, containers, volumes),
			)
		},
	)
}

func (wr *WorkloadResourceReconciler) ReconcileDaemonSet(
	ctx context.Context,
	namespace string,
	name string,
	deleteResource bool,
	podOptions PodOptions,
	labels map[string]string,
	ingressPortLabels map[int32][]map[string]string,
	egressPortLabels map[int32][]map[string]string,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.DaemonSet, bool, error) {
	return tracing.WithSpan3(ctx, wr.Tracer, "ReconcileDaemonSet",
		func(ctx context.Context, span trace.Span) (*appsv1.DaemonSet, bool, error) {
			span.SetAttributes(
				attribute.String("daemonset.namespace", namespace),
				attribute.String("daemonset.name", name),
				attribute.Int("daemonset.containers.count", len(containers)),
				attribute.Int("daemonset.volumes.count", len(volumes)),
			)
			return reconcileWorkload(
				ctx,
				wr.KubernetesClient,
				wr.KubernetesClient.AppsV1().DaemonSets(namespace),
				"DaemonSet",
				namespace,
				name,
				deleteResource,
				nil,
				labels,
				wr.convertToPeerApplyConfigurations(ingressPortLabels),
				wr.convertToPeerApplyConfigurations(egressPortLabels),
				metav1.DeletePropagationBackground,
				appsv1ac.DaemonSet,
				appsv1ac.DaemonSetSpec,
				(*appsv1ac.DaemonSetApplyConfiguration).WithOwnerReferences,
				(*appsv1ac.DaemonSetApplyConfiguration).WithSpec,
				(*appsv1ac.DaemonSetApplyConfiguration).WithLabels,
				(*appsv1ac.DaemonSetSpecApplyConfiguration).WithSelector,
				(*appsv1ac.DaemonSetSpecApplyConfiguration).WithTemplate,
				createPodTemplateSpec(podOptions, containers, volumes),
				func(daemonSet *appsv1.DaemonSet) bool {
					return arePodsReady(
						daemonSet.Status.DesiredNumberScheduled,
						daemonSet.Status.NumberReady,
						daemonSet.Status.NumberAvailable,
						daemonSet.Status.UpdatedNumberScheduled,
						daemonSet.Generation,
						daemonSet.Status.ObservedGeneration,
					)
				},
			)
		},
	)
}
