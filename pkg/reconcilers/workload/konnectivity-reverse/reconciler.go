package konnectivityreverse

import (
	"context"
	"fmt"
	"math"

	"github.com/go-logr/logr"
	slices "github.com/samber/lo"

	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
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
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type ReverseKonnectivityReconciler interface {
	ReconcileReverseKonnectivity(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewReverseKonnectivityReconciler(
	managementClusterClient *alias.WorkloadClusterClient,
	ciliumClient ciliumclient.Interface,
	konnectivityNamespace string,
	konnectivityServiceAccount string,
	konnectivityServerAudience string,
	konnectivityServerPort int32,
) ReverseKonnectivityReconciler {
	return &reverseKonnectivityReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			WorkloadClusterClient: managementClusterClient,
			CiliumClient:          ciliumClient,
			Tracer:                tracing.GetTracer("konnectivity-reverse"),
		},
		konnectivityServerAudience:      konnectivityServerAudience,
		konnectivityServerPort:          konnectivityServerPort,
		konnectivityNamespace:           konnectivityNamespace,
		konnectivityServerServiceAccount: konnectivityServiceAccount,
		konnectivityServerTokenName:     "konnectivity-server-token",
	}
}

type reverseKonnectivityReconciler struct {
	reconcilers.WorkloadResourceReconciler
	konnectivityNamespace              string
	konnectivityServerAudience         string
	konnectivityServerPort             int32
	konnectivityServerServiceAccount   string
	konnectivityServerTokenName        string
}

var _ ReverseKonnectivityReconciler = &reverseKonnectivityReconciler{}

// ReconcileReverseKonnectivity sets up the reverse konnectivity tunnel
// (workload cluster → control plane communication via konnectivity).
//
// Phases:
// 1. RBAC: Create service account and roles for reverse server
// 2. Deployment: Deploy reverse konnectivity server to workload cluster
// 3. Network Policy: Allow CP → WL traffic on reverse tunnel port
func (rkr *reverseKonnectivityReconciler) ReconcileReverseKonnectivity(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "ReconcileReverseKonnectivity",
		func(ctx context.Context, span trace.Span) (string, error) {
			// TODO: Implement reverse konnectivity reconciliation
			// See ARCHITECTURE.md for design details

			phases := []struct {
				Name      string
				Reconcile func(context.Context) (string, error)
			}{
				{
					Name:      "RBAC",
					Reconcile: rkr.reconcileReverseKonnectivityRBAC,
				},
				{
					Name: "Deployment",
					Reconcile: func(ctx context.Context) (string, error) {
						return rkr.reconcileReverseKonnectivityDeployment(ctx, hostedControlPlane, cluster)
					},
				},
				{
					Name: "NetworkPolicy",
					Reconcile: func(ctx context.Context) (string, error) {
						return rkr.reconcileReverseKonnectivityNetworkPolicy(ctx, cluster)
					},
				},
			}

			logger := logr.FromContextAsSlogLogger(ctx)
			for _, phase := range phases {
				notReadyReason, err := tracing.WithSpan(ctx, rkr.Tracer, phase.Name,
					func(ctx context.Context, _ trace.Span) (string, error) {
						return phase.Reconcile(logr.NewContextWithSlogLogger(ctx, logger.With("phase", phase.Name)))
					},
				)
				if err != nil {
					return "", fmt.Errorf("failed to reconcile reverse konnectivity phase %s: %w", phase.Name, err)
				} else if notReadyReason != "" {
					return notReadyReason, nil
				}
			}

			return "", nil
		},
	)
}

func (rkr *reverseKonnectivityReconciler) reconcileReverseKonnectivityRBAC(ctx context.Context) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "reconcileReverseKonnectivityRBAC",
		func(ctx context.Context, span trace.Span) (string, error) {
			serviceAccount := corev1ac.ServiceAccount(
				rkr.konnectivityServerServiceAccount,
				rkr.konnectivityNamespace,
			)

			_, err := rkr.WorkloadClusterClient.CoreV1().
				ServiceAccounts(*serviceAccount.Namespace).
				Apply(ctx, serviceAccount, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to apply reverse konnectivity server service account: %w", err)
			}

			role := rbacv1ac.Role(rkr.konnectivityServerServiceAccount, rkr.konnectivityNamespace).
				WithRules(
					rbacv1ac.PolicyRule().
						WithAPIGroups("coordination.k8s.io").
						WithResources("leases").
						WithVerbs("watch", "list"),
				)
			_, err = rkr.WorkloadClusterClient.RbacV1().Roles(*role.Namespace).
				Apply(ctx, role, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to apply reverse konnectivity server role: %w", err)
			}

			roleBinding := rbacv1ac.RoleBinding(rkr.konnectivityServerServiceAccount, rkr.konnectivityNamespace).
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

			_, err = rkr.WorkloadClusterClient.RbacV1().RoleBindings(*roleBinding.Namespace).
				Apply(ctx, roleBinding, operatorutil.ApplyOptions)
			if err != nil {
				if apierrors.IsInvalid(err) {
					if err := rkr.WorkloadClusterClient.RbacV1().RoleBindings(*roleBinding.Namespace).
						Delete(ctx, *roleBinding.Name, metav1.DeleteOptions{}); err != nil {
						return "", fmt.Errorf(
							"failed to delete invalid reverse konnectivity server role binding %s/%s: %w",
							*roleBinding.Namespace,
							*roleBinding.Name,
							err,
						)
					}
					return "reverse konnectivity server role binding needs to be recreated", nil
				}
				return "", fmt.Errorf("failed to apply reverse konnectivity server role binding: %w", err)
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding(rkr.konnectivityServerAudience).
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

			_, err = rkr.WorkloadClusterClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
			if err != nil {
				if apierrors.IsInvalid(err) {
					if err := rkr.WorkloadClusterClient.RbacV1().ClusterRoleBindings().
						Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
						return "", fmt.Errorf(
							"failed to delete invalid reverse konnectivity server cluster role binding %s: %w",
							*clusterRoleBinding.Name,
							err,
						)
					}
					return "reverse konnectivity server cluster role binding needs to be recreated", nil
				}
				return "", fmt.Errorf(
					"failed to apply reverse konnectivity server cluster role binding %s: %w",
					*clusterRoleBinding.Name,
					err,
				)
			}

			return "", nil
		},
	)
}

func (rkr *reverseKonnectivityReconciler) reconcileReverseKonnectivityDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "reconcileReverseKonnectivityDeployment",
		func(ctx context.Context, span trace.Span) (string, error) {
			if hostedControlPlane.Spec.KonnectivityReverse == nil ||
				hostedControlPlane.Spec.KonnectivityReverse.Server == nil {
				return "", fmt.Errorf("reverse konnectivity server configuration is missing")
			}

			serverConfig := hostedControlPlane.Spec.KonnectivityReverse.Server
			serverPortNum := rkr.konnectivityServerPort
			if serverConfig.Port != nil {
				serverPortNum = *serverConfig.Port
			}

			serviceAccountTokenVolume := corev1ac.Volume().
				WithName(rkr.konnectivityServerTokenName).
				WithProjected(corev1ac.ProjectedVolumeSource().
					WithSources(
						corev1ac.VolumeProjection().
							WithServiceAccountToken(corev1ac.ServiceAccountTokenProjection().
								WithAudience(rkr.konnectivityServerAudience).
								WithExpirationSeconds(3600).
								WithPath(rkr.konnectivityServerTokenName),
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

			serverPort := corev1ac.ContainerPort().
				WithName("server").
				WithContainerPort(serverPortNum).
				WithProtocol(corev1.ProtocolTCP)

			minorVersion, err := operatorutil.GetMinorVersion(hostedControlPlane)
			if err != nil {
				return "", fmt.Errorf("failed to get minor version of hosted control plane: %w", err)
			}

			container := corev1ac.Container().
				WithName("konnectivity-server").
				WithImage(operatorutil.ResolveKonnectivityImage(
					serverConfig.Image,
					"server",
					minorVersion,
				)).
				WithImagePullPolicy(serverConfig.ImagePullPolicyOrDefault()).
				WithArgs(
					"/proxy",
					"server",
					"--bind-address=0.0.0.0",
					fmt.Sprintf("--server-port=%d", serverPortNum),
					"--health-port=8134",
					"--mode=grpc",
					fmt.Sprintf("--service-account-token-file=%s", "/var/run/secrets/tokens/"+rkr.konnectivityServerTokenName),
					"--server-ca-file=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
				).
				WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
					serverConfig.Resources,
				)).
				WithStartupProbe(operatorutil.CreateStartupProbe(healthPort, "/readyz", corev1.URISchemeHTTP)).
				WithReadinessProbe(operatorutil.CreateReadinessProbe(healthPort, "/readyz", corev1.URISchemeHTTP)).
				WithLivenessProbe(operatorutil.CreateLivenessProbe(healthPort, "/healthz", corev1.URISchemeHTTP)).
				WithPorts(healthPort, serverPort).
				WithVolumeMounts(serviceAccountTokenVolumeMount)

			nodes, err := rkr.WorkloadClusterClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to list nodes: %w", err)
			}

			replicas := int32(1)
			if serverConfig.Replicas != nil {
				replicas = *serverConfig.Replicas
			} else {
				replicas = int32(math.Max(1, float64(len(nodes.Items))))
			}

			_, ready, err := rkr.ReconcileDeployment(
				ctx,
				rkr.konnectivityNamespace,
				"konnectivity-server",
				false,
				replicas,
				reconcilers.PodOptions{
					ServiceAccountName: rkr.konnectivityServerServiceAccount,
					PriorityClassName:  "system-cluster-critical",
					Tolerations: []*corev1ac.TolerationApplyConfiguration{
						corev1ac.Toleration().
							WithKey("CriticalAddonsOnly").
							WithOperator(corev1.TolerationOpExists),
					},
				},
				names.GetControlPlaneLabels(cluster, "konnectivity-reverse"),
				map[int32][]networkpolicy.IngressNetworkPolicyTarget{
					serverPortNum: {
						networkpolicy.NewCIDRNetworkPolicyTarget("0.0.0.0/0"),
					},
				},
				map[int32][]networkpolicy.EgressNetworkPolicyTarget{},
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{
						NeedsServiceAccount: true,
					}),
				},
				[]*corev1ac.VolumeApplyConfiguration{serviceAccountTokenVolume},
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile reverse konnectivity server deployment: %w", err)
			}
			if !ready {
				return "reverse konnectivity server deployment not ready", nil
			}

			return "", nil
		},
	)
}

func (rkr *reverseKonnectivityReconciler) reconcileReverseKonnectivityNetworkPolicy(
	ctx context.Context,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "reconcileReverseKonnectivityNetworkPolicy",
		func(ctx context.Context, span trace.Span) (string, error) {
			// Network policies for reverse konnectivity server are handled in the deployment phase
			// via ReconcileDeployment's ingress network policy targets. This phase serves as a
			// checkpoint to ensure the deployment was successful before proceeding.
			return "", nil
		},
	)
}
