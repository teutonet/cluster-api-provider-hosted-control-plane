package konnectivity

import (
	"context"
	"fmt"
	"math"
	"path"
	"strconv"

	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	"github.com/go-logr/logr"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type KonnectivityReconciler interface {
	ReconcileKonnectivity(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewKonnectivityReconciler(
	kubernetesClient alias.WorkloadClusterClient,
	ciliumClient ciliumclient.Interface,
	konnectivityNamespace string,
	konnectivityServiceAccount string,
	konnectivityServerAudience string,
	konnectivityServicePort int32,
) KonnectivityReconciler {
	return &konnectivityReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			KubernetesClient: kubernetesClient,
			CiliumClient:     ciliumClient,
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
	cluster *capiv2.Cluster,
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
					Name: "Deployment",
					Reconcile: func(ctx context.Context) (string, error) {
						return kr.reconcileKonnectivityDeployment(ctx, hostedControlPlane, cluster)
					},
				},
			}

			logger := logr.FromContextAsSlogLogger(ctx)
			for _, phase := range phases {
				notReadyReason, err := tracing.WithSpan(
					ctx,
					kr.Tracer,
					phase.Name,
					func(ctx context.Context, _ trace.Span) (string, error) {
						return phase.Reconcile(logr.NewContextWithSlogLogger(ctx, logger.With("phase", phase.Name)))
					},
				)
				if err != nil {
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

			role := rbacv1ac.Role(kr.konnectivityServiceAccountName, kr.konnectivityNamespace).
				WithRules(
					rbacv1ac.PolicyRule().
						WithAPIGroups("coordination.k8s.io").
						WithResources("leases").
						WithVerbs("watch", "list"),
				)
			_, err = kr.KubernetesClient.RbacV1().Roles(*role.Namespace).
				Apply(ctx, role, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to apply konnectivity agent role: %w", err)
			}

			roleBinding := rbacv1ac.RoleBinding(kr.konnectivityServiceAccountName, kr.konnectivityNamespace).
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind("Role").
						WithName(*role.Name),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(*serviceAccount.Kind).
						WithName(*serviceAccount.Name).
						WithNamespace(*serviceAccount.Namespace),
				)

			_, err = kr.KubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
				Apply(ctx, roleBinding, operatorutil.ApplyOptions)
			if err != nil {
				if apierrors.IsInvalid(err) {
					if err := kr.KubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
						Delete(ctx, *roleBinding.Name, metav1.DeleteOptions{}); err != nil {
						return "", fmt.Errorf(
							"failed to delete invalid konnectivity agent role binding %s/%s: %w",
							*roleBinding.Namespace,
							*roleBinding.Name,
							err,
						)
					}
					return "konnectivity agent role binding needs to be recreated", nil
				}
				return "", fmt.Errorf("failed to apply konnectivity agent role binding: %w", err)
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

func (kr *konnectivityReconciler) reconcileKonnectivityDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "reconcileKonnectivityDeployment",
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
				WithImage(operatorutil.ResolveKonnectivityImage(
					hostedControlPlane.Spec.KonnectivityClient.Image,
					"proxy-agent",
					minorVersion,
				)).
				WithImagePullPolicy(hostedControlPlane.Spec.KonnectivityClient.ImagePullPolicy).
				WithArgs(kr.buildKonnectivityClientArgs(
					ctx,
					hostedControlPlane,
					cluster,
					serviceAccountTokenVolumeMount,
					healthPort,
				)...).
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

			nodes, err := kr.KubernetesClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to list nodes: %w", err)
			}

			_, ready, err := kr.ReconcileDeployment(
				ctx,
				kr.konnectivityNamespace,
				"konnectivity-agent",
				false,
				int32(
					math.Min(float64(len(nodes.Items)), float64(hostedControlPlane.Spec.KonnectivityClient.Replicas)),
				),
				reconcilers.PodOptions{
					ServiceAccountName: kr.konnectivityServiceAccountName,
					PriorityClassName:  "system-cluster-critical",
					Tolerations: []*corev1ac.TolerationApplyConfiguration{
						corev1ac.Toleration().
							WithKey("CriticalAddonsOnly").
							WithOperator(corev1.TolerationOpExists),
					},
				},
				names.GetControlPlaneLabels(cluster, "konnectivity"),
				map[int32][]networkpolicy.IngressNetworkPolicyTarget{},
				map[int32][]networkpolicy.EgressNetworkPolicyTarget{
					0: { // needs to proxy to any port in the whole cluster
						networkpolicy.NewClusterNetworkPolicyTarget(),
					},
					kr.konnectivityServicePort: {
						networkpolicy.NewDNSNetworkPolicyTarget(
							names.GetKonnectivityServerHost(cluster),
						),
					},
				},
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{}),
				},
				[]*corev1ac.VolumeApplyConfiguration{serviceAccountTokenVolume},
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile konnectivity agent deployment: %w", err)
			}
			if !ready {
				return "konnectivity agent deployment not ready", nil
			}

			return "", nil
		},
	)
}

func (kr *konnectivityReconciler) buildKonnectivityClientArgs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	serviceAccountTokenVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	healthPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	args := map[string]string{
		"admin-server-port":   "8133",
		"ca-cert":             path.Join(serviceaccount.DefaultAPITokenMountPath, corev1.ServiceAccountRootCAKey),
		"health-server-port":  strconv.Itoa(int(*healthPort.ContainerPort)),
		"logtostderr":         "true",
		"proxy-server-host":   names.GetKonnectivityServerHost(cluster),
		"proxy-server-port":   strconv.Itoa(int(kr.konnectivityServicePort)),
		"agent-id":            "$(NODE_NAME)",
		"count-server-leases": "true",
		"service-account-token-path": path.Join(
			*serviceAccountTokenVolumeMount.MountPath,
			kr.konnectivityServiceAccountTokenName,
		),
	}
	return operatorutil.ArgsToSlice(
		ctx,
		hostedControlPlane.Spec.KonnectivityClient.Args,
		args,
	)
}
