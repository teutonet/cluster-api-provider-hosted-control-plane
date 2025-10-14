package rbac

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/bootstraptoken/clusterinfo"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/uploadconfig"
)

type RBACReconciler interface {
	ReconcileRBAC(ctx context.Context) (string, error)
}

func NewRBACReconciler(
	managementClusterClient *alias.WorkloadClusterClient,
	kubeadmKubeletConfigMapNamespace string,
	kubeadmConfigConfigMapName string,
	kubeletConfigMapName string,
	clusterInfoConfigMapNamespace string,
	clusterInfoConfigMapName string,
) RBACReconciler {
	return &rbacReconciler{
		managementClusterClient:          managementClusterClient,
		tracer:                           tracing.GetTracer("rbac"),
		kubeadmKubeletConfigMapNamespace: kubeadmKubeletConfigMapNamespace,
		kubeadmConfigConfigMapName:       kubeadmConfigConfigMapName,
		kubeletConfigMapName:             kubeletConfigMapName,
		clusterInfoConfigMapNamespace:    clusterInfoConfigMapNamespace,
		clusterInfoConfigMapName:         clusterInfoConfigMapName,
	}
}

type rbacReconciler struct {
	managementClusterClient          *alias.WorkloadClusterClient
	tracer                           string
	kubeadmConfigConfigMapName       string
	kubeletConfigMapName             string
	kubeadmKubeletConfigMapNamespace string
	clusterInfoConfigMapNamespace    string
	clusterInfoConfigMapName         string
}

var _ RBACReconciler = &rbacReconciler{}

type RBACResources struct {
	ClusterRoles        []*rbacv1ac.ClusterRoleApplyConfiguration
	ClusterRoleBindings []*rbacv1ac.ClusterRoleBindingApplyConfiguration
	Roles               []*rbacv1ac.RoleApplyConfiguration
	RoleBindings        []*rbacv1ac.RoleBindingApplyConfiguration
}

func (rr *rbacReconciler) ReconcileRBAC(ctx context.Context) (string, error) {
	return tracing.WithSpan(ctx, rr.tracer, "ReconcileRBAC",
		func(ctx context.Context, span trace.Span) (string, error) {
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

			logger := logr.FromContextAsSlogLogger(ctx)
			for _, phase := range phases {
				notReadyReasons, err := tracing.WithSpan(ctx, rr.tracer,
					phase.Name,
					func(ctx context.Context, _ trace.Span) ([]string, error) {
						ctx = logr.NewContextWithSlogLogger(ctx, logger.With("phase", phase.Name))
						var notReadyReasons []string
						resources, err := phase.Generate()
						if err != nil {
							return nil, fmt.Errorf("failed to generate %s: %w", phase.Name, err)
						}
						for _, clusterRole := range resources.ClusterRoles {
							clusterRoleInterface := rr.managementClusterClient.RbacV1().ClusterRoles()
							_, err := clusterRoleInterface.
								Apply(ctx, clusterRole, operatorutil.ApplyOptions)
							if err != nil {
								if !apierrors.IsInvalid(err) {
									return nil, fmt.Errorf(
										"failed to apply cluster role %s: %w",
										*clusterRole.Name,
										err,
									)
								}
								if err := clusterRoleInterface.
									Delete(ctx, *clusterRole.Name, metav1.DeleteOptions{}); err != nil {
									return nil, fmt.Errorf(
										"failed to delete invalid cluster role %s: %w",
										*clusterRole.Name, err,
									)
								}
								notReadyReasons = append(
									notReadyReasons,
									fmt.Sprintf("cluster role %s needs to be recreated", *clusterRole.Name),
								)
							}
						}

						for _, clusterRoleBinding := range resources.ClusterRoleBindings {
							clusterRoleBindingInterface := rr.managementClusterClient.RbacV1().ClusterRoleBindings()
							_, err := clusterRoleBindingInterface.
								Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
							if err != nil {
								if !apierrors.IsInvalid(err) {
									return nil, fmt.Errorf(
										"failed to apply cluster role binding %s: %w", *clusterRoleBinding.Name, err,
									)
								}
								if err := clusterRoleBindingInterface.
									Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
									return nil, fmt.Errorf(
										"failed to delete invalid cluster role binding %s: %w",
										*clusterRoleBinding.Name, err,
									)
								}
								notReadyReasons = append(
									notReadyReasons,
									fmt.Sprintf(
										"cluster role binding %s needs to be recreated",
										*clusterRoleBinding.Name,
									),
								)
							}
						}

						for _, role := range resources.Roles {
							roleInterface := rr.managementClusterClient.RbacV1().Roles(*role.Namespace)
							_, err := roleInterface.
								Apply(ctx, role, operatorutil.ApplyOptions)
							if err != nil {
								if !apierrors.IsInvalid(err) {
									return nil, fmt.Errorf(
										"failed to apply role %s in namespace %s: %w",
										*role.Name, *role.Namespace, err,
									)
								}
								if err := roleInterface.
									Delete(ctx, *role.Name, metav1.DeleteOptions{}); err != nil {
									return nil, fmt.Errorf(
										"failed to delete invalid role %s in namespace %s: %w",
										*role.Name, *role.Namespace, err,
									)
								}
								notReadyReasons = append(
									notReadyReasons,
									fmt.Sprintf(
										"role %s in namespace %s needs to be recreated",
										*role.Name,
										*role.Namespace,
									),
								)
							}
						}

						for _, roleBinding := range resources.RoleBindings {
							roleBindingInterface := rr.managementClusterClient.RbacV1().
								RoleBindings(*roleBinding.Namespace)
							_, err := roleBindingInterface.
								Apply(ctx, roleBinding, operatorutil.ApplyOptions)
							if err != nil {
								if !apierrors.IsInvalid(err) {
									return nil, fmt.Errorf(
										"failed to apply role binding %s in namespace %s: %w",
										*roleBinding.Name, *roleBinding.Namespace, err,
									)
								}
								if err := roleBindingInterface.
									Delete(ctx, *roleBinding.Name, metav1.DeleteOptions{}); err != nil {
									return nil, fmt.Errorf(
										"failed to delete invalid role binding %s in namespace %s: %w",
										*roleBinding.Name, *roleBinding.Namespace, err,
									)
								}
								notReadyReasons = append(
									notReadyReasons,
									fmt.Sprintf(
										"role binding %s in namespace %s needs to be recreated",
										*roleBinding.Name,
										*roleBinding.Namespace,
									),
								)
							}
						}

						return notReadyReasons, nil
					},
				)
				if err != nil {
					return "", err
				}
				if len(notReadyReasons) > 0 {
					return strings.Join(notReadyReasons, ","), nil
				}
			}

			return "", nil
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
	role := rbacv1ac.Role(clusterinfo.BootstrapSignerClusterRoleName, rr.clusterInfoConfigMapNamespace).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithVerbs("get").
				WithResources("configmaps").
				WithResourceNames(rr.clusterInfoConfigMapName),
		)

	roleBinding := rbacv1ac.RoleBinding(clusterinfo.BootstrapSignerClusterRoleName, rr.clusterInfoConfigMapNamespace).
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
		rr.kubeletConfigMapName,
		konstants.KubeletBaseConfigMapRole,
	)
}

func (rr *rbacReconciler) generateKubeletKubeadmConfigRBAC() (*RBACResources, error) {
	return rr.generateConfigMapRBAC(
		rr.kubeadmConfigConfigMapName,
		uploadconfig.NodesKubeadmConfigClusterRoleName,
	)
}

func (rr *rbacReconciler) generateNodeBootstrapGetNodesRBAC() (*RBACResources, error) {
	clusterRole := rbacv1ac.ClusterRole(konstants.GetNodesClusterRoleName).WithRules(
		rbacv1ac.PolicyRule().
			WithAPIGroups("").
			WithResources("nodes").
			WithVerbs("get"),
	)

	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.GetNodesClusterRoleName).WithRoleRef(rbacv1ac.RoleRef().
		WithAPIGroup(rbacv1.GroupName).
		WithKind(*clusterRole.Kind).
		WithName(*clusterRole.Name),
	).
		WithSubjects(rbacv1ac.Subject().
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
