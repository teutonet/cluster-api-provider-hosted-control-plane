package rbac

import (
	"context"
	"fmt"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/bootstraptoken/clusterinfo"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/uploadconfig"
)

type RBACReconciler interface {
	ReconcileRBAC(ctx context.Context) error
}

func NewRBACReconciler(
	kubernetesClient kubernetes.Interface,
) RBACReconciler {
	return &rbacReconciler{
		kubernetesClient: kubernetesClient,
		tracer:           tracing.GetTracer("rbac"),
	}
}

type rbacReconciler struct {
	kubernetesClient kubernetes.Interface
	tracer           string
}

var _ RBACReconciler = &rbacReconciler{}

type RBACResources struct {
	ClusterRoles        []*rbacv1ac.ClusterRoleApplyConfiguration
	ClusterRoleBindings []*rbacv1ac.ClusterRoleBindingApplyConfiguration
	Roles               []*rbacv1ac.RoleApplyConfiguration
	RoleBindings        []*rbacv1ac.RoleBindingApplyConfiguration
}

func (rr *rbacReconciler) ReconcileRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, rr.tracer, "ReconcileRBAC",
		func(ctx context.Context, span trace.Span) error {
			phases := []struct {
				Name     string
				Generate func() (*RBACResources, error)
			}{
				{
					Name:     "NodeBootstrapGetNodesRBAC",
					Generate: rr.generateNodeBootstrapGetNodesRBAC,
				},
				{
					Name:     "NodeBootstrapTokenPostCSRsRBAC",
					Generate: rr.generateNodeBootstrapTokenPostCSRsRBAC,
				},
				{
					Name:     "AutoApproveNodeBootstrapTokenRBAC",
					Generate: rr.generateAutoApproveNodeBootstrapTokenRBAC,
				},
				{
					Name:     "AutoApproveNodeCertificateRotationRBAC",
					Generate: rr.generateAutoApproveNodeCertificateRotationRBAC,
				},
				{
					Name:     "ClusterInfoRBAC",
					Generate: rr.generateClusterInfoRBAC,
				},
				{
					Name:     "KubeletConfigRBAC",
					Generate: rr.generateKubeletConfigRBAC,
				},
				{
					Name:     "KubeletKubeadmConfigRBAC",
					Generate: rr.generateKubeletKubeadmConfigRBAC,
				},
				{
					Name:     "ClusterAdminBindingRBAC",
					Generate: rr.generateClusterAdminBindingRBAC,
				},
			}

			for _, phase := range phases {
				resources, err := phase.Generate()
				if err != nil {
					return fmt.Errorf("failed to generate %s: %w", phase.Name, err)
				}
				for _, clusterRole := range resources.ClusterRoles {
					_, err := rr.kubernetesClient.RbacV1().ClusterRoles().
						Apply(ctx, clusterRole, operatorutil.ApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := rr.kubernetesClient.RbacV1().ClusterRoles().
								Delete(ctx, *clusterRole.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid cluster role %s: %w",
									*clusterRole.Name, err,
								)
							}
							return operatorutil.ErrRequeueRequired
						}
						return fmt.Errorf("failed to apply cluster role %s: %w", *clusterRole.Name, err)
					}
				}

				for _, clusterRoleBinding := range resources.ClusterRoleBindings {
					_, err := rr.kubernetesClient.RbacV1().ClusterRoleBindings().
						Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := rr.kubernetesClient.RbacV1().ClusterRoleBindings().
								Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid cluster role binding %s: %w",
									*clusterRoleBinding.Name, err,
								)
							}
							return operatorutil.ErrRequeueRequired
						}
						return fmt.Errorf("failed to apply cluster role binding %s: %w", *clusterRoleBinding.Name, err)
					}
				}

				for _, role := range resources.Roles {
					_, err := rr.kubernetesClient.RbacV1().Roles(*role.Namespace).
						Apply(ctx, role, operatorutil.ApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := rr.kubernetesClient.RbacV1().Roles(*role.Namespace).
								Delete(ctx, *role.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid role %s in namespace %s: %w",
									*role.Name, *role.Namespace, err,
								)
							}
							return operatorutil.ErrRequeueRequired
						}
						return fmt.Errorf(
							"failed to apply role %s in namespace %s: %w",
							*role.Name, *role.Namespace, err,
						)
					}
				}

				for _, roleBinding := range resources.RoleBindings {
					_, err := rr.kubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
						Apply(ctx, roleBinding, operatorutil.ApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := rr.kubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
								Delete(ctx, *roleBinding.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid role binding %s in namespace %s: %w",
									*roleBinding.Name, *roleBinding.Namespace, err,
								)
							}
							return operatorutil.ErrRequeueRequired
						}
						return fmt.Errorf(
							"failed to apply role binding %s in namespace %s: %w",
							*roleBinding.Name, *roleBinding.Namespace, err,
						)
					}
				}
			}

			return nil
		},
	)
}

func (rr *rbacReconciler) generateAutoApproveNodeCertificateRotationRBAC() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.NodeAutoApproveCertificateRotationClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.CSRAutoApprovalClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.NodesGroup),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (rr *rbacReconciler) generateAutoApproveNodeBootstrapTokenRBAC() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.NodeAutoApproveBootstrapClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.CSRAutoApprovalClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (rr *rbacReconciler) generateClusterAdminBindingRBAC() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.ClusterAdminsGroupAndClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind("ClusterRole").
				WithName("cluster-admin"),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.ClusterAdminsGroupAndClusterRoleBinding),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (rr *rbacReconciler) generateClusterInfoRBAC() (*RBACResources, error) {
	role := rbacv1ac.Role(clusterinfo.BootstrapSignerClusterRoleName, metav1.NamespacePublic).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithVerbs("get").
				WithResources("configmaps").
				WithResourceNames(bootstrapapi.ConfigMapClusterInfo),
		)

	roleBinding := rbacv1ac.RoleBinding(clusterinfo.BootstrapSignerClusterRoleName, metav1.NamespacePublic).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind(*role.Kind).
				WithName(*role.Name),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.UserKind).
				WithName(user.Anonymous),
		)

	return &RBACResources{
		Roles:        []*rbacv1ac.RoleApplyConfiguration{role},
		RoleBindings: []*rbacv1ac.RoleBindingApplyConfiguration{roleBinding},
	}, nil
}

func (rr *rbacReconciler) generateConfigMapRBAC(
	configMapName string,
	roleName string,
) (*RBACResources, error) {
	role := rbacv1ac.Role(roleName, metav1.NamespaceSystem).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithVerbs("get").
				WithResources("configmaps").
				WithResourceNames(configMapName),
		)

	roleBinding := rbacv1ac.RoleBinding(roleName, metav1.NamespaceSystem).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind(*role.Kind).
				WithName(*role.Name),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.NodesGroup),
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		Roles:        []*rbacv1ac.RoleApplyConfiguration{role},
		RoleBindings: []*rbacv1ac.RoleBindingApplyConfiguration{roleBinding},
	}, nil
}

func (rr *rbacReconciler) generateKubeletConfigRBAC() (*RBACResources, error) {
	return rr.generateConfigMapRBAC(
		konstants.KubeletBaseConfigurationConfigMap,
		konstants.KubeletBaseConfigMapRole,
	)
}

func (rr *rbacReconciler) generateKubeletKubeadmConfigRBAC() (*RBACResources, error) {
	return rr.generateConfigMapRBAC(
		konstants.KubeadmConfigConfigMap,
		uploadconfig.NodesKubeadmConfigClusterRoleName,
	)
}

func (rr *rbacReconciler) generateNodeBootstrapGetNodesRBAC() (*RBACResources, error) {
	clusterRole := rbacv1ac.ClusterRole(konstants.GetNodesClusterRoleName).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("nodes").
				WithVerbs("get"),
		)

	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.GetNodesClusterRoleName).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind(*clusterRole.Kind).
				WithName(*clusterRole.Name),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		ClusterRoles:        []*rbacv1ac.ClusterRoleApplyConfiguration{clusterRole},
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (rr *rbacReconciler) generateNodeBootstrapTokenPostCSRsRBAC() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.NodeKubeletBootstrap).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.NodeBootstrapperClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}
