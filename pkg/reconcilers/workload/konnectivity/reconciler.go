package konnectivity

import (
	"context"
	"fmt"
	"path"
	"strconv"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type KonnectivityReconciler interface {
	ReconcileKonnectivity(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) (string, error)
}

func NewKonnectivityReconciler(
	kubernetesClient *alias.WorkloadClusterClient,
	konnectivityNamespace string,
	konnectivityServiceAccount string,
	konnectivityServerAudience string,
	konnectivityServicePort int32,
) KonnectivityReconciler {
	return &konnectivityReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			KubernetesClient: kubernetesClient,
			Tracer:           tracing.GetTracer("konnectivity"),
		},
		konnectivityServerAudience:          konnectivityServerAudience,
		konnectivityServicePort:             konnectivityServicePort,
		konnectivityNamespace:               konnectivityNamespace,
		konnectivityServiceAccountName:      konnectivityServiceAccount,
		konnectivityServiceAccountTokenName: "konnectivity-agent-token",
	}
}

type konnectivityReconciler struct {
	reconcilers.WorkloadResourceReconciler
	konnectivityNamespace               string
	konnectivityServerAudience          string
	konnectivityServicePort             int32
	konnectivityServiceAccountName      string
	konnectivityServiceAccountTokenName string
}

var _ KonnectivityReconciler = &konnectivityReconciler{}

func (kr *konnectivityReconciler) ReconcileKonnectivity(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "ReconcileKonnectivity",
		func(ctx context.Context, span trace.Span) (string, error) {
			phases := []struct {
				Name      string
				Reconcile func(context.Context) (string, error)
			}{
				{
					Name:      "RBAC",
					Reconcile: kr.reconcileKonnectivityRBAC,
				},
				{
					Name: "DaemonSet",
					Reconcile: func(ctx context.Context) (string, error) {
						return kr.reconcileKonnectivityDaemonSet(ctx, hostedControlPlane, cluster)
					},
				},
			}

			for _, phase := range phases {
				if notReadyReason, err := phase.Reconcile(ctx); err != nil {
					return "", fmt.Errorf("failed to reconcile konnectivity phase %s: %w", phase.Name, err)
				} else if notReadyReason != "" {
					return notReadyReason, nil
				}
			}

			return "", nil
		},
	)
}

func (kr *konnectivityReconciler) reconcileKonnectivityRBAC(ctx context.Context) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "reconcileKonnectivityRBAC",
		func(ctx context.Context, span trace.Span) (string, error) {
			serviceAccount := corev1ac.ServiceAccount(
				kr.konnectivityServiceAccountName,
				kr.konnectivityNamespace,
			)

			_, err := kr.KubernetesClient.CoreV1().
				ServiceAccounts(*serviceAccount.Namespace).
				Apply(ctx, serviceAccount, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to apply konnectivity agent service account: %w", err)
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding(kr.konnectivityServerAudience).
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind("ClusterRole").
						WithName("system:auth-delegator"),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(*serviceAccount.Kind).
						WithName(*serviceAccount.Name).
						WithNamespace(*serviceAccount.Namespace),
				)

			_, err = kr.KubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
			if err != nil {
				if apierrors.IsInvalid(err) {
					if err := kr.KubernetesClient.RbacV1().ClusterRoleBindings().
						Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
						return "", fmt.Errorf(
							"failed to delete invalid konnectivity agent cluster role binding %s: %w",
							*clusterRoleBinding.Name,
							err,
						)
					}
					return "konnectivity agent cluster role binding needs to be recreated", nil
				}
				return "", fmt.Errorf(
					"failed to apply konnectivity agent cluster role binding %s: %w",
					*clusterRoleBinding.Name,
					err,
				)
			}

			return "", nil
		},
	)
}

func (kr *konnectivityReconciler) reconcileKonnectivityDaemonSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "reconcileKonnectivityDaemonSet",
		func(ctx context.Context, span trace.Span) (string, error) {
			serviceAccountTokenVolume := corev1ac.Volume().
				WithName(kr.konnectivityServiceAccountName).
				WithProjected(corev1ac.ProjectedVolumeSource().
					WithSources(
						corev1ac.VolumeProjection().
							WithServiceAccountToken(corev1ac.ServiceAccountTokenProjection().
								WithAudience(kr.konnectivityServerAudience).
								WithExpirationSeconds(3600).
								WithPath(kr.konnectivityServiceAccountTokenName),
							),
					).
					WithDefaultMode(420),
				)
			serviceAccountTokenVolumeMount := corev1ac.VolumeMount().
				WithName(*serviceAccountTokenVolume.Name).
				WithMountPath("/var/run/secrets/tokens").
				WithReadOnly(true)
			healthPort := corev1ac.ContainerPort().
				WithName("health").
				WithContainerPort(8134).
				WithProtocol(corev1.ProtocolTCP)

			minorVersion, err := operatorutil.GetMinorVersion(hostedControlPlane)
			if err != nil {
				return "", fmt.Errorf("failed to get minor version of hosted control plane: %w", err)
			}

			container := corev1ac.Container().
				WithName("konnectivity-agent").
				WithImage(
					fmt.Sprintf("registry.k8s.io/kas-network-proxy/proxy-agent:v0.%d.0", minorVersion),
				).
				WithArgs(kr.buildKonnectivityClientArgs(
					hostedControlPlane,
					cluster,
					serviceAccountTokenVolumeMount,
					healthPort,
				)...).
				WithImagePullPolicy(corev1.PullAlways).
				WithEnv(corev1ac.EnvVar().
					WithName("NODE_NAME").
					WithValueFrom(corev1ac.EnvVarSource().
						WithFieldRef(corev1ac.ObjectFieldSelector().
							WithFieldPath("spec.nodeName"),
						),
					),
				).
				WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
					hostedControlPlane.Spec.KonnectivityClient.Resources,
				)).
				WithStartupProbe(operatorutil.CreateStartupProbe(healthPort, "/readyz", corev1.URISchemeHTTP)).
				WithReadinessProbe(operatorutil.CreateReadinessProbe(healthPort, "/readyz", corev1.URISchemeHTTP)).
				WithLivenessProbe(operatorutil.CreateLivenessProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
				WithPorts(healthPort).
				WithVolumeMounts(serviceAccountTokenVolumeMount)

			_, ready, err := kr.ReconcileDaemonSet(
				ctx,
				"konnectivity-agent",
				kr.konnectivityNamespace,
				reconcilers.PodOptions{
					ServiceAccountName: kr.konnectivityServiceAccountName,
					PriorityClassName:  "system-node-critical",
					Tolerations: []*corev1ac.TolerationApplyConfiguration{
						corev1ac.Toleration().
							WithKey("CriticalAddonsOnly").
							WithOperator(corev1.TolerationOpExists),
					},
				},
				names.GetControlPlaneLabels(cluster, "konnectivity"),
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{}),
				},
				[]*corev1ac.VolumeApplyConfiguration{serviceAccountTokenVolume},
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile konnectivity agent daemonset: %w", err)
			}
			if !ready {
				return "konnectivity agent daemonSet not ready", nil
			}

			return "", nil
		},
	)
}

func (kr *konnectivityReconciler) buildKonnectivityClientArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	serviceAccountTokenVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	healthPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	args := map[string]string{
		"admin-server-port":  "8133",
		"ca-cert":            path.Join(serviceaccount.DefaultAPITokenMountPath, corev1.ServiceAccountRootCAKey),
		"health-server-port": strconv.Itoa(int(*healthPort.ContainerPort)),
		"logtostderr":        "true",
		"proxy-server-host":  names.GetKonnectivityServerHost(cluster),
		"proxy-server-port":  strconv.Itoa(int(kr.konnectivityServicePort)),
		"agent-id":           "$(NODE_NAME)",
		"service-account-token-path": path.Join(
			*serviceAccountTokenVolumeMount.MountPath,
			kr.konnectivityServiceAccountTokenName,
		),
	}
	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.KonnectivityClient.Args, args)
}
