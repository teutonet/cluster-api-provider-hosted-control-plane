package hostedcontrolplane

import (
	"context"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
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

func (wr *WorkloadClusterReconciler) ReconcileKubeletConfigRBAC(
	ctx context.Context,
	_ *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileKubeletConfigRBAC",
		func(ctx context.Context, span trace.Span) error {
			role := rbacv1ac.Role("kubeadm:kubelet-config", metav1.NamespaceSystem).
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

			roleBinding := rbacv1ac.RoleBinding("kubelet-config-role-binding", metav1.NamespaceSystem).
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
						WithName("system:bootstrappers:kubeadm:default-node-token"),
				)

			_, err = wr.kubernetesClient.RbacV1().RoleBindings(metav1.NamespaceSystem).Apply(ctx,
				roleBinding, workloadApplyOptions,
			)
			return errorsUtil.IfErrErrorf("failed to apply kubelet config role binding: %w", err)
		},
	)
}
