package coredns

import (
	"context"
	"fmt"
	"path"

	"github.com/coredns/corefile-migration/migration/corefile"
	operatorutil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/reconcilers"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type CoreDNSReconciler interface {
	ReconcileCoreDNS(ctx context.Context, cluster *capiv1.Cluster) error
}

func NewCoreDNSReconciler(
	kubernetesClient kubernetes.Interface,
) CoreDNSReconciler {
	return &coreDNSReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			KubernetesClient: kubernetesClient,
			Tracer:           tracing.GetTracer("coredns"),
		},
		coreDNSLabels: map[string]string{
			"k8s-app":                       "kube-dns",
			"kubernetes.io/cluster-service": "true",
			"kubernetes.io/name":            "CoreDNS",
		},
		coreDNSServiceAccountName: "coredns",
		coreDNSDNSPortName:        "dns",
		coreDNSTCPDNSPortName:     "dns-tcp",
		coreDNSMetricsPortName:    "metrics",
		coreDNSConfigMapName:      "coredns",
		coreDNSCorefileFileName:   "Corefile",
	}
}

type coreDNSReconciler struct {
	reconcilers.WorkloadResourceReconciler
	coreDNSLabels             map[string]string
	coreDNSServiceAccountName string
	coreDNSDNSPortName        string
	coreDNSTCPDNSPortName     string
	coreDNSMetricsPortName    string
	coreDNSConfigMapName      string
	coreDNSCorefileFileName   string
}

var _ CoreDNSReconciler = &coreDNSReconciler{}

func (cr *coreDNSReconciler) ReconcileCoreDNS(ctx context.Context, cluster *capiv1.Cluster) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNS",
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

func (cr *coreDNSReconciler) reconcileCoreDNSRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNSRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(cr.coreDNSServiceAccountName, metav1.NamespaceSystem)

			_, err := cr.KubernetesClient.CoreV1().
				ServiceAccounts(*serviceAccount.Namespace).
				Apply(ctx, serviceAccount, operatorutil.ApplyOptions)
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

			_, err = cr.KubernetesClient.RbacV1().ClusterRoles().
				Apply(ctx, clusterRole, operatorutil.ApplyOptions)
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

			_, err = cr.KubernetesClient.RbacV1().ClusterRoleBindings().
				Apply(ctx, clusterRoleBinding, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS cluster role binding: %w", err)
			}

			return nil
		},
	)
}

func (cr *coreDNSReconciler) reconcileCoreDNSConfigMap(ctx context.Context, _ *capiv1.Cluster) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNSConfigMap",
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

			configMap := corev1ac.ConfigMap(cr.coreDNSConfigMapName, metav1.NamespaceSystem).
				WithData(map[string]string{
					cr.coreDNSCorefileFileName: defaultCorefile.ToString(),
				})

			_, err := cr.KubernetesClient.CoreV1().
				ConfigMaps(*configMap.Namespace).
				Apply(ctx, configMap, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS configmap: %w", err)
			}

			return nil
		},
	)
}

func (cr *coreDNSReconciler) reconcileCoreDNSDeployment(ctx context.Context) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNSDeployment",
		func(ctx context.Context, span trace.Span) error {
			coreDNSConfigVolume := corev1ac.Volume().
				WithName("config-volume").
				WithConfigMap(corev1ac.ConfigMapVolumeSource().
					WithName(cr.coreDNSConfigMapName).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(cr.coreDNSCorefileFileName).
							WithPath(cr.coreDNSCorefileFileName),
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
			container := corev1ac.Container().
				WithName("coredns").
				WithImage("registry.k8s.io/coredns/coredns:v1.12.0").
				WithImagePullPolicy(corev1.PullAlways).
				WithArgs("-conf", path.Join(*corednsConfigVolumeMount.MountPath, cr.coreDNSCorefileFileName)).
				WithPorts(
					corev1ac.ContainerPort().
						WithName(cr.coreDNSDNSPortName).
						WithContainerPort(53).
						WithProtocol(corev1.ProtocolUDP),
					corev1ac.ContainerPort().
						WithName(cr.coreDNSTCPDNSPortName).
						WithContainerPort(53).
						WithProtocol(corev1.ProtocolTCP),
					corev1ac.ContainerPort().
						WithName(cr.coreDNSMetricsPortName).
						WithContainerPort(9153).
						WithProtocol(corev1.ProtocolTCP),
					healthPort,
					readyPort,
				).
				WithVolumeMounts(corednsConfigVolumeMount).
				WithLivenessProbe(operatorutil.CreateLivenessProbe(healthPort, "/health", corev1.URISchemeHTTP)).
				WithReadinessProbe(operatorutil.CreateReadinessProbe(readyPort, "/ready", corev1.URISchemeHTTP)).
				WithStartupProbe(operatorutil.CreateStartupProbe(readyPort, "/ready", corev1.URISchemeHTTP)).
				WithSecurityContext(corev1ac.SecurityContext().
					WithAllowPrivilegeEscalation(false).
					WithCapabilities(corev1ac.Capabilities().
						WithDrop("ALL").
						WithAdd("NET_BIND_SERVICE"),
					).
					WithReadOnlyRootFilesystem(true).
					WithRunAsNonRoot(true).
					WithRunAsUser(1000),
				)

			_, err := cr.ReconcileDeployment(
				ctx,
				"coredns",
				metav1.NamespaceSystem,
				2,
				cr.coreDNSServiceAccountName,
				"system-cluster-critical",
				cr.coreDNSLabels,
				[]*corev1ac.TolerationApplyConfiguration{
					corev1ac.Toleration().
						WithOperator(corev1.TolerationOpExists),
				},
				[]*corev1ac.ContainerApplyConfiguration{container},
				[]*corev1ac.VolumeApplyConfiguration{coreDNSConfigVolume},
			)

			return errorsUtil.IfErrErrorf("failed to reconcile CoreDNS deployment: %w", err)
		},
	)
}

func (cr *coreDNSReconciler) reconcileCoreDNSService(ctx context.Context) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNSService",
		func(ctx context.Context, span trace.Span) error {
			service := corev1ac.Service("kube-dns", metav1.NamespaceSystem).
				WithLabels(cr.coreDNSLabels).
				WithSpec(corev1ac.ServiceSpec().
					WithSelector(cr.coreDNSLabels).
					WithClusterIP("10.96.0.10").
					WithPorts(
						corev1ac.ServicePort().
							WithName("dns").
							WithPort(53).
							WithProtocol(corev1.ProtocolUDP).
							WithTargetPort(intstr.FromString(cr.coreDNSDNSPortName)),
						corev1ac.ServicePort().
							WithName("dns-tcp").
							WithPort(53).
							WithProtocol(corev1.ProtocolTCP).
							WithTargetPort(intstr.FromString(cr.coreDNSTCPDNSPortName)),
						corev1ac.ServicePort().
							WithName("metrics").
							WithPort(9153).
							WithProtocol(corev1.ProtocolTCP).
							WithTargetPort(intstr.FromString(cr.coreDNSMetricsPortName)),
					).
					WithType(corev1.ServiceTypeClusterIP),
				)

			_, err := cr.KubernetesClient.CoreV1().
				Services(*service.Namespace).
				Apply(ctx, service, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to apply CoreDNS service: %w", err)
			}

			return nil
		},
	)
}
