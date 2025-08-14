package hostedcontrolplane

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	componentbaseconfigalpha1 "k8s.io/component-base/config/v1alpha1"
	kubeproxyv1alpha1 "k8s.io/kube-proxy/config/v1alpha1"
	"k8s.io/kubernetes/cmd/kubeadm/app/componentconfigs"
	"k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/addons/proxy"
	util2 "k8s.io/kubernetes/cmd/kubeadm/app/util"
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	kubeProxyReconcilerTracer = "KubeProxyReconciler"
)

var (
	kubeProxyLabels = map[string]string{
		"k8s-app":                       "kube-proxy",
		"kubernetes.io/cluster-service": "true",
	}
	kubeProxyConfigFileName  = "config.conf"
	kubeProxyConfigMountPath = "/var/lib/kube-proxy"
)

type KubeProxyReconciler struct {
	kubernetesClient kubernetes.Interface
}

func (kr *KubeProxyReconciler) ReconcileKubeProxy(
	ctx context.Context,
	cluster *capiv1.Cluster,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, kubeProxyReconcilerTracer, "ReconcileKubeProxy",
		func(ctx context.Context, span trace.Span) error {
			phases := []struct {
				Name      string
				Reconcile func(context.Context) error
			}{
				{
					Name: "ConfigMap",
					Reconcile: func(ctx context.Context) error {
						return kr.reconcileKubeProxyConfigMap(ctx, cluster)
					},
				},
				{
					Name:      "RBAC",
					Reconcile: kr.reconcileKubeProxyRBAC,
				},
				{
					Name: "DaemonSet",
					Reconcile: func(ctx context.Context) error {
						return kr.reconcileKubeProxyDaemonSet(ctx, hostedControlPlane, cluster)
					},
				},
			}

			for _, phase := range phases {
				if err := phase.Reconcile(ctx); err != nil {
					return fmt.Errorf("failed to reconcile kube-proxy phase %s: %w", phase.Name, err)
				}
			}

			return nil
		},
	)
}

func (kr *KubeProxyReconciler) reconcileKubeProxyRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, kubeProxyReconcilerTracer, "ReconcileKubeProxyRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(proxy.KubeProxyServiceAccountName, metav1.NamespaceSystem)

			_, err := kr.kubernetesClient.CoreV1().
				ServiceAccounts(metav1.NamespaceSystem).
				Apply(ctx, serviceAccount, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy service account: %w", err)
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding(constants.KubeProxyClusterRoleBindingName).
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind("ClusterRole").
						WithName(constants.KubeProxyClusterRoleName),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(*serviceAccount.Kind).
						WithName(*serviceAccount.Name).
						WithNamespace(*serviceAccount.Namespace),
				)

			_, err = kr.kubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy cluster role binding: %w", err)
			}

			role := rbacv1ac.Role(proxy.KubeProxyConfigMapRoleName, metav1.NamespaceSystem).
				WithRules(
					rbacv1ac.PolicyRule().
						WithVerbs("get").
						WithAPIGroups("").
						WithResources("configmaps").
						WithResourceNames(constants.KubeProxyConfigMap),
				)

			_, err = kr.kubernetesClient.RbacV1().Roles(metav1.NamespaceSystem).
				Apply(ctx, role, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy role: %w", err)
			}

			roleBinding := rbacv1ac.RoleBinding(proxy.KubeProxyConfigMapRoleName, metav1.NamespaceSystem).
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind(*role.Kind).
						WithName(*role.Name),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(rbacv1.GroupKind).
						WithName(constants.NodeBootstrapTokenAuthGroup),
				)

			_, err = kr.kubernetesClient.RbacV1().RoleBindings(metav1.NamespaceSystem).
				Apply(ctx, roleBinding, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy role binding: %w", err)
			}

			return nil
		},
	)
}

func (kr *KubeProxyReconciler) reconcileKubeProxyConfigMap(ctx context.Context, cluster *capiv1.Cluster) error {
	return tracing.WithSpan1(ctx, kubeProxyReconcilerTracer, "ReconcileKubeProxyConfigMap",
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

			zeroSDuration := metav1.Duration{
				Duration: 0 * time.Second,
			}
			kubeProxyConfig := kubeproxyv1alpha1.KubeProxyConfiguration{
				ClientConnection: componentbaseconfigalpha1.ClientConnectionConfiguration{
					AcceptContentTypes: "",
					Burst:              0,
					ContentType:        "",
					Kubeconfig:         path.Join(kubeProxyConfigMountPath, kubeconfigFileName),
					QPS:                0,
				},
				ConfigSyncPeriod: zeroSDuration,
				Conntrack: kubeproxyv1alpha1.KubeProxyConntrackConfiguration{
					MaxPerCore:            nil,
					Min:                   nil,
					TCPCloseWaitTimeout:   nil,
					TCPEstablishedTimeout: nil,
				},
				IPTables: kubeproxyv1alpha1.KubeProxyIPTablesConfiguration{
					MasqueradeAll: false,
					MasqueradeBit: nil,
					MinSyncPeriod: zeroSDuration,
					SyncPeriod:    zeroSDuration,
				},
				IPVS: kubeproxyv1alpha1.KubeProxyIPVSConfiguration{
					ExcludeCIDRs:  nil,
					Scheduler:     "",
					StrictARP:     false,
					MinSyncPeriod: zeroSDuration,
					SyncPeriod:    zeroSDuration,
					TCPFinTimeout: zeroSDuration,
					TCPTimeout:    zeroSDuration,
					UDPTimeout:    zeroSDuration,
				},
				BindAddress:                 "0.0.0.0",
				BindAddressHardFail:         false,
				ClusterCIDR:                 "192.168.0.0/16",
				DetectLocalMode:             "",
				EnableProfiling:             false,
				HealthzBindAddress:          "",
				HostnameOverride:            "",
				MetricsBindAddress:          "0.0.0.0:10249",
				Mode:                        "",
				OOMScoreAdj:                 nil,
				PortRange:                   "",
				ShowHiddenMetricsForVersion: "",
			}

			kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
			}

			kubeProxyConfigBytes, err := util2.MarshalToYamlForCodecs(
				&kubeProxyConfig,
				kubeproxyv1alpha1.SchemeGroupVersion,
				componentconfigs.Codecs,
			)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to marshal kube-proxy config: %w", err)
			}

			configMap := corev1ac.ConfigMap(constants.KubeProxyConfigMap, metav1.NamespaceSystem).
				WithData(map[string]string{
					kubeconfigFileName:              string(kubeconfigBytes),
					constants.KubeProxyConfigMapKey: string(kubeProxyConfigBytes),
				})

			_, err = kr.kubernetesClient.CoreV1().
				ConfigMaps(metav1.NamespaceSystem).
				Apply(ctx, configMap, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy config map: %w", err)
			}

			return nil
		},
	)
}

func (kr *KubeProxyReconciler) reconcileKubeProxyDaemonSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, kubeProxyReconcilerTracer, "ReconcileKubeProxyDaemonSet",
		func(ctx context.Context, span trace.Span) error {
			kubeProxyConfigVolume := corev1ac.Volume().
				WithName("kube-proxy-config").
				WithConfigMap(corev1ac.ConfigMapVolumeSource().
					WithName(constants.KubeProxyConfigMap),
				)
			xtablesLockVolume := corev1ac.Volume().
				WithName("xtables-lock").
				WithHostPath(
					corev1ac.HostPathVolumeSource().
						WithPath("/run/xtables.lock").
						WithType(corev1.HostPathFileOrCreate),
				)
			modulesVolume := corev1ac.Volume().
				WithName("lib-modules").
				WithHostPath(
					corev1ac.HostPathVolumeSource().
						WithPath("/lib/modules"),
				)

			kubeProxyConfigVolumeMount := corev1ac.VolumeMount().
				WithName(*kubeProxyConfigVolume.Name).
				WithMountPath(kubeProxyConfigMountPath)
			xtablesLockVolumeMount := corev1ac.VolumeMount().
				WithName(*xtablesLockVolume.Name).
				WithMountPath("/run/xtables.lock").
				WithReadOnly(false)
			modulesVolumeMount := corev1ac.VolumeMount().
				WithName(*modulesVolume.Name).
				WithMountPath("/lib/modules").
				WithReadOnly(true)

			template := corev1ac.PodTemplateSpec().
				WithLabels(kubeProxyLabels).
				WithSpec(
					corev1ac.PodSpec().
						WithServiceAccountName(proxy.KubeProxyServiceAccountName).
						WithContainers(
							corev1ac.Container().
								WithName("kube-proxy").
								WithImage(
									fmt.Sprintf("k8s.gcr.io/kube-proxy:%s", hostedControlPlane.Spec.Version),
								).
								WithCommand("/usr/local/bin/kube-proxy").
								WithArgs(kr.buildArgs(kubeProxyConfigVolumeMount)...).
								WithEnv(
									corev1ac.EnvVar().
										WithName("NODE_NAME").
										WithValueFrom(
											corev1ac.EnvVarSource().
												WithFieldRef(
													corev1ac.ObjectFieldSelector().
														WithFieldPath("spec.nodeName"),
												),
										),
								).
								WithVolumeMounts(kubeProxyConfigVolumeMount, xtablesLockVolumeMount, modulesVolumeMount).
								WithSecurityContext(corev1ac.SecurityContext().
									WithPrivileged(true),
								),
						).
						WithPriorityClassName("system-node-critical").
						WithHostNetwork(true).
						WithVolumes(kubeProxyConfigVolume, xtablesLockVolume, modulesVolume).
						WithTolerations(corev1ac.Toleration().
							WithOperator(corev1.TolerationOpExists),
						),
				)

			if err := operatorutil.ValidateMounts(template.Spec); err != nil {
				return fmt.Errorf("kubeproxy daemonset has mounts without corresponding volume: %w", err)
			}

			var err error
			template, err = util.SetChecksumAnnotations(ctx, kr.kubernetesClient, metav1.NamespaceSystem, template)
			if err != nil {
				return fmt.Errorf("failed to set checksum annotations for kube-proxy template: %w", err)
			}

			daemonSet := appsv1ac.DaemonSet("kube-proxy", metav1.NamespaceSystem).
				WithLabels(kubeProxyLabels).
				WithSpec(appsv1ac.DaemonSetSpec().
					WithSelector(metav1ac.LabelSelector().
						WithMatchLabels(kubeProxyLabels),
					).
					WithTemplate(template),
				)

			_, err = kr.kubernetesClient.AppsV1().DaemonSets(metav1.NamespaceSystem).
				Apply(ctx, daemonSet, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply kube-proxy daemon set: %w", err)
			}

			return nil
		},
	)
}

func (kr *KubeProxyReconciler) buildArgs(kubeProxyConfigVolumeMount *corev1ac.VolumeMountApplyConfiguration) []string {
	args := map[string]string{
		"config":            path.Join(*kubeProxyConfigVolumeMount.MountPath, kubeProxyConfigFileName),
		"hostname-override": "$(NODE_NAME)",
	}
	return operatorutil.ArgsToSlice(args)
}
