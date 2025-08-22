package reconcilers

import (
	"context"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
)

type WorkloadResourceReconciler struct {
	Tracer           string
	KubernetesClient kubernetes.Interface
}

func (wr *WorkloadResourceReconciler) ReconcileDeployment(
	ctx context.Context,
	name string,
	namespace string,
	replicas int32,
	serviceAccountName string,
	priorityClassName string,
	labels map[string]string,
	tolerations []*corev1ac.TolerationApplyConfiguration,
	containers []*corev1ac.ContainerApplyConfiguration,
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, error) {
	return tracing.WithSpan(ctx, wr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, error) {
			return reconcileDeployment(
				ctx,
				wr.KubernetesClient,
				namespace,
				name,
				labels,
				replicas,
				createPodTemplateSpec(serviceAccountName, priorityClassName, tolerations, containers, volumes),
			)
		},
	)
}

func (wr *WorkloadResourceReconciler) ReconcileDaemonSet(
	ctx context.Context,
	name string,
	namespace string,
	serviceAccountName string,
	priorityClassName string,
	hostNetwork bool,
	labels map[string]string,
	tolerations []*corev1ac.TolerationApplyConfiguration,
	containers []*corev1ac.ContainerApplyConfiguration,
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.DaemonSet, error) {
	return tracing.WithSpan(ctx, wr.Tracer, "ReconcileDaemonSet",
		func(ctx context.Context, span trace.Span) (*appsv1.DaemonSet, error) {
			return reconcile(
				ctx,
				wr.KubernetesClient,
				wr.KubernetesClient.AppsV1().DaemonSets(namespace),
				"DaemonSet",
				namespace,
				name,
				labels,
				appsv1ac.DaemonSet,
				appsv1ac.DaemonSetSpec,
				(*appsv1ac.DaemonSetApplyConfiguration).WithSpec,
				(*appsv1ac.DaemonSetApplyConfiguration).WithLabels,
				(*appsv1ac.DaemonSetSpecApplyConfiguration).WithSelector,
				(*appsv1ac.DaemonSetSpecApplyConfiguration).WithTemplate,
				createPodTemplateSpec(
					serviceAccountName,
					priorityClassName,
					tolerations,
					containers,
					volumes,
					func(spec *corev1ac.PodSpecApplyConfiguration) *corev1ac.PodSpecApplyConfiguration {
						return spec.WithHostNetwork(hostNetwork)
					},
				),
				func(_ *appsv1.DaemonSet) bool { return true },
			)
		},
	)
}
