package hostedcontrolplane

import (
	"context"
	"fmt"
	"path"

	"github.com/coredns/corefile-migration/migration/corefile"
	operatorutil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	corednsReconcilerTracer = "CoreDNSReconciler"
)

var (
	coreDNSLabels = map[string]string{
		"k8s-app":                       "kube-dns",
		"kubernetes.io/cluster-service": "true",
		"kubernetes.io/name":            "CoreDNS",
	}
	coreDNSServiceAccountName = "coredns"
	coreDNSDNSPortName        = "dns"
	coreDNSTCPDNSPortName     = "dns-tcp"
	coreDNSMetricsPortName    = "metrics"
	coreDNSConfigMapName      = "coredns"
	coreDNSCorefileFileName   = "Corefile"
)

type CoreDNSReconciler struct {
	kubernetesClient kubernetes.Interface
}

func (cr *CoreDNSReconciler) ReconcileCoreDNS(ctx context.Context, cluster *capiv1.Cluster) error {
	return tracing.WithSpan1(ctx, corednsReconcilerTracer, "ReconcileCoreDNS",
		func(ctx context.Context, span trace.Span) error {
			phases := []struct {
				Name      string
				Reconcile func(context.Context) error
			}{
				{
					Name:      "RBAC",
					Reconcile: cr.reconcileCoreDNSRBAC,
				},
				{
					Name: "ConfigMap",
					Reconcile: func(ctx context.Context) error {
						return cr.reconcileCoreDNSConfigMap(ctx, cluster)
					},
				},
				{
					Name:      "Deployment",
					Reconcile: cr.reconcileCoreDNSDeployment,
				},
				{
					Name:      "Service",
					Reconcile: cr.reconcileCoreDNSService,
				},
			}

			for _, phase := range phases {
				if err := phase.Reconcile(ctx); err != nil {
					return fmt.Errorf("failed to reconcile CoreDNS phase %s: %w", phase.Name, err)
				}
			}

			return nil
		},
	)
}

func (cr *CoreDNSReconciler) reconcileCoreDNSRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, corednsReconcilerTracer, "ReconcileCoreDNSRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(coreDNSServiceAccountName, metav1.NamespaceSystem)

			_, err := cr.kubernetesClient.CoreV1().
				ServiceAccounts(metav1.NamespaceSystem).
				Apply(ctx, serviceAccount, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS service account: %w", err)
			}

			clusterRole := rbacv1ac.ClusterRole("system:coredns").
				WithRules(
					rbacv1ac.PolicyRule().
						WithVerbs("list", "watch").
						WithAPIGroups("").
						WithResources("endpoints", "services", "pods", "namespaces"),
					rbacv1ac.PolicyRule().
						WithVerbs("get").
						WithAPIGroups("").
						WithResources("nodes"),
					rbacv1ac.PolicyRule().
						WithVerbs("list", "watch").
						WithAPIGroups("discovery.k8s.io").
						WithResources("endpointslices"),
				)

			_, err = cr.kubernetesClient.RbacV1().ClusterRoles().
				Apply(ctx, clusterRole, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS cluster role: %w", err)
			}

			clusterRoleBinding := rbacv1ac.ClusterRoleBinding("system:coredns").
				WithRoleRef(
					rbacv1ac.RoleRef().
						WithAPIGroup(rbacv1.GroupName).
						WithKind(*clusterRole.Kind).
						WithName(*clusterRole.Name),
				).
				WithSubjects(
					rbacv1ac.Subject().
						WithKind(*serviceAccount.Kind).
						WithName(*serviceAccount.Name).
						WithNamespace(*serviceAccount.Namespace),
				)

			_, err = cr.kubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS cluster role binding: %w", err)
			}

			return nil
		},
	)
}

func (cr *CoreDNSReconciler) reconcileCoreDNSConfigMap(ctx context.Context, _ *capiv1.Cluster) error {
	return tracing.WithSpan1(ctx, corednsReconcilerTracer, "ReconcileCoreDNSConfigMap",
		func(ctx context.Context, span trace.Span) error {
			defaultCorefile := &corefile.Corefile{
				Servers: []*corefile.Server{
					{
						DomPorts: []string{".:53"},
						Plugins: []*corefile.Plugin{
							{
								Name: "errors",
							},
							{
								Name: "health",
								Options: []*corefile.Option{
									{
										Name: "lameduck",
										Args: []string{"5s"},
									},
								},
							},
							{
								Name: "ready",
							},
							{
								Name: "kubernetes",
								Args: []string{"cluster.local", "in-addr.arpa", "ip6.arpa"},
								Options: []*corefile.Option{
									{
										Name: "fallthrough",
										Args: []string{"in-addr.arpa", "ip6.arpa"},
									},
									{
										Name: "ttl",
										Args: []string{"30"},
									},
								},
							},
							{
								Name: "prometheus",
								Args: []string{":9153"},
							},
							{
								Name: "forward",
								Args: []string{".", "/etc/resolv.conf"},
								Options: []*corefile.Option{
									{
										Name: "max_concurrent",
										Args: []string{"1000"},
									},
								},
							},
							{
								Name: "cache",
								Args: []string{"30"},
								Options: []*corefile.Option{
									{
										Name: "disable",
										Args: []string{"success", "cluster.local"},
									},
									{
										Name: "disable",
										Args: []string{"denial", "cluster.local"},
									},
								},
							},
							{
								Name: "loop",
							},
							{
								Name: "loadbalance",
							},
						},
					},
				},
			}

			configMap := corev1ac.ConfigMap(coreDNSConfigMapName, metav1.NamespaceSystem).
				WithData(map[string]string{
					coreDNSCorefileFileName: defaultCorefile.ToString(),
				})

			_, err := cr.kubernetesClient.CoreV1().
				ConfigMaps(metav1.NamespaceSystem).
				Apply(ctx, configMap, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS configmap: %w", err)
			}

			return nil
		},
	)
}

func (cr *CoreDNSReconciler) reconcileCoreDNSDeployment(ctx context.Context) error {
	return tracing.WithSpan1(ctx, corednsReconcilerTracer, "ReconcileCoreDNSDeployment",
		func(ctx context.Context, span trace.Span) error {
			labelSelector := metav1ac.LabelSelector().WithMatchLabels(coreDNSLabels)
			coreDNSConfigVolume := corev1ac.Volume().
				WithName("config-volume").
				WithConfigMap(corev1ac.ConfigMapVolumeSource().
					WithName(coreDNSConfigMapName).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(coreDNSCorefileFileName).
							WithPath(coreDNSCorefileFileName),
					),
				)
			corednsConfigVolumeMount := corev1ac.VolumeMount().
				WithName(*coreDNSConfigVolume.Name).
				WithMountPath("/etc/coredns").
				WithReadOnly(true)

			healthPort := corev1ac.ContainerPort().
				WithName("health").
				WithContainerPort(8080).
				WithProtocol(corev1.ProtocolTCP)
			readyPort := corev1ac.ContainerPort().
				WithName("ready").
				WithContainerPort(8181).
				WithProtocol(corev1.ProtocolTCP)
			template := corev1ac.PodTemplateSpec().
				WithLabels(coreDNSLabels).
				WithSpec(corev1ac.PodSpec().
					WithTopologySpreadConstraints(operatorutil.CreatePodTopologySpreadConstraints(labelSelector)).
					WithServiceAccountName(coreDNSServiceAccountName).
					WithPriorityClassName("system-cluster-critical").
					WithTolerations(corev1ac.Toleration().WithOperator(corev1.TolerationOpExists)).
					WithContainers(
						corev1ac.Container().
							WithName("coredns").
							WithImage("registry.k8s.io/coredns/coredns:v1.12.0").
							WithImagePullPolicy(corev1.PullAlways).
							WithArgs("-conf", path.Join(*corednsConfigVolumeMount.MountPath, coreDNSCorefileFileName)).
							WithPorts(
								corev1ac.ContainerPort().
									WithName(coreDNSDNSPortName).
									WithContainerPort(53).
									WithProtocol(corev1.ProtocolUDP),
								corev1ac.ContainerPort().
									WithName(coreDNSTCPDNSPortName).
									WithContainerPort(53).
									WithProtocol(corev1.ProtocolTCP),
								corev1ac.ContainerPort().
									WithName(coreDNSMetricsPortName).
									WithContainerPort(9153).
									WithProtocol(corev1.ProtocolTCP),
								healthPort,
								readyPort,
							).
							WithVolumeMounts(corednsConfigVolumeMount).
							WithLivenessProbe(corev1ac.Probe().
								WithHTTPGet(corev1ac.HTTPGetAction().
									WithPath("/health").
									WithPort(intstr.FromString(*healthPort.Name)),
								).
								WithInitialDelaySeconds(60).
								WithTimeoutSeconds(5).
								WithSuccessThreshold(1).
								WithFailureThreshold(5),
							).
							WithReadinessProbe(corev1ac.Probe().
								WithHTTPGet(corev1ac.HTTPGetAction().
									WithPath("/ready").
									WithPort(intstr.FromString(*readyPort.Name)),
								).
								WithPeriodSeconds(2)).
							WithStartupProbe(corev1ac.Probe().
								WithHTTPGet(corev1ac.HTTPGetAction().
									WithPath("/ready").
									WithPort(intstr.FromString(*readyPort.Name)),
								).
								WithPeriodSeconds(2).
								WithFailureThreshold(10),
							).
							WithSecurityContext(corev1ac.SecurityContext().
								WithAllowPrivilegeEscalation(false).
								WithCapabilities(corev1ac.Capabilities().
									WithDrop("ALL").
									WithAdd("NET_BIND_SERVICE"),
								).
								WithReadOnlyRootFilesystem(true).
								WithRunAsNonRoot(true).
								WithRunAsUser(1000),
							),
					).
					WithEnableServiceLinks(false).
					WithVolumes(coreDNSConfigVolume).
					WithDNSPolicy(corev1.DNSDefault),
				)

			template, err := util.SetChecksumAnnotations(ctx, cr.kubernetesClient, metav1.NamespaceSystem, template)
			if err != nil {
				return fmt.Errorf("failed to set checksum annotations: %w", err)
			}

			deployment := appsv1ac.Deployment("coredns", metav1.NamespaceSystem).
				WithLabels(coreDNSLabels).
				WithSpec(appsv1ac.DeploymentSpec().
					WithReplicas(2).
					WithSelector(labelSelector).
					WithTemplate(template),
				)

			_, err = cr.kubernetesClient.AppsV1().Deployments(metav1.NamespaceSystem).
				Apply(ctx, deployment, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS deployment: %w", err)
			}

			return nil
		},
	)
}

func (cr *CoreDNSReconciler) reconcileCoreDNSService(ctx context.Context) error {
	return tracing.WithSpan1(ctx, corednsReconcilerTracer, "ReconcileCoreDNSService",
		func(ctx context.Context, span trace.Span) error {
			service := corev1ac.Service("kube-dns", metav1.NamespaceSystem).
				WithLabels(coreDNSLabels).
				WithSpec(corev1ac.ServiceSpec().
					WithSelector(coreDNSLabels).
					WithClusterIP("10.96.0.10").
					WithPorts(
						corev1ac.ServicePort().
							WithName("dns").
							WithPort(53).
							WithProtocol(corev1.ProtocolUDP).
							WithTargetPort(intstr.FromString(coreDNSDNSPortName)),
						corev1ac.ServicePort().
							WithName("dns-tcp").
							WithPort(53).
							WithProtocol(corev1.ProtocolTCP).
							WithTargetPort(intstr.FromString(coreDNSTCPDNSPortName)),
						corev1ac.ServicePort().
							WithName("metrics").
							WithPort(9153).
							WithProtocol(corev1.ProtocolTCP).
							WithTargetPort(intstr.FromString(coreDNSMetricsPortName)),
					).
					WithType(corev1.ServiceTypeClusterIP),
				)

			_, err := cr.kubernetesClient.CoreV1().
				Services(metav1.NamespaceSystem).
				Apply(ctx, service, workloadApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS service: %w", err)
			}

			return nil
		},
	)
}
