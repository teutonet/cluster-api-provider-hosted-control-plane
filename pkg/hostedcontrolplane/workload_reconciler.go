package hostedcontrolplane

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/bootstraptoken/clusterinfo"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	workloadClusterReconcilerTracer = "WorkloadClusterReconciler"
)

type WorkloadClusterReconciler struct {
	kubernetesClient kubernetes.Interface
}

type RBACResources struct {
	ClusterRoles        []*rbacv1ac.ClusterRoleApplyConfiguration
	ClusterRoleBindings []*rbacv1ac.ClusterRoleBindingApplyConfiguration
	Roles               []*rbacv1ac.RoleApplyConfiguration
	RoleBindings        []*rbacv1ac.RoleBindingApplyConfiguration
}

var workloadApplyOptions = metav1.ApplyOptions{
	FieldManager: "workload-cluster-reconciler",
	Force:        true,
}

//noling:lll // urls are long 🤷

// ReconcileWorkloadRBAC mimics kuebadm phases, e.g.
// - https://github.com/kubernetes/kubernetes/blob/6f06cd6e05704a9a7b18e74a048a297e5bdb5498/cmd/kubeadm/app/cmd/phases/init/bootstraptoken.go#L65
func (wr *WorkloadClusterReconciler) ReconcileWorkloadRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileWorkloadRBAC",
		func(ctx context.Context, span trace.Span) error {
			phases := []struct {
				Name     string
				Generate func() (*RBACResources, error)
			}{
				{
					Name:     "NodeBootstrapGetNodes",
					Generate: wr.generateNodeBootstrapGetNodes,
				},
				{
					Name:     "NodeBootstrapTokenPostCSRs",
					Generate: wr.generateNodeBootstrapTokenPostCSRs,
				},
				{
					Name:     "AutoApproveNodeBootstrapToken",
					Generate: wr.generateAutoApproveNodeBootstrapToken,
				},
				{
					Name:     "AutoApproveNodeCertificateRotationBAC",
					Generate: wr.generateAutoApproveNodeCertificateRotation,
				},
				{
					Name:     "ClusterInfo",
					Generate: wr.generateClusterInfo,
				},
				{
					Name:     "KubeletConfig",
					Generate: wr.generateKubeletConfig,
				},
				{
					Name:     "ClusterAdminBinding",
					Generate: wr.generateClusterAdminBinding,
				},
			}

			for _, phase := range phases {
				resources, err := phase.Generate()
				if err != nil {
					return fmt.Errorf("failed to generate kubeadm phase %s: %w", phase.Name, err)
				}
				for _, clusterRole := range resources.ClusterRoles {
					_, err := wr.kubernetesClient.RbacV1().ClusterRoles().
						Apply(ctx, clusterRole, workloadApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if deleteErr := wr.kubernetesClient.RbacV1().ClusterRoles().Delete(ctx, *clusterRole.Name, metav1.DeleteOptions{}); deleteErr != nil {
								return fmt.Errorf("failed to delete invalid cluster role %s: %w", *clusterRole.Name, deleteErr)
							}
							if _, retryErr := wr.kubernetesClient.RbacV1().ClusterRoles().Apply(ctx, clusterRole, workloadApplyOptions); retryErr != nil {
								return fmt.Errorf("failed to apply cluster role %s after deletion: %w", *clusterRole.Name, retryErr)
							}
						} else {
							return fmt.Errorf("failed to apply cluster role %s: %w", *clusterRole.Name, err)
						}
					}
				}

				for _, clusterRoleBinding := range resources.ClusterRoleBindings {
					_, err := wr.kubernetesClient.RbacV1().ClusterRoleBindings().
						Apply(ctx, clusterRoleBinding, workloadApplyOptions)
					if err != nil {
						return fmt.Errorf("failed to apply cluster role binding %s: %w", *clusterRoleBinding.Name, err)
					}
				}

				for _, role := range resources.Roles {
					_, err := wr.kubernetesClient.RbacV1().Roles(*role.Namespace).
						Apply(ctx, role, workloadApplyOptions)
					if err != nil {
						return fmt.Errorf(
							"failed to apply role %s in namespace %s: %w",
							*role.Name,
							*role.Namespace,
							err,
						)
					}
				}

				for _, roleBinding := range resources.RoleBindings {
					_, err := wr.kubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
						Apply(ctx, roleBinding, workloadApplyOptions)
					if err != nil {
						return fmt.Errorf(
							"failed to apply role binding %s in namespace %s: %w",
							*roleBinding.Name,
							*roleBinding.Namespace,
							err,
						)
					}
				}
			}

			return nil
		},
	)
}

func (wr *WorkloadClusterReconciler) generateAutoApproveNodeCertificateRotation() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.NodeAutoApproveCertificateRotationClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(v1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.CSRAutoApprovalClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.NodesGroup),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) generateAutoApproveNodeBootstrapToken() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.NodeAutoApproveBootstrapClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(v1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.CSRAutoApprovalClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) generateClusterAdminBinding() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.ClusterAdminsGroupAndClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(v1.GroupName).
				WithKind("ClusterRole").
				WithName("cluster-admin"),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.ClusterAdminsGroupAndClusterRoleBinding),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) generateClusterInfo() (*RBACResources, error) {
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
				WithAPIGroup(v1.GroupName).
				WithKind("Role").
				WithName(clusterinfo.BootstrapSignerClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.UserKind).
				WithName(user.Anonymous),
		)

	return &RBACResources{
		Roles:        []*rbacv1ac.RoleApplyConfiguration{role},
		RoleBindings: []*rbacv1ac.RoleBindingApplyConfiguration{roleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) generateKubeletConfig() (*RBACResources, error) {
	role := rbacv1ac.Role(konstants.KubeletBaseConfigMapRole, metav1.NamespaceSystem).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithVerbs("get").
				WithResources("configmaps").
				WithResourceNames(konstants.KubeletBaseConfigurationConfigMap),
		)

	roleBinding := rbacv1ac.RoleBinding(konstants.KubeletBaseConfigMapRole, metav1.NamespaceSystem).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(v1.GroupName).
				WithKind("Role").
				WithName(konstants.KubeletBaseConfigMapRole),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.NodesGroup),
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		Roles:        []*rbacv1ac.RoleApplyConfiguration{role},
		RoleBindings: []*rbacv1ac.RoleBindingApplyConfiguration{roleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) generateNodeBootstrapGetNodes() (*RBACResources, error) {
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
				WithAPIGroup(v1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.GetNodesClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		ClusterRoles:        []*rbacv1ac.ClusterRoleApplyConfiguration{clusterRole},
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) generateNodeBootstrapTokenPostCSRs() (*RBACResources, error) {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.NodeKubeletBootstrap).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(v1.GroupName).
				WithKind("ClusterRole").
				WithName(konstants.NodeBootstrapperClusterRoleName),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(v1.GroupKind).
				WithName(konstants.NodeBootstrapTokenAuthGroup),
		)

	return &RBACResources{
		ClusterRoleBindings: []*rbacv1ac.ClusterRoleBindingApplyConfiguration{clusterRoleBinding},
	}, nil
}

func (wr *WorkloadClusterReconciler) ReconcileClusterInfoConfigMap(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	cluster *capiv1.Cluster,
) error {
	caSecret, err := kubernetesClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CA secret: %w", err)
	}
	kubeconfig := &api.Config{
		Clusters: map[string]*api.Cluster{
			"": {
				Server:                   cluster.Spec.ControlPlaneEndpoint.String(),
				CertificateAuthorityData: caSecret.Data[corev1.TLSCertKey],
			},
		},
	}
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
	}

	configMap := corev1ac.ConfigMap(bootstrapapi.ConfigMapClusterInfo, metav1.NamespacePublic).
		WithData(map[string]string{
			bootstrapapi.KubeConfigKey: string(kubeconfigBytes),
		})

	_, err = wr.kubernetesClient.CoreV1().
		ConfigMaps(metav1.NamespacePublic).
		Apply(ctx, configMap, workloadApplyOptions)
	return errorsUtil.IfErrErrorf("failed to apply cluster info configmap: %w", err)
}
