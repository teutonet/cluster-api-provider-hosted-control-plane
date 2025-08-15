package konnectivity

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type KonnectivityReconciler interface {
	ReconcileKonnectivity(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
}

func NewKonnectivityReconciler(
	kubernetesClient kubernetes.Interface,
	konnectivityServerAudience string,
	konnectivityServicePort int32,
) KonnectivityReconciler {
	return &konnectivityReconciler{
		kubernetesClient:                    kubernetesClient,
		konnectivityServerAudience:          konnectivityServerAudience,
		konnectivityServicePort:             konnectivityServicePort,
		konnectivityServiceAccountName:      "konnectivity-agent",
		konnectivityServiceAccountTokenName: "konnectivity-agent-token",
		tracer:                              tracing.GetTracer("konnectivity"),
	}
}

type konnectivityReconciler struct {
	kubernetesClient                    kubernetes.Interface
	konnectivityServerAudience          string
	konnectivityServicePort             int32
	konnectivityServiceAccountName      string
	konnectivityServiceAccountTokenName string
	tracer                              string
}

var _ KonnectivityReconciler = &konnectivityReconciler{}

func (kr *konnectivityReconciler) ReconcileKonnectivity(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, kr.tracer, "ReconcileKonnectivity",
		func(ctx context.Context, span trace.Span) error {
			phases := []struct {
				Name      string
				Reconcile func(context.Context) error
			}{
				{
					Name:      "RBAC",
					Reconcile: kr.reconcileKonnectivityRBAC,
				},
				{
					Name: "DaemonSet",
					Reconcile: func(ctx context.Context) error {
						return kr.reconcileKonnectivityDaemonSet(ctx, hostedControlPlane, cluster)
					},
				},
			}

			for _, phase := range phases {
				if err := phase.Reconcile(ctx); err != nil {
					return fmt.Errorf("failed to reconcile konnectivity phase %s: %w", phase.Name, err)
				}
			}

			return nil
		},
	)
}

func (kr *konnectivityReconciler) reconcileKonnectivityRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, kr.tracer, "reconcileKonnectivityRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(
				"konnectivity-agent",
				metav1.NamespaceSystem,
			)

			_, err := kr.kubernetesClient.CoreV1().
				ServiceAccounts(metav1.NamespaceSystem).
				Apply(ctx, serviceAccount, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply konnectivity agent service account: %w", err)
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

			_, err = kr.kubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
			if err != nil {
				if apierrors.IsInvalid(err) {
					if err := kr.kubernetesClient.RbacV1().ClusterRoleBindings().
						Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
						return fmt.Errorf(
							"failed to delete invalid konnectivity agent cluster role binding %s: %w",
							*clusterRoleBinding.Name,
							err,
						)
					}
					return operatorutil.ErrRequeueRequired
				}
				return fmt.Errorf(
					"failed to apply konnectivity agent cluster role binding %s: %w",
					*clusterRoleBinding.Name,
					err,
				)
			}

			return nil
		},
	)
}

func (kr *konnectivityReconciler) reconcileKonnectivityDaemonSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, kr.tracer, "reconcileKonnectivityDaemonSet",
		func(ctx context.Context, span trace.Span) error {
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
				return fmt.Errorf("failed to get minor version of hosted control plane: %w", err)
			}

			template := corev1ac.PodTemplateSpec().
				WithLabels(names.GetControlPlaneLabels(cluster, "konnectivity")).
				WithSpec(corev1ac.PodSpec().
					WithServiceAccountName(kr.konnectivityServiceAccountName).
					WithTolerations(
						corev1ac.Toleration().
							WithKey("CriticalAddonsOnly").
							WithOperator(corev1.TolerationOpExists),
					).
					WithContainers(
						corev1ac.Container().
							WithName("konnectivity-agent").
							WithImage(
								fmt.Sprintf("registry.k8s.io/kas-network-proxy/proxy-agent:v0.%d.0", minorVersion),
							).
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
							WithStartupProbe(operatorutil.CreateStartupProbe(
								healthPort,
								"/readyz",
								corev1.URISchemeHTTP,
							)).
							WithReadinessProbe(operatorutil.CreateReadinessProbe(
								healthPort,
								"/readyz",
								corev1.URISchemeHTTP,
							)).
							WithLivenessProbe(operatorutil.CreateLivenessProbe(
								healthPort,
								"/healthz",
								corev1.URISchemeHTTP,
							)).
							WithArgs(kr.buildKonnectivityClientArgs(
								hostedControlPlane,
								cluster,
								serviceAccountTokenVolumeMount,
								healthPort,
							)...).
							WithPorts(healthPort).
							WithVolumeMounts(serviceAccountTokenVolumeMount),
					).
					WithVolumes(serviceAccountTokenVolume),
				)

			template, err = operatorutil.SetChecksumAnnotations(ctx, kr.kubernetesClient, cluster.Namespace, template)
			if err != nil {
				return fmt.Errorf("failed to set checksum annotations: %w", err)
			}

			daemonSet := appsv1ac.DaemonSet("konnectivity-agent", metav1.NamespaceSystem).
				WithSpec(appsv1ac.DaemonSetSpec().
					WithSelector(names.GetControlPlaneSelector(cluster, "konnectivity")).
					WithTemplate(template),
				)

			if err := operatorutil.ValidateMounts(daemonSet.Spec.Template.Spec); err != nil {
				return fmt.Errorf("konnectivity daemonset has mounts without corresponding volume: %w", err)
			}

			_, err = kr.kubernetesClient.AppsV1().DaemonSets(metav1.NamespaceSystem).
				Apply(ctx, daemonSet, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply konnectivity agent daemonset: %w", err)
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
		"proxy-server-host":  cluster.Spec.ControlPlaneEndpoint.Host,
		"proxy-server-port":  strconv.Itoa(int(kr.konnectivityServicePort)),
		"agent-id":           "$(NODE_NAME)",
		"service-account-token-path": path.Join(
			*serviceAccountTokenVolumeMount.MountPath,
			kr.konnectivityServiceAccountTokenName,
		),
	}
	return operatorutil.ArgsToSlice(hostedControlPlane.Spec.KonnectivityClient.Args, args)
}
