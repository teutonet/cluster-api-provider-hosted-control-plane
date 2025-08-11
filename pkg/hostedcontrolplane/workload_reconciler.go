package hostedcontrolplane

import (
	"context"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	workloadClusterReconcilerTracer = "WorkloadClusterReconciler"
)

type WorkloadClusterReconciler struct {
	kubernetesClient kubernetes.Interface
}

var workloadApplyOptions = metav1.ApplyOptions{
	FieldManager: "workload-cluster-reconciler",
	Force:        true,
}

var nodeBootstrapTokenAuthGroup = "system:bootstrappers:kubeadm:default-node-token"

func (wr *WorkloadClusterReconciler) ReconcileKubeletConfigRBAC(
	ctx context.Context,
	_ *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileKubeletConfigRBAC",
		func(ctx context.Context, span trace.Span) error {
			name := "kubeadm:kubelet-config"
			role := rbacv1ac.Role(name, metav1.NamespaceSystem).
				WithRules(
					rbacv1ac.PolicyRule().
						WithAPIGroups("").
						WithResources("configmaps").
						WithResourceNames("kubelet-config").
						WithVerbs("get"),
				)

			_, err := wr.kubernetesClient.RbacV1().Roles(metav1.NamespaceSystem).Apply(ctx, role, workloadApplyOptions)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to apply kubelet config role: %w", err)
			}

			roleBinding := rbacv1ac.RoleBinding(name, metav1.NamespaceSystem).
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind(*role.Kind).
						WithName(*role.Name),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(rbacv1.GroupKind).
						WithName("system:nodes"),
					rbacv1ac.Subject().
						WithKind(rbacv1.GroupKind).
						WithName(nodeBootstrapTokenAuthGroup),
				)

			_, err = wr.kubernetesClient.RbacV1().RoleBindings(metav1.NamespaceSystem).Apply(ctx,
				roleBinding, workloadApplyOptions,
			)
			return errorsUtil.IfErrErrorf("failed to apply kubelet config role binding: %w", err)
		},
	)
}

func (wr *WorkloadClusterReconciler) ReconcileNodeRBAC(
	ctx context.Context,
	_ *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileNodeRBAC",
		func(ctx context.Context, span trace.Span) error {
			name := "kubeadm:get-nodes"
			clusterRole := rbacv1ac.ClusterRole(name).
				WithRules(
					rbacv1ac.PolicyRule().
						WithAPIGroups("").
						WithResources("nodes").
						WithVerbs("get"),
				)

			_, err := wr.kubernetesClient.RbacV1().ClusterRoles().Apply(ctx, clusterRole, workloadApplyOptions)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to apply node cluster role: %w", err)
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding(name).
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind(*clusterRole.Kind).
						WithName(*clusterRole.Name),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(rbacv1.GroupKind).
						WithName(nodeBootstrapTokenAuthGroup),
				)

			_, err = wr.kubernetesClient.RbacV1().
				ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, workloadApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply node cluster role binding: %w", err)
		},
	)
}

func (wr *WorkloadClusterReconciler) ReconcileClusterAdminBinding(
	ctx context.Context,
	_ *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileClusterAdminBinding",
		func(ctx context.Context, span trace.Span) error {
			clusterRoleBinding := rbacv1ac.ClusterRoleBinding("kubeadm:cluster-admins").
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind("ClusterRole").
						WithName("cluster-admin"),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(rbacv1.GroupKind).
						WithName("kubeadm:cluster-admins"),
				)

			_, err := wr.kubernetesClient.RbacV1().
				ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, workloadApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply cluster admin binding: %w", err)
		},
	)
}
