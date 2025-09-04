package kubeproxy

import (
	"context"
	"fmt"
	"path"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type KubeProxyReconciler interface {
	ReconcileKubeProxy(
		ctx context.Context,
		cluster *capiv1.Cluster,
		hostedControlPlane *v1alpha1.HostedControlPlane,
	) (string, error)
}

func NewKubeProxyReconciler(
	kubernetesClient *alias.WorkloadClusterClient,
	podCIDR string,
) KubeProxyReconciler {
	return &kubeProxyReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			KubernetesClient: kubernetesClient,
			Tracer:           tracing.GetTracer("kubeproxy"),
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
	cluster *capiv1.Cluster,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (string, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "ReconcileKubeProxy",
		func(ctx context.Context, span trace.Span) (string, error) {
			phases := []struct {
				Name      string
				Reconcile func(context.Context) (string, error)
			}{
				{
					Name: "ConfigMap",
					Reconcile: func(ctx context.Context) (string, error) {
						return "", kr.reconcileKubeProxyConfigMap(ctx, cluster)
					},
				},
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context) (string, error) {
						return "", kr.reconcileKubeProxyRBAC(ctx)
					},
				},
				{
					Name: "DaemonSet",
					Reconcile: func(ctx context.Context) (string, error) {
						return kr.reconcileKubeProxyDaemonSet(ctx, hostedControlPlane)
					},
				},
			}

			for _, phase := range phases {
				if notReadyReason, err := phase.Reconcile(ctx); err != nil {
					return "", fmt.Errorf("failed to reconcile kube-proxy phase %s: %w", phase.Name, err)
				} else if notReadyReason != "" {
					return notReadyReason, nil
				}
			}

			return "", nil
		},
	)
}

func (kr *kubeProxyReconciler) reconcileKubeProxyRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeProxyRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(kr.kubeProxyServiceAccount, kr.kubeProxyNamespace)

			_, err := kr.KubernetesClient.CoreV1().
				ServiceAccounts(*serviceAccount.Namespace).
				Apply(ctx, serviceAccount, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy service account: %w", err)
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

			_, err = kr.KubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy cluster role binding: %w", err)
			}

			role := rbacv1ac.Role(proxy.KubeProxyConfigMapRoleName, kr.kubeProxyNamespace).
				WithRules(
					rbacv1ac.PolicyRule().
						WithVerbs("get").
						WithAPIGroups("").
						WithResources("configmaps").
						WithResourceNames(kr.kubeProxyConfigMapName),
				)

			_, err = kr.KubernetesClient.RbacV1().Roles(*role.Namespace).
				Apply(ctx, role, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy role: %w", err)
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

			_, err = kr.KubernetesClient.RbacV1().RoleBindings(*roleBinding.Namespace).
				Apply(ctx, roleBinding, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy role binding: %w", err)
			}

			return nil
		},
	)
}

func (kr *kubeProxyReconciler) reconcileKubeProxyConfigMap(ctx context.Context, cluster *capiv1.Cluster) error {
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
				WithImage(fmt.Sprintf("k8s.gcr.io/kube-proxy:%s", hostedControlPlane.Spec.Version)).
				WithCommand("/usr/local/bin/kube-proxy").
				WithArgs(kr.buildArgs(kubeProxyConfigVolumeMount)...).
				WithEnv(corev1ac.EnvVar().
					WithName("NODE_NAME").
					WithValueFrom(corev1ac.EnvVarSource().
						WithFieldRef(corev1ac.ObjectFieldSelector().
							WithFieldPath("spec.nodeName"),
						),
					),
					corev1ac.EnvVar().WithName("NODE_IP").
						WithValueFrom(corev1ac.EnvVarSource().
							WithFieldRef(corev1ac.ObjectFieldSelector().
								WithFieldPath("status.hostIP"),
							),
						),
				).
				WithVolumeMounts(kubeProxyConfigVolumeMount, xtablesLockVolumeMount, modulesVolumeMount)

			_, ready, err := kr.ReconcileDaemonSet(
				ctx,
				konstants.KubeProxy,
				kr.kubeProxyNamespace,
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

func (kr *kubeProxyReconciler) buildArgs(kubeProxyConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration) []string {
	args := map[string]string{
		"config":             path.Join(*kubeProxyConfigVolumeMount.MountPath, kr.kubeProxyConfigMapKey),
		"hostname-override":  "$(NODE_NAME)",
		"nodeport-addresses": "primary",
		"bind-address":       "$(NODE_IP)",
	}
	return operatorutil.ArgsToSlice(args)
}
