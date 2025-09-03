package reconcilers

import (
	"context"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

type WorkloadResourceReconciler struct {
	Tracer           string
	KubernetesClient *alias.WorkloadClusterClient
}

func (wr *WorkloadResourceReconciler) ReconcileDeployment(
	ctx context.Context,
	name string,
	namespace string,
	replicas int32,
	podOptions PodOptions,
	labels map[string]string,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	return tracing.WithSpan3(ctx, wr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, bool, error) {
			return reconcileDeployment(
				ctx,
				wr.KubernetesClient,
				namespace,
				name,
				nil,
				labels,
				replicas,
				createPodTemplateSpec(podOptions, containers, volumes),
			)
		},
	)
}

func (wr *WorkloadResourceReconciler) ReconcileDaemonSet(
	ctx context.Context,
	name string,
	namespace string,
	podOptions PodOptions,
	labels map[string]string,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.DaemonSet, bool, error) {
	return tracing.WithSpan3(ctx, wr.Tracer, "ReconcileDaemonSet",
		func(ctx context.Context, span trace.Span) (*appsv1.DaemonSet, bool, error) {
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
				createPodTemplateSpec(podOptions, containers, volumes),
				func(_ *appsv1.DaemonSet) bool { return true },
			)
		},
	)
}
