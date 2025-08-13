package hostedcontrolplane

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	kubelettypes "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmv1beta4 "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta4"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/bootstraptoken/clusterinfo"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/uploadconfig"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/config"
	kubeletv1beta1 "k8s.io/kubernetes/pkg/kubelet/apis/config/v1beta1"
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
					Name:     "NodeBootstrapGetNodesRBAC",
					Generate: wr.generateNodeBootstrapGetNodesRBAC,
				},
				{
					Name:     "NodeBootstrapTokenPostCSRsRBAC",
					Generate: wr.generateNodeBootstrapTokenPostCSRsRBAC,
				},
				{
					Name:     "AutoApproveNodeBootstrapTokenRBAC",
					Generate: wr.generateAutoApproveNodeBootstrapTokenRBAC,
				},
				{
					Name:     "AutoApproveNodeCertificateRotationBAC",
					Generate: wr.generateAutoApproveNodeCertificateRotationRBAC,
				},
				{
					Name:     "ClusterInfoRBAC",
					Generate: wr.generateClusterInfoRBAC,
				},
				{
					Name:     "KubeletConfigRBAC",
					Generate: wr.generateKubeletConfigRBAC,
				},
				{
					Name:     "KubeletKubeadmConfigRBAC",
					Generate: wr.generateKubeletKubeadmConfigRBAC,
				},
				{
					Name:     "ClusterAdminBindingRBAC",
					Generate: wr.generateClusterAdminBindingRBAC,
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
							if err := wr.kubernetesClient.RbacV1().ClusterRoles().
								Delete(ctx, *clusterRole.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid cluster role %s: %w",
									*clusterRole.Name, err,
								)
							}
							return ErrRequeueRequired
						}
						return fmt.Errorf("failed to apply cluster role %s: %w", *clusterRole.Name, err)
					}
				}

				for _, clusterRoleBinding := range resources.ClusterRoleBindings {
					_, err := wr.kubernetesClient.RbacV1().ClusterRoleBindings().
						Apply(ctx, clusterRoleBinding, workloadApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := wr.kubernetesClient.RbacV1().ClusterRoleBindings().
								Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid cluster role binding %s: %w",
									*clusterRoleBinding.Name, err,
								)
							}
							return ErrRequeueRequired
						}
						return fmt.Errorf("failed to apply cluster role binding %s: %w", *clusterRoleBinding.Name, err)
					}
				}

				for _, role := range resources.Roles {
					_, err := wr.kubernetesClient.RbacV1().Roles(*role.Namespace).
						Apply(ctx, role, workloadApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := wr.kubernetesClient.RbacV1().Roles(*role.Namespace).
								Delete(ctx, *role.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid role %s in namespace %s: %w",
									*role.Name, *role.Namespace, err,
								)
							}
							return ErrRequeueRequired
						}
						return fmt.Errorf(
							"failed to apply role %s in namespace %s: %w",
							*role.Name, *role.Namespace, err,
						)
					}
				}

				for _, roleBinding := range resources.RoleBindings {
					_, err := wr.kubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
						Apply(ctx, roleBinding, workloadApplyOptions)
					if err != nil {
						if apierrors.IsInvalid(err) {
							if err := wr.kubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
								Delete(ctx, *roleBinding.Name, metav1.DeleteOptions{}); err != nil {
								return fmt.Errorf(
									"failed to delete invalid role binding %s in namespace %s: %w",
									*roleBinding.Name, *roleBinding.Namespace, err,
								)
							}
							return ErrRequeueRequired
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

func (wr *WorkloadClusterReconciler) generateAutoApproveNodeCertificateRotationRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) generateAutoApproveNodeBootstrapTokenRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) generateClusterAdminBindingRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) generateClusterInfoRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) generateKubeletConfigRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) generateKubeletKubeadmConfigRBAC() (*RBACResources, error) {
	role := rbacv1ac.Role(uploadconfig.NodesKubeadmConfigClusterRoleName, metav1.NamespaceSystem).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithVerbs("get").
				WithResources("configmaps").
				WithResourceNames(konstants.KubeadmConfigConfigMap),
		)

	roleBinding := rbacv1ac.RoleBinding(uploadconfig.NodesKubeadmConfigClusterRoleName, metav1.NamespaceSystem).
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

func (wr *WorkloadClusterReconciler) generateNodeBootstrapGetNodesRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) generateNodeBootstrapTokenPostCSRsRBAC() (*RBACResources, error) {
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

func (wr *WorkloadClusterReconciler) ReconcileClusterInfoConfigMap(
	ctx context.Context,
	managementClient kubernetes.Interface,
	cluster *capiv1.Cluster,
) error {
	caSecret, err := managementClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CA secret: %w", err)
	}
	kubeconfig := &api.Config{
		Clusters: map[string]*api.Cluster{
			"": {
				Server:                   fmt.Sprintf("https://%s", cluster.Spec.ControlPlaneEndpoint.String()),
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

func (wr *WorkloadClusterReconciler) ReconcileKubeadmConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileKubeadmConfig",
		func(ctx context.Context, span trace.Span) error {
			initConfiguration, err := config.DefaultedStaticInitConfiguration()
			if err != nil {
				return fmt.Errorf("failed to get defaulted static init configuration: %w", err)
			}
			conf := initConfiguration.ClusterConfiguration
			conf.Networking = kubeadm.Networking{
				DNSDomain:     "cluster.local",
				PodSubnet:     "192.168.0.0/16",
				ServiceSubnet: "10.96.0.0/12",
			}
			conf.KubernetesVersion = hostedControlPlane.Spec.Version
			conf.ControlPlaneEndpoint = cluster.Spec.ControlPlaneEndpoint.String()
			conf.ClusterName = cluster.Name

			clusterConfiguration, err := config.MarshalKubeadmConfigObject(&conf, kubeadmv1beta4.SchemeGroupVersion)
			if err != nil {
				return fmt.Errorf("failed to marshal cluster configuration: %w", err)
			}
			configMap := corev1ac.ConfigMap(konstants.KubeadmConfigConfigMap, metav1.NamespaceSystem).
				WithData(
					map[string]string{
						konstants.ClusterConfigurationKind: string(clusterConfiguration),
					},
				)

			_, err = wr.kubernetesClient.CoreV1().
				ConfigMaps(metav1.NamespaceSystem).
				Apply(ctx, configMap, workloadApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply kubeadm config configmap: %w", err)
		},
	)
}

func (wr *WorkloadClusterReconciler) ReconcileKubeletConfig(
	ctx context.Context,
	_ *v1alpha1.HostedControlPlane,
	_ *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileKubeadmConfig",
		func(ctx context.Context, span trace.Span) error {
			var kubeletConfiguration kubelettypes.KubeletConfiguration

			kubeletv1beta1.SetDefaults_KubeletConfiguration(&kubeletConfiguration)

			kubeletConfiguration.APIVersion = kubelettypes.SchemeGroupVersion.String()
			kubeletConfiguration.Kind = "KubeletConfiguration"
			kubeletConfiguration.Authentication.X509.ClientCAFile = "/etc/kubernetes/pki/ca.crt"
			kubeletConfiguration.CgroupDriver = konstants.CgroupDriverSystemd
			kubeletConfiguration.ClusterDNS = []string{"10.96.0.10"}
			kubeletConfiguration.ClusterDomain = "cluster.local"
			kubeletConfiguration.RotateCertificates = true
			kubeletConfiguration.StaticPodPath = kubeadmv1beta4.DefaultManifestsDir
			kubeletConfiguration.Logging.FlushFrequency.SerializeAsString = false
			kubeletConfiguration.ResolverConfig = nil

			content, err := util.ToYaml(&kubeletConfiguration)
			if err != nil {
				return fmt.Errorf("failed to marshal kubelet configuration: %w", err)
			}

			configMap := corev1ac.ConfigMap(konstants.KubeletBaseConfigurationConfigMap, metav1.NamespaceSystem).
				WithData(
					map[string]string{
						konstants.KubeletBaseConfigurationConfigMapKey: content.String(),
					},
				)

			_, err = wr.kubernetesClient.CoreV1().
				ConfigMaps(metav1.NamespaceSystem).
				Apply(ctx, configMap, workloadApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply kubeadm config configmap: %w", err)
		},
	)
}
