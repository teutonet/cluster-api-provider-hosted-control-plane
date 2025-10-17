package kubeproxy

import (
	"context"
	"fmt"
	"path"

	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	"github.com/go-logr/logr"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	componentbaseconfigalpha1 "k8s.io/component-base/config/v1alpha1"
	kubeproxyv1alpha1 "k8s.io/kube-proxy/config/v1alpha1"
	"k8s.io/kubernetes/cmd/kubeadm/app/componentconfigs"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/addons/proxy"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type KubeProxyReconciler interface {
	ReconcileKubeProxy(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
		deleteResource bool,
	) (string, error)
}

func NewKubeProxyReconciler(
	managementClusterClient *alias.WorkloadClusterClient,
	ciliumClient ciliumclient.Interface,
	podCIDR string,
) KubeProxyReconciler {
	return &kubeProxyReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			WorkloadClusterClient: managementClusterClient,
			CiliumClient:          ciliumClient,
			Tracer:                tracing.GetTracer("kubeproxy"),
		},
		podCIDR:                 podCIDR,
		kubeProxyNamespace:      metav1.NamespaceSystem,
		kubeProxyServiceAccount: proxy.KubeProxyServiceAccountName,
		kubeProxyConfigMapName:  konstants.KubeProxyConfigMap,
		kubeProxyConfigMapKey:   konstants.KubeProxyConfigMapKey,
		kubeProxyLabels: map[string]string{
			"k8s-app":                       konstants.KubeProxy,
			"kubernetes.io/cluster-service": "true",
		},
		kubeProxyConfigMountPath: "/var/lib/kube-proxy",
	}
}

type kubeProxyReconciler struct {
	reconcilers.WorkloadResourceReconciler
	podCIDR                  string
	kubeProxyNamespace       string
	kubeProxyServiceAccount  string
	kubeProxyConfigMapName   string
	kubeProxyConfigMapKey    string
	kubeProxyLabels          map[string]string
	kubeProxyConfigMountPath string
}

var _ KubeProxyReconciler = &kubeProxyReconciler{}

func (kr *kubeProxyReconciler) ReconcileKubeProxy(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	deleteResource bool,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "ReconcileKubeProxy",
		func(ctx context.Context, span trace.Span) (string, error) {
			phases := []struct {
				Name      string
				Reconcile func(ctx context.Context, deleteResource bool) (string, error)
			}{
				{
					Name: "ConfigMap",
					Reconcile: func(ctx context.Context, deleteResource bool) (string, error) {
						return "", kr.reconcileKubeProxyConfigMap(ctx, cluster, deleteResource)
					},
				},
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context, deleteResource bool) (string, error) {
						return "", kr.reconcileKubeProxyRBAC(ctx, deleteResource)
					},
				},
				{
					Name: "DaemonSet",
					Reconcile: func(ctx context.Context, deleteResource bool) (string, error) {
						return kr.reconcileKubeProxyDaemonSet(ctx, hostedControlPlane, deleteResource)
					},
				},
			}

			logger := logr.FromContextAsSlogLogger(ctx)
			for _, phase := range phases {
				notReadyReason, err := tracing.WithSpan(ctx, kr.Tracer, phase.Name,
					func(ctx context.Context, _ trace.Span) (string, error) {
						return phase.Reconcile(
							logr.NewContextWithSlogLogger(ctx, logger.With("phase", phase.Name)),
							deleteResource,
						)
					},
				)
				if err != nil {
					return "", fmt.Errorf("failed to reconcile kube-proxy phase %s: %w", phase.Name, err)
				} else if notReadyReason != "" {
					return notReadyReason, nil
				}
			}

			return "", nil
		},
	)
}

func (kr *kubeProxyReconciler) reconcileKubeProxyRBAC(
	ctx context.Context,
	deleteResource bool,
) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeProxyRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(kr.kubeProxyServiceAccount, kr.kubeProxyNamespace)

			serviceAccountInterface := kr.WorkloadClusterClient.CoreV1().
				ServiceAccounts(*serviceAccount.Namespace)
			if deleteResource {
				err := serviceAccountInterface.
					Delete(ctx, *serviceAccount.Name, metav1.DeleteOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete kube-proxy service account: %w", err)
				}
			} else {
				_, err := serviceAccountInterface.
					Apply(ctx, serviceAccount, operatorutil.ApplyOptions)
				if err != nil {
					return fmt.Errorf("failed to apply kube-proxy service account: %w", err)
				}
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.KubeProxyClusterRoleBindingName).
				WithRoleRef(rbacv1ac.RoleRef().
					WithAPIGroup(rbacv1.GroupName).
					WithKind("ClusterRole").
					WithName(konstants.KubeProxyClusterRoleName),
				).
				WithSubjects(rbacv1ac.Subject().
					WithKind(*serviceAccount.Kind).
					WithName(*serviceAccount.Name).
					WithNamespace(*serviceAccount.Namespace),
				)

			clusterRoleBindingInterface := kr.WorkloadClusterClient.RbacV1().ClusterRoleBindings()
			if deleteResource {
				err := clusterRoleBindingInterface.
					Delete(ctx, *clusterRoleBinding.Name, metav1.DeleteOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete kube-proxy cluster role binding: %w", err)
				}
			} else {
				_, err := clusterRoleBindingInterface.
					Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
				if err != nil {
					return fmt.Errorf("failed to apply kube-proxy cluster role binding: %w", err)
				}
			}

			role := rbacv1ac.Role(proxy.KubeProxyConfigMapRoleName, kr.kubeProxyNamespace).
				WithRules(
					rbacv1ac.PolicyRule().
						WithVerbs("get").
						WithAPIGroups("").
						WithResources("configmaps").
						WithResourceNames(kr.kubeProxyConfigMapName),
				)

			roleInterface := kr.WorkloadClusterClient.RbacV1().Roles(*role.Namespace)
			if deleteResource {
				err := roleInterface.
					Delete(ctx, *role.Name, metav1.DeleteOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete kube-proxy role: %w", err)
				}
			} else {
				_, err := roleInterface.
					Apply(ctx, role, operatorutil.ApplyOptions)
				if err != nil {
					return fmt.Errorf("failed to apply kube-proxy role: %w", err)
				}
			}

			roleBinding := rbacv1ac.RoleBinding(proxy.KubeProxyConfigMapRoleName, kr.kubeProxyNamespace).
				WithRoleRef(rbacv1ac.RoleRef().
					WithAPIGroup(rbacv1.GroupName).
					WithKind(*role.Kind).
					WithName(*role.Name),
				).
				WithSubjects(rbacv1ac.Subject().
					WithKind(rbacv1.GroupKind).
					WithName(konstants.NodeBootstrapTokenAuthGroup),
				)

			roleBindingInterface := kr.WorkloadClusterClient.RbacV1().RoleBindings(*roleBinding.Namespace)
			if deleteResource {
				err := roleBindingInterface.
					Delete(ctx, *roleBinding.Name, metav1.DeleteOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete kube-proxy role binding: %w", err)
				}
			} else {
				_, err := roleBindingInterface.
					Apply(ctx, roleBinding, operatorutil.ApplyOptions)
				if err != nil {
					return fmt.Errorf("failed to apply kube-proxy role binding: %w", err)
				}
			}

			return nil
		},
	)
}

func (kr *kubeProxyReconciler) reconcileKubeProxyConfigMap(
	ctx context.Context,
	cluster *capiv2.Cluster,
	deleteResource bool,
) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeProxyConfigMap",
		func(ctx context.Context, span trace.Span) error {
			kubeconfigFileName := "kubeconfig.conf"
			kubeconfig := &api.Config{
				Clusters: map[string]*api.Cluster{
					"default": {
						Server: fmt.Sprintf("https://%s", cluster.Spec.ControlPlaneEndpoint.String()),
						CertificateAuthority: path.Join(
							serviceaccount.DefaultAPITokenMountPath,
							corev1.ServiceAccountRootCAKey,
						),
					},
				},
				Contexts: map[string]*api.Context{
					"default": {
						Cluster:  "default",
						AuthInfo: "default",
					},
				},
				CurrentContext: "default",
				AuthInfos: map[string]*api.AuthInfo{
					"default": {
						TokenFile: path.Join(serviceaccount.DefaultAPITokenMountPath, corev1.ServiceAccountTokenKey),
					},
				},
			}

			kubeProxyConfig := kubeproxyv1alpha1.KubeProxyConfiguration{
				ClientConnection: componentbaseconfigalpha1.ClientConnectionConfiguration{
					Kubeconfig: path.Join(kr.kubeProxyConfigMountPath, kubeconfigFileName),
				},
				ClusterCIDR:        kr.podCIDR,
				MetricsBindAddress: "0.0.0.0:10249",
				NodePortAddresses:  []string{"primary"},
			}

			kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
			}

			kubeProxyConfigBytes, err := kubeadmutil.MarshalToYamlForCodecs(
				&kubeProxyConfig,
				kubeproxyv1alpha1.SchemeGroupVersion,
				componentconfigs.Codecs,
			)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to marshal kube-proxy config: %w", err)
			}

			return kr.ReconcileConfigmap(
				ctx,
				kr.kubeProxyNamespace,
				kr.kubeProxyConfigMapName,
				deleteResource,
				nil,
				map[string]string{
					kubeconfigFileName:       string(kubeconfigBytes),
					kr.kubeProxyConfigMapKey: string(kubeProxyConfigBytes),
				},
			)
		},
	)
}

func (kr *kubeProxyReconciler) reconcileKubeProxyDaemonSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	deleteResource bool,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "ReconcileKubeProxyDaemonSet",
		func(ctx context.Context, span trace.Span) (string, error) {
			kubeProxyConfigVolume := corev1ac.Volume().
				WithName("kube-proxy-config").
				WithConfigMap(corev1ac.ConfigMapVolumeSource().
					WithName(kr.kubeProxyConfigMapName),
				)
			xtablesLockVolume := corev1ac.Volume().
				WithName("xtables-lock").
				WithHostPath(corev1ac.HostPathVolumeSource().
					WithPath("/run/xtables.lock").
					WithType(corev1.HostPathFileOrCreate),
				)
			modulesVolume := corev1ac.Volume().
				WithName("lib-modules").
				WithHostPath(corev1ac.HostPathVolumeSource().
					WithPath("/lib/modules"),
				)

			kubeProxyConfigVolumeMount := corev1ac.VolumeMount().
				WithName(*kubeProxyConfigVolume.Name).
				WithMountPath(kr.kubeProxyConfigMountPath)
			xtablesLockVolumeMount := corev1ac.VolumeMount().
				WithName(*xtablesLockVolume.Name).
				WithMountPath("/run/xtables.lock").
				WithReadOnly(false)
			modulesVolumeMount := corev1ac.VolumeMount().
				WithName(*modulesVolume.Name).
				WithMountPath("/lib/modules").
				WithReadOnly(true)

			container := corev1ac.Container().
				WithName(konstants.KubeProxy).
				WithImage(operatorutil.ResolveKubeProxyImage(
					hostedControlPlane.Spec.KubeProxy.Image,
					hostedControlPlane.Spec.Version,
				)).
				WithImagePullPolicy(hostedControlPlane.Spec.KubeProxy.ImagePullPolicyOrDefault()).
				WithCommand("/usr/local/bin/kube-proxy").
				WithArgs(kr.buildArgs(ctx, hostedControlPlane, kubeProxyConfigVolumeMount)...).
				WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
					hostedControlPlane.Spec.KubeProxy.Resources,
				)).
				WithEnv(corev1ac.EnvVar().
					WithName("NODE_NAME").
					WithValueFrom(corev1ac.EnvVarSource().
						WithFieldRef(corev1ac.ObjectFieldSelector().
							WithFieldPath("spec.nodeName"),
						),
					),
				).
				WithVolumeMounts(kubeProxyConfigVolumeMount, xtablesLockVolumeMount, modulesVolumeMount)

			_, ready, err := kr.ReconcileDaemonSet(
				ctx,
				kr.kubeProxyNamespace,
				konstants.KubeProxy,
				deleteResource,
				reconcilers.PodOptions{
					ServiceAccountName: kr.kubeProxyServiceAccount,
					PriorityClassName:  "system-node-critical",
					HostNetwork:        true,
					DNSPolicy:          corev1.DNSDefault,
					Tolerations: []*corev1ac.TolerationApplyConfiguration{
						corev1ac.Toleration().
							WithOperator(corev1.TolerationOpExists),
					},
				},
				kr.kubeProxyLabels,
				map[int32][]networkpolicy.IngressNetworkPolicyTarget{},
				map[int32][]networkpolicy.EgressNetworkPolicyTarget{
					0: { // needs to proxy to any port in the whole cluster
						networkpolicy.NewClusterNetworkPolicyTarget(),
					},
					6443: {
						networkpolicy.NewAPIServerNetworkPolicyTarget(hostedControlPlane),
					},
				},
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{
						Root:                    true,
						ReadWriteRootFilesystem: true,
					}),
				},
				[]*corev1ac.VolumeApplyConfiguration{kubeProxyConfigVolume, xtablesLockVolume, modulesVolume},
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile kube-proxy daemonset: %w", err)
			}
			if !ready {
				return "kube-proxy daemonset not ready", nil
			}

			return "", nil
		},
	)
}

func (kr *kubeProxyReconciler) buildArgs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	kubeProxyConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration,
) []string {
	args := map[string]string{
		"config":            path.Join(*kubeProxyConfigVolumeMount.MountPath, kr.kubeProxyConfigMapKey),
		"hostname-override": "$(NODE_NAME)",
	}
	return operatorutil.ArgsToSlice(ctx, hostedControlPlane.Spec.KubeProxy.Args, args)
}
