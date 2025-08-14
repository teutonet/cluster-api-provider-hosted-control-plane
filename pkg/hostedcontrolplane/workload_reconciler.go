package hostedcontrolplane

import (
	"context"
	"fmt"
	"path"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
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
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
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

var (
	konnectivityServiceAccountName      = "konnectivity-agent"
	konnectivityServiceAccountTokenName = "konnectivity-agent-token"
)

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

			content, err := operatorutil.ToYaml(&kubeletConfiguration)
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

func (wr *WorkloadClusterReconciler) ReconcileKonnectivityRBAC(
	ctx context.Context,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileKonnectivityRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(
				"konnectivity-agent",
				metav1.NamespaceSystem,
			)

			_, err := wr.kubernetesClient.CoreV1().
				ServiceAccounts(metav1.NamespaceSystem).
				Apply(ctx, serviceAccount, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply konnectivity agent service account: %w", err)
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding(KonnectivityServerAudience).
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

			_, err = wr.kubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, workloadApplyOptions)
			if err != nil {
				if apierrors.IsInvalid(err) {
					if err := wr.kubernetesClient.RbacV1().ClusterRoleBindings().
						Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{}); err != nil {
						return fmt.Errorf(
							"failed to delete invalid konnectivity agent cluster role binding %s: %w",
							*clusterRoleBinding.Name,
							err,
						)
					}
					return ErrRequeueRequired
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

func (wr *WorkloadClusterReconciler) ReconcileKonnectivityDaemonSet(
	ctx context.Context,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileKonnectivityDaemonSet",
		func(ctx context.Context, span trace.Span) error {
			serviceAccountTokenVolume := corev1ac.Volume().
				WithName(konnectivityServiceAccountName).
				WithProjected(corev1ac.ProjectedVolumeSource().
					WithSources(
						corev1ac.VolumeProjection().
							WithServiceAccountToken(corev1ac.ServiceAccountTokenProjection().
								WithAudience(KonnectivityServerAudience).
								WithExpirationSeconds(3600).
								WithPath(konnectivityServiceAccountTokenName),
							),
					).
					WithDefaultMode(420),
				)
			serviceAccountTokenVolumeMount := corev1ac.VolumeMount().
				WithName(*serviceAccountTokenVolume.Name).
				WithMountPath(serviceaccount.DefaultAPITokenMountPath).
				WithReadOnly(true)
			healthPort := corev1ac.ContainerPort().
				WithName("health").
				WithContainerPort(8134).
				WithProtocol(corev1.ProtocolTCP)

			template := corev1ac.PodTemplateSpec().
				WithLabels(names.GetControlPlaneLabels(cluster, "konnectivity")).
				WithSpec(corev1ac.PodSpec().
					WithServiceAccountName(konnectivityServiceAccountName).
					WithTolerations(
						corev1ac.Toleration().
							WithKey("CriticalAddonsOnly").
							WithOperator(corev1.TolerationOpExists),
					).
					WithContainers(
						corev1ac.Container().
							WithName("konnectivity-agent").
							WithImage("registry.k8s.io/kas-network-proxy/proxy-agent:v0.33.0").
							WithImagePullPolicy(corev1.PullAlways).
							WithArgs(wr.buildKonnectivityClientArgs(cluster, serviceAccountTokenVolumeMount, healthPort)...).
							WithPorts(healthPort).
							WithVolumeMounts(serviceAccountTokenVolumeMount),
					).
					WithVolumes(serviceAccountTokenVolume),
				)

			template, err := util.SetChecksumAnnotations(ctx, wr.kubernetesClient, cluster.Namespace, template)
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

			_, err = wr.kubernetesClient.AppsV1().DaemonSets(metav1.NamespaceSystem).
				Apply(ctx, daemonSet, workloadApplyOptions)
			return errorsUtil.IfErrErrorf("failed to apply konnectivity agent daemonset: %w", err)
		},
	)
}

func (wr *WorkloadClusterReconciler) buildKonnectivityClientArgs(
	cluster *capiv1.Cluster,
	serviceAccountTokenVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	healthPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	args := map[string]string{
		"admin-server-port":  "8133",
		"ca-cert":            path.Join(*serviceAccountTokenVolumeMount.MountPath, corev1.ServiceAccountRootCAKey),
		"health-server-port": fmt.Sprintf("%d", healthPort.ContainerPort),
		"logtostderr":        "true",
		"proxy-server-host":  names.GetKonnectivityServerHost(cluster),
		"proxy-server-port":  fmt.Sprintf("%d", KonnectivityServicePort),
		"service-account-token-path": path.Join(
			*serviceAccountTokenVolumeMount.MountPath,
			konnectivityServiceAccountTokenName,
		),
	}
	return operatorutil.ArgsToSlice(args)
}
