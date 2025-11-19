package reconcilers

import (
	"context"
	"fmt"
	"net"

	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	slices "github.com/samber/lo"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/kubernetes/pkg/controller/certificates/rootcacertpublisher"
)

type WorkloadResourceReconciler struct {
	Tracer                string
	WorkloadClusterClient *alias.WorkloadClusterClient
	CiliumClient          ciliumclient.Interface
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
			service, ready, err := reconcileService(
				ctx,
				wr.WorkloadClusterClient,
				namespace,
				name,
				nil,
				labels,
				serviceType,
				serviceIP,
				headless,
				ports,
			)
			if err != nil {
				return nil, false, errorsUtil.IfErrErrorf(
					"failed to reconcile service %s/%s into workloadcluster: %w",
					namespace, name, err,
				)
			}
			return service, ready, err
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
			return errorsUtil.IfErrErrorf("failed to reconcile configmap %s/%s into workloadcluster: %w",
				namespace,
				name,
				reconcileConfigmap(
					ctx,
					wr.WorkloadClusterClient,
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
	namespace string,
	name string,
	deleteResource bool,
	replicas int32,
	podOptions PodOptions,
	labels map[string]string,
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
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
			if err := wr.addKubeCAChecksum(ctx, namespace, &podOptions); err != nil {
				return nil, false, err
			}
			deployment, ready, err := reconcileDeployment(
				ctx,
				wr.WorkloadClusterClient,
				wr.CiliumClient,
				namespace,
				name,
				deleteResource,
				nil,
				labels,
				ingressPolicyTargets,
				egressPolicyTargets,
				replicas,
				createPodTemplateSpec(podOptions, nil, containers, volumes),
			)
			if err != nil {
				return nil, false, fmt.Errorf(
					"failed to reconcile deployment %s/%s into workloadcluster: %w",
					namespace, name, err,
				)
			}
			return deployment, ready, err
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
	ingressPolicyTargets map[int32][]networkpolicy.IngressNetworkPolicyTarget,
	egressPolicyTargets map[int32][]networkpolicy.EgressNetworkPolicyTarget,
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
			if err := wr.addKubeCAChecksum(ctx, namespace, &podOptions); err != nil {
				return nil, false, err
			}
			daemonSet, ready, err := reconcileWorkload(
				ctx,
				wr.WorkloadClusterClient,
				wr.CiliumClient,
				wr.WorkloadClusterClient.AppsV1().DaemonSets(namespace),
				appsv1.SchemeGroupVersion.String(),
				"DaemonSet",
				namespace,
				name,
				deleteResource,
				nil,
				labels,
				ingressPolicyTargets,
				egressPolicyTargets,
				metav1.DeletePropagationBackground,
				appsv1ac.DaemonSet,
				appsv1ac.DaemonSetSpec,
				(*appsv1ac.DaemonSetApplyConfiguration).WithOwnerReferences,
				(*appsv1ac.DaemonSetApplyConfiguration).WithSpec,
				(*appsv1ac.DaemonSetApplyConfiguration).WithLabels,
				(*appsv1ac.DaemonSetSpecApplyConfiguration).WithSelector,
				(*appsv1ac.DaemonSetSpecApplyConfiguration).WithTemplate,
				(*appsv1.DaemonSet).GetUID,
				createPodTemplateSpec(podOptions, nil, containers, volumes),
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
			if err != nil {
				return nil, false, fmt.Errorf(
					"failed to reconcile daemonset %s/%s into workloadcluster: %w",
					namespace, name, err,
				)
			}
			return daemonSet, ready, err
		},
	)
}

func (wr *WorkloadResourceReconciler) addKubeCAChecksum(
	ctx context.Context, namespace string, podOptions *PodOptions,
) error {
	// various components doesn't refresh the root CA cert, so we need to restart the pods when it changes
	// TODO: keep until https://github.com/kubernetes-sigs/apiserver-network-proxy/issues/801 is resolved
	// TODO: check coredns
	// TODO: check kube-proxy
	caCrtChecksum, err := operatorutil.CalculateConfigMapChecksum(ctx, wr.WorkloadClusterClient,
		namespace,
		[]string{rootcacertpublisher.RootCACertConfigMapName},
	)
	if err != nil {
		return fmt.Errorf("failed to calculate kube-root CA configmap checksum: %w", err)
	}
	if podOptions.Annotations == nil {
		podOptions.Annotations = map[string]string{}
	}
	podOptions.Annotations["checksum/ca.crt"] = caCrtChecksum
	return nil
}
