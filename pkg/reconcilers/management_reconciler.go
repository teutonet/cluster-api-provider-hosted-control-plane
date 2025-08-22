package reconcilers

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type ManagementResourceReconciler struct {
	Tracer           string
	KubernetesClient kubernetes.Interface
}

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;patch;delete

func (mr *ManagementResourceReconciler) ReconcileDeployment(
	ctx context.Context,
	cluster *capiv1.Cluster,
	replicas int32,
	component string,
	targetComponent string,
	containers []*corev1ac.ContainerApplyConfiguration,
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, error) {
	return tracing.WithSpan(ctx, mr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, error) {
			return reconcileDeployment(
				ctx,
				mr.KubernetesClient,
				cluster.Namespace,
				fmt.Sprintf("%s-%s", cluster.Name, component),
				names.GetControlPlaneLabels(cluster, component),
				replicas,
				createPodTemplateSpec(
					"",
					"",
					[]*corev1ac.TolerationApplyConfiguration{},
					containers,
					volumes,
					func(spec *corev1ac.PodSpecApplyConfiguration) *corev1ac.PodSpecApplyConfiguration {
						if targetComponent != "" {
							return spec.WithAffinity(corev1ac.Affinity().
								WithPodAffinity(corev1ac.PodAffinity().
									WithPreferredDuringSchedulingIgnoredDuringExecution(
										corev1ac.WeightedPodAffinityTerm().
											WithWeight(100).
											WithPodAffinityTerm(corev1ac.PodAffinityTerm().
												WithLabelSelector(names.GetControlPlaneSelector(cluster, targetComponent)).
												WithTopologyKey(corev1.LabelHostname),
											),
									),
								))
						}
						return spec
					},
				),
			)
		},
	)
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
