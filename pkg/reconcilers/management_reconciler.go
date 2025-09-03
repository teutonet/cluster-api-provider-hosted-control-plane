package reconcilers

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
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
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	replicas int32,
	priorityClassName string,
	component string,
	targetComponent string,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
) (*appsv1.Deployment, bool, error) {
	return tracing.WithSpan3(ctx, mr.Tracer, "ReconcileDeployment",
		func(ctx context.Context, span trace.Span) (*appsv1.Deployment, bool, error) {
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
				util.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				names.GetControlPlaneLabels(cluster, component),
				replicas,
				createPodTemplateSpec(podOptions, containers, volumes),
			)
		},
	)
}

func (mr *ManagementResourceReconciler) ReconcileStatefulset(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	name string,
	namespace string,
	podOptions PodOptions,
	serviceName string,
	podManagementPolicy appsv1.PodManagementPolicyType,
	updateStrategy *appsv1ac.StatefulSetUpdateStrategyApplyConfiguration,
	labels map[string]string,
	replicas int32,
	containers []slices.Tuple2[*corev1ac.ContainerApplyConfiguration, ContainerOptions],
	volumes []*corev1ac.VolumeApplyConfiguration,
	volumeClaimTemplates []*corev1ac.PersistentVolumeClaimApplyConfiguration,
	volumeClaimRetentionPolicy *appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicyApplyConfiguration,
) (*appsv1.StatefulSet, bool, error) {
	return tracing.WithSpan3(ctx, mr.Tracer, "ReconcileStatefulset",
		func(ctx context.Context, span trace.Span) (*appsv1.StatefulSet, bool, error) {
			return reconcileStatefulset(
				ctx,
				mr.KubernetesClient,
				namespace,
				name,
				util.GetOwnerReferenceApplyConfiguration(hostedControlPlane),
				serviceName,
				podManagementPolicy,
				updateStrategy,
				labels,
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
