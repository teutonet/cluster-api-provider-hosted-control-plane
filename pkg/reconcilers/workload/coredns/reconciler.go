package coredns

import (
	"context"
	"fmt"
	"net"
	"path"

	"github.com/coredns/corefile-migration/migration/corefile"
	"github.com/go-logr/logr"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
)

type CoreDNSReconciler interface {
	ReconcileCoreDNS(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) (string, error)
}

func NewCoreDNSReconciler(
	kubernetesClient alias.WorkloadClusterClient,
	serviceDomain string,
	dnsIP net.IP,
) CoreDNSReconciler {
	return &coreDNSReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			KubernetesClient: kubernetesClient,
			Tracer:           tracing.GetTracer("coredns"),
		},
		serviceDomain:  serviceDomain,
		dnsIP:          dnsIP,
		coreDNSPort:    53,
		prometheusPort: 9153,
		coreDNSLabels: map[string]string{
			"k8s-app":                       "kube-dns",
			"kubernetes.io/cluster-service": "true",
			"kubernetes.io/name":            "CoreDNS",
		},
		coreDNSServiceAccountName: "coredns",
		coreDNSDNSPortName:        "dns",
		coreDNSTCPDNSPortName:     "dns-tcp",
		coreDNSMetricsPortName:    "metrics",
		coreDNSResourceName:       "coredns",
		coreDNSNamespace:          metav1.NamespaceSystem,
		coreDNSCorefileFileName:   "Corefile",
	}
}

type coreDNSReconciler struct {
	reconcilers.WorkloadResourceReconciler
	serviceDomain             string
	dnsIP                     net.IP
	coreDNSLabels             map[string]string
	coreDNSServiceAccountName string
	coreDNSPort               int32
	prometheusPort            int32
	coreDNSDNSPortName        string
	coreDNSTCPDNSPortName     string
	coreDNSMetricsPortName    string
	coreDNSResourceName       string
	coreDNSNamespace          string
	coreDNSCorefileFileName   string
}

var _ CoreDNSReconciler = &coreDNSReconciler{}

func (cr *coreDNSReconciler) ReconcileCoreDNS(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (string, error) {
	return tracing.WithSpan(ctx, cr.Tracer, "ReconcileCoreDNS",
		func(ctx context.Context, span trace.Span) (string, error) {
			phases := []struct {
				Name      string
				Reconcile func(context.Context) (string, error)
			}{
				{
					Name: "RBAC",
					Reconcile: func(ctx context.Context) (string, error) {
						return "", cr.reconcileCoreDNSRBAC(ctx)
					},
				},
				{
					Name: "ConfigMap",
					Reconcile: func(ctx context.Context) (string, error) {
						return "", cr.reconcileCoreDNSConfigMap(ctx)
					},
				},
				{
					Name: "Deployment",
					Reconcile: func(ctx context.Context) (string, error) {
						return cr.reconcileCoreDNSDeployment(ctx, hostedControlPlane)
					},
				},
				{
					Name:      "Service",
					Reconcile: cr.reconcileCoreDNSService,
				},
			}

			logger := logr.FromContextAsSlogLogger(ctx)
			for _, phase := range phases {
				notReadyReason, err := tracing.WithSpan(
					ctx,
					cr.Tracer,
					phase.Name,
					func(ctx context.Context, _ trace.Span) (string, error) {
						return phase.Reconcile(logr.NewContextWithSlogLogger(ctx, logger.With("phase", phase.Name)))
					},
				)
				if err != nil {
					return "", fmt.Errorf("failed to reconcile CoreDNS phase %s: %w", phase.Name, err)
				} else if notReadyReason != "" {
					return notReadyReason, nil
				}
			}

			return "", nil
		},
	)
}

func (cr *coreDNSReconciler) reconcileCoreDNSRBAC(ctx context.Context) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNSRBAC",
		func(ctx context.Context, span trace.Span) error {
			serviceAccount := corev1ac.ServiceAccount(cr.coreDNSServiceAccountName, cr.coreDNSNamespace)

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

func (cr *coreDNSReconciler) reconcileCoreDNSConfigMap(ctx context.Context) error {
	return tracing.WithSpan1(ctx, cr.Tracer, "ReconcileCoreDNSConfigMap",
		func(ctx context.Context, span trace.Span) error {
			defaultCorefile := &corefile.Corefile{
				Servers: []*corefile.Server{
					{
						DomPorts: []string{fmt.Sprintf(".:%d", cr.coreDNSPort)},
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
								Args: []string{cr.serviceDomain, "in-addr.arpa", "ip6.arpa"},
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
								Args: []string{fmt.Sprintf(":%d", cr.prometheusPort)},
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
										Args: []string{"success", cr.serviceDomain},
									},
									{
										Name: "disable",
										Args: []string{"denial", cr.serviceDomain},
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

			return cr.ReconcileConfigmap(
				ctx,
				cr.coreDNSNamespace,
				cr.coreDNSResourceName,
				false,
				cr.coreDNSLabels,
				map[string]string{
					cr.coreDNSCorefileFileName: defaultCorefile.ToString(),
				},
			)
		},
	)
}

func (cr *coreDNSReconciler) reconcileCoreDNSDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (string, error) {
	return tracing.WithSpan(ctx, cr.Tracer, "ReconcileCoreDNSDeployment",
		func(ctx context.Context, span trace.Span) (string, error) {
			coreDNSConfigVolume := corev1ac.Volume().
				WithName("config-volume").
				WithConfigMap(corev1ac.ConfigMapVolumeSource().
					WithName(cr.coreDNSResourceName).
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
				WithImage(operatorutil.ResolveCoreDNSImage(hostedControlPlane.Spec.CoreDNS.Image)).
				WithImagePullPolicy(corev1.PullAlways).
				WithArgs(operatorutil.ArgsToSliceWithObservability(
					ctx,
					hostedControlPlane.Spec.CoreDNS.Args,
					map[string]string{
						"-conf": path.Join(*corednsConfigVolumeMount.MountPath, cr.coreDNSCorefileFileName),
					},
				)...).
				WithResources(operatorutil.ResourceRequirementsToResourcesApplyConfiguration(
					hostedControlPlane.Spec.CoreDNS.Resources,
				)).
				WithPorts(
					corev1ac.ContainerPort().
						WithName(cr.coreDNSDNSPortName).
						WithContainerPort(cr.coreDNSPort).
						WithProtocol(corev1.ProtocolUDP),
					corev1ac.ContainerPort().
						WithName(cr.coreDNSTCPDNSPortName).
						WithContainerPort(cr.coreDNSPort).
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
				WithStartupProbe(operatorutil.CreateStartupProbe(readyPort, "/ready", corev1.URISchemeHTTP))

			nodes, err := cr.KubernetesClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to list nodes: %w", err)
			}

			_, ready, err := cr.ReconcileDeployment(
				ctx,
				cr.coreDNSResourceName,
				cr.coreDNSNamespace,
				false,
				slices.Ternary(len(nodes.Items) > 1, int32(2), int32(1)),
				reconcilers.PodOptions{
					ServiceAccountName: cr.coreDNSServiceAccountName,
					PriorityClassName:  "system-cluster-critical",
					DNSPolicy:          corev1.DNSDefault,
					Tolerations: []*corev1ac.TolerationApplyConfiguration{
						corev1ac.Toleration().
							WithOperator(corev1.TolerationOpExists),
					},
				},
				cr.coreDNSLabels,
				nil,
				nil,
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{
						Capabilities: []corev1.Capability{"NET_BIND_SERVICE"},
					}),
				},
				[]*corev1ac.VolumeApplyConfiguration{coreDNSConfigVolume},
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile CoreDNS deployment: %w", err)
			}
			if !ready {
				return "CoreDNS deployment not ready", nil
			}

			return "", nil
		},
	)
}

func (cr *coreDNSReconciler) reconcileCoreDNSService(ctx context.Context) (string, error) {
	return tracing.WithSpan(ctx, cr.Tracer, "ReconcileCoreDNSService",
		func(ctx context.Context, span trace.Span) (string, error) {
			_, ready, err := cr.ReconcileService(
				ctx,
				cr.coreDNSNamespace,
				"kube-dns",
				cr.coreDNSLabels,
				corev1.ServiceTypeClusterIP,
				cr.dnsIP,
				false,
				[]*corev1ac.ServicePortApplyConfiguration{
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
				},
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile CoreDNS service: %w", err)
			}
			if !ready {
				return "CoreDNS service not ready", nil
			}

			return "", nil
		},
	)
}
