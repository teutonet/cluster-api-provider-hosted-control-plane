package monitoring

import (
	"context"
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	monitoringv1ac "github.com/prometheus-operator/prometheus-operator/pkg/client/applyconfiguration/monitoring/v1"
	monitoringv1alpha1ac "github.com/prometheus-operator/prometheus-operator/pkg/client/applyconfiguration/monitoring/v1alpha1"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	// ClusterNameLabel and ClusterNamespaceLabel are put on all scraped metrics, so they can be told apart from the
	// metrics of the management cluster (and of other workload clusters).
	ClusterNameLabel      = "capi_cluster_name"
	ClusterNamespaceLabel = "capi_cluster_namespace"

	// The workload cluster components are scraped through the pod proxy of the apiserver.
	podProxyMetricsPathTemplate = "/api/v1/namespaces/$1/pods/$2:%d/proxy/metrics"

	// The konnectivity server serves its metrics on the health port, which is named like this.
	konnectivityServerHealthPortName = "k-health"
	coreDNSPodSelector               = "k8s-app=kube-dns"

	// APIServerPortName is the name of the (https) container port of the kube-apiserver.
	APIServerPortName = "api"

	// apiServerMetricsAllowlist keeps only what we alert on, the apiserver exposes ~12k series per pod.
	// The source labels are __name__, resource, subresource and code, joined by ";", so the series of the families
	// without these labels end in ";;;".
	//   - the egress dialer, which dials the konnectivity server, so it shows "No agent available" and dial timeouts
	//   - the inflight requests
	//   - the requests that go through konnectivity (exec, logs, attach, port-forward), and all server errors.
	apiServerMetricsAllowlist = "(" +
		"apiserver_egress_dialer_dial_failure_count" +
		"|apiserver_egress_dialer_dial_duration_seconds_(bucket|count|sum)" +
		"|apiserver_egress_dialer_dial_start_total" +
		"|apiserver_current_inflight_requests" +
		");.*" +
		"|apiserver_request_total;pods;(exec|log|attach|portforward);.*" +
		"|apiserver_request_total;[^;]*;[^;]*;5.."

	// apiServerMetricsHealthCheckDrop drops the requests to the health check endpoints, which are non resource
	// requests with the path as subresource. They answer 500 on every rollout (shutdown delay) and are only noise.
	// The source labels are __name__ and subresource, joined by ";".
	apiServerMetricsHealthCheckDrop = "apiserver_request_total;/(readyz|livez|healthz)(/.*)?"
)

type MonitoringReconciler interface {
	// ReconcileMonitoring creates the monitoring resources if the target is enabled, otherwise it removes them.
	ReconcileMonitoring(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
		target Target,
	) error
}

// NewMonitoringReconciler creates a MonitoringReconciler. The monitoringClient is nil if the Prometheus Operator CRDs
// are not installed, in which case there is nothing to reconcile.
func NewMonitoringReconciler(
	monitoringClient monitoringclient.Interface,
	apiServerComponentLabel string,
	apiServerServicePort int32,
	konnectivityAgentComponentLabel string,
) MonitoringReconciler {
	return &monitoringReconciler{
		monitoringClient:                monitoringClient,
		apiServerComponentLabel:         apiServerComponentLabel,
		apiServerServicePort:            apiServerServicePort,
		konnectivityAgentComponentLabel: konnectivityAgentComponentLabel,
		tracer:                          tracing.GetTracer("monitoring"),
	}
}

type monitoringReconciler struct {
	monitoringClient                monitoringclient.Interface
	apiServerComponentLabel         string
	apiServerServicePort            int32
	konnectivityAgentComponentLabel string
	tracer                          string
}

var _ MonitoringReconciler = &monitoringReconciler{}

//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=prometheuses,verbs=list
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors;scrapeconfigs,verbs=create;patch;delete

func (mr *monitoringReconciler) ReconcileMonitoring(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	target Target,
) error {
	return tracing.WithSpan1(ctx, mr.tracer, "ReconcileMonitoring",
		func(ctx context.Context, span trace.Span) error {
			if mr.monitoringClient == nil {
				return nil
			}
			if !target.Enabled {
				return mr.deleteMonitors(ctx, hostedControlPlane.Namespace, cluster)
			}

			if err := mr.reconcileKonnectivityServerPodMonitor(ctx, hostedControlPlane, cluster, target); err != nil {
				return fmt.Errorf("failed to reconcile konnectivity server pod monitor: %w", err)
			}
			if err := mr.reconcileAPIServerPodMonitor(ctx, hostedControlPlane, cluster, target); err != nil {
				return fmt.Errorf("failed to reconcile apiserver pod monitor: %w", err)
			}
			if err := mr.reconcileScrapeConfig(
				ctx, hostedControlPlane, cluster, target,
				names.GetKonnectivityAgentScrapeConfigName(cluster),
				"konnectivity-agent",
				labels.SelectorFromSet(names.GetControlPlaneLabels(cluster, mr.konnectivityAgentComponentLabel)).String(),
				KonnectivityAgentMetricsPort,
			); err != nil {
				return fmt.Errorf("failed to reconcile konnectivity agent scrape config: %w", err)
			}
			if err := mr.reconcileScrapeConfig(
				ctx, hostedControlPlane, cluster, target,
				names.GetCoreDNSScrapeConfigName(cluster),
				"coredns",
				coreDNSPodSelector,
				CoreDNSMetricsPort,
			); err != nil {
				return fmt.Errorf("failed to reconcile coredns scrape config: %w", err)
			}
			return nil
		},
	)
}

func (mr *monitoringReconciler) deleteMonitors(ctx context.Context, namespace string, cluster *capiv2.Cluster) error {
	podMonitors := mr.monitoringClient.MonitoringV1().PodMonitors(namespace)
	for _, name := range []string{
		names.GetKonnectivityServerPodMonitorName(cluster),
		names.GetAPIServerPodMonitorName(cluster),
	} {
		if err := podMonitors.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod monitor %s: %w", name, err)
		}
	}
	scrapeConfigs := mr.monitoringClient.MonitoringV1alpha1().ScrapeConfigs(namespace)
	for _, name := range []string{
		names.GetKonnectivityAgentScrapeConfigName(cluster),
		names.GetCoreDNSScrapeConfigName(cluster),
	} {
		if err := scrapeConfigs.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete scrape config %s: %w", name, err)
		}
	}
	return nil
}

// clusterRelabelConfigs marks the metrics as coming from the workload cluster.
func clusterRelabelConfigs(cluster *capiv2.Cluster, job string) []*monitoringv1ac.RelabelConfigApplyConfiguration {
	return []*monitoringv1ac.RelabelConfigApplyConfiguration{
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithTargetLabel(ClusterNameLabel).
			WithReplacement(cluster.Name),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithTargetLabel(ClusterNamespaceLabel).
			WithReplacement(cluster.Namespace),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithTargetLabel("job").
			WithReplacement(job),
	}
}

func (mr *monitoringReconciler) reconcileKonnectivityServerPodMonitor(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	target Target,
) error {
	podMonitor := monitoringv1ac.
		PodMonitor(names.GetKonnectivityServerPodMonitorName(cluster), hostedControlPlane.Namespace).
		WithLabels(slices.Assign(target.MonitorLabels, names.GetControlPlaneLabels(cluster, ""))).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(monitoringv1ac.PodMonitorSpec().
			WithSelector(names.GetControlPlaneSelector(cluster, mr.apiServerComponentLabel)).
			WithPodMetricsEndpoints(monitoringv1ac.PodMetricsEndpoint().
				WithPort(konnectivityServerHealthPortName).
				WithPath("/metrics").
				WithScheme(monitoringv1.SchemeHTTP).
				WithRelabelConfigs(clusterRelabelConfigs(cluster, "konnectivity-server")...),
			),
		)
	_, err := mr.monitoringClient.MonitoringV1().PodMonitors(hostedControlPlane.Namespace).
		Apply(ctx, podMonitor, operatorutil.ApplyOptions)
	return err //nolint:wrapcheck // wrapped by the caller
}

// metricsClientTLSConfig authenticates as the metrics client (user control-plane-metrics) and trusts the cluster CA.
// serverName is only needed if the server is not addressed by one of the names in its certificate (e.g. a pod IP).
func metricsClientTLSConfig(
	cluster *capiv2.Cluster,
	serverName string,
) *monitoringv1ac.SafeTLSConfigApplyConfiguration {
	tlsConfig := monitoringv1ac.SafeTLSConfig().
		WithCA(monitoringv1ac.SecretOrConfigMap().WithSecret(corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: names.GetCABundleSecretName(cluster)},
			Key:                  konstants.CACertName,
		})).
		WithCert(monitoringv1ac.SecretOrConfigMap().WithSecret(corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: names.GetMetricsClientCertificateSecretName(cluster),
			},
			Key: corev1.TLSCertKey,
		})).
		WithKeySecret(corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: names.GetMetricsClientCertificateSecretName(cluster),
			},
			Key: corev1.TLSPrivateKeyKey,
		})
	if serverName != "" {
		tlsConfig = tlsConfig.WithServerName(serverName)
	}
	return tlsConfig
}

// reconcileAPIServerPodMonitor scrapes the (authenticated) metrics of the hosted kube-apiserver pods directly.
// The metrics client needs to be allowed to get /metrics in the workload cluster, see the workload RBAC.
func (mr *monitoringReconciler) reconcileAPIServerPodMonitor(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	target Target,
) error {
	podMonitor := monitoringv1ac.
		PodMonitor(names.GetAPIServerPodMonitorName(cluster), hostedControlPlane.Namespace).
		WithLabels(slices.Assign(target.MonitorLabels, names.GetControlPlaneLabels(cluster, ""))).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(monitoringv1ac.PodMonitorSpec().
			WithSelector(names.GetControlPlaneSelector(cluster, mr.apiServerComponentLabel)).
			WithPodMetricsEndpoints(monitoringv1ac.PodMetricsEndpoint().
				WithPort(APIServerPortName).
				WithPath("/metrics").
				WithScheme(monitoringv1.SchemeHTTPS).
				// the pods are addressed by IP, the certificate contains the internal Service host
				WithTLSConfig(metricsClientTLSConfig(cluster, names.GetInternalServiceHost(cluster))).
				WithRelabelConfigs(clusterRelabelConfigs(cluster, "kube-apiserver")...).
				WithMetricRelabelConfigs(
					monitoringv1ac.RelabelConfig().
						WithAction("drop").
						WithSourceLabels("__name__", "subresource").
						WithSeparator(";").
						WithRegex(apiServerMetricsHealthCheckDrop),
					monitoringv1ac.RelabelConfig().
						WithAction("keep").
						WithSourceLabels("__name__", "resource", "subresource", "code").
						WithSeparator(";").
						WithRegex(apiServerMetricsAllowlist),
				),
			),
		)
	_, err := mr.monitoringClient.MonitoringV1().PodMonitors(hostedControlPlane.Namespace).
		Apply(ctx, podMonitor, operatorutil.ApplyOptions)
	return err //nolint:wrapcheck // wrapped by the caller
}

// reconcileScrapeConfig scrapes the pods of the workload cluster that match the selector. Prometheus discovers them
// through the (internal) apiserver Service, with the metrics client certificate, and scrapes them through the pod
// proxy of the apiserver, as the pod network of the workload cluster is not reachable from the management cluster.
func (mr *monitoringReconciler) reconcileScrapeConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	target Target,
	name string,
	job string,
	podSelector string,
	port int32,
) error {
	apiServerAddress := fmt.Sprintf("%s:%d", names.GetInternalServiceHost(cluster), mr.apiServerServicePort)

	relabelConfigs := []*monitoringv1ac.RelabelConfigApplyConfiguration{
		monitoringv1ac.RelabelConfig().
			WithAction("keep").
			WithSourceLabels("__meta_kubernetes_pod_phase").
			WithRegex("Running"),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithSourceLabels("__meta_kubernetes_namespace", "__meta_kubernetes_pod_name").
			WithSeparator(";").
			WithRegex("(.+);(.+)").
			WithTargetLabel("__metrics_path__").
			WithReplacement(fmt.Sprintf(podProxyMetricsPathTemplate, port)),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithTargetLabel("__address__").
			WithReplacement(apiServerAddress),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithSourceLabels("__meta_kubernetes_namespace").
			WithTargetLabel("namespace"),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithSourceLabels("__meta_kubernetes_pod_name").
			WithTargetLabel("pod"),
		monitoringv1ac.RelabelConfig().
			WithAction("replace").
			WithSourceLabels("__meta_kubernetes_pod_name").
			WithTargetLabel("instance"),
	}
	relabelConfigs = append(relabelConfigs, clusterRelabelConfigs(cluster, job)...)

	scrapeConfig := monitoringv1alpha1ac.ScrapeConfig(name, hostedControlPlane.Namespace).
		WithLabels(slices.Assign(target.MonitorLabels, names.GetControlPlaneLabels(cluster, ""))).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(monitoringv1alpha1ac.ScrapeConfigSpec().
			WithJobName(job).
			WithScheme(monitoringv1.SchemeHTTPS).
			WithTLSConfig(metricsClientTLSConfig(cluster, "")).
			WithKubernetesSDConfigs(monitoringv1alpha1ac.KubernetesSDConfig().
				WithAPIServer("https://" + apiServerAddress).
				WithRole(monitoringv1alpha1.KubernetesRolePod).
				WithNamespaces(monitoringv1alpha1ac.NamespaceDiscovery().WithNames(metav1.NamespaceSystem)).
				WithSelectors(monitoringv1alpha1ac.K8SSelectorConfig().
					WithRole(monitoringv1alpha1.KubernetesRolePod).
					WithLabel(podSelector),
				).
				WithTLSConfig(metricsClientTLSConfig(cluster, "")),
			).
			WithRelabelConfigs(relabelConfigs...),
		)
	_, err := mr.monitoringClient.MonitoringV1alpha1().ScrapeConfigs(hostedControlPlane.Namespace).
		Apply(ctx, scrapeConfig, operatorutil.ApplyOptions)
	return err //nolint:wrapcheck // wrapped by the caller
}
