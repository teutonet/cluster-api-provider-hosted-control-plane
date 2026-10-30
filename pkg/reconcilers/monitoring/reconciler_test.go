package monitoring

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	monitoringfake "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/fake"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	testCluster = &capiv2.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-ns"}}
	testHCP     = &v1alpha1.HostedControlPlane{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "HostedControlPlane"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-ns", UID: types.UID("test-uid")},
	}
	testTarget = Target{Enabled: true, MonitorLabels: map[string]string{"release": "stack"}}
)

func newTestReconciler(client *monitoringfake.Clientset) MonitoringReconciler {
	return NewMonitoringReconciler(client, "api-server", 443, KonnectivityAgentComponent)
}

// newTestClient is a fake client that stores applied (server side apply) objects, the field managed tracker of the
// fake does not know the schemas of the Prometheus Operator types.
func newTestClient(t *testing.T) *monitoringfake.Clientset {
	t.Helper()
	client := monitoringfake.NewSimpleClientset()
	client.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction, ok := action.(k8stesting.PatchAction)
		if !ok || patchAction.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		var object runtime.Object
		switch patchAction.GetResource().Resource {
		case "podmonitors":
			object = &monitoringv1.PodMonitor{}
		case "scrapeconfigs":
			object = &monitoringv1alpha1.ScrapeConfig{}
		default:
			return false, nil, nil
		}
		if err := json.Unmarshal(patchAction.GetPatch(), object); err != nil {
			return true, nil, err
		}
		accessor, err := meta.Accessor(object)
		if err != nil {
			return true, nil, err
		}
		accessor.SetNamespace(patchAction.GetNamespace())
		if _, err := client.Tracker().Get(
			patchAction.GetResource(), patchAction.GetNamespace(), accessor.GetName(),
		); err != nil {
			err = client.Tracker().Create(patchAction.GetResource(), object, patchAction.GetNamespace())
			return true, object, err
		}
		err = client.Tracker().Update(patchAction.GetResource(), object, patchAction.GetNamespace())
		return true, object, err
	})
	return client
}

func relabelReplacements(configs []monitoringv1.RelabelConfig) map[string]string {
	replacements := map[string]string{}
	for _, config := range configs {
		if config.Action == "replace" && config.TargetLabel != "" && config.Replacement != nil {
			replacements[config.TargetLabel] = *config.Replacement
		}
	}
	return replacements
}

func TestReconcileMonitoring_CreatesMonitors(t *testing.T) {
	g, ctx, _ := G(t)
	client := newTestClient(t)

	g.Expect(newTestReconciler(client).ReconcileMonitoring(ctx, testHCP, testCluster, testTarget)).To(Succeed())

	podMonitor, err := client.MonitoringV1().PodMonitors("test-ns").
		Get(ctx, names.GetKonnectivityServerPodMonitorName(testCluster), metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(podMonitor.Labels).To(HaveKeyWithValue("release", "stack"))
	g.Expect(podMonitor.OwnerReferences).To(HaveLen(1))
	g.Expect(podMonitor.Spec.Selector.MatchLabels).To(Equal(names.GetControlPlaneLabels(testCluster, "api-server")))
	g.Expect(podMonitor.Spec.PodMetricsEndpoints).To(HaveLen(1))
	endpoint := podMonitor.Spec.PodMetricsEndpoints[0]
	g.Expect(*endpoint.Port).To(Equal("k-health"))
	g.Expect(endpoint.Path).To(Equal("/metrics"))
	g.Expect(relabelReplacements(endpoint.RelabelConfigs)).To(And(
		HaveKeyWithValue(ClusterNameLabel, "test-cluster"),
		HaveKeyWithValue(ClusterNamespaceLabel, "test-ns"),
		HaveKeyWithValue("job", "konnectivity-server"),
	))

	for name, expected := range map[string]struct {
		job      string
		selector string
		port     string
	}{
		names.GetKonnectivityAgentScrapeConfigName(testCluster): {
			job:      "konnectivity-agent",
			selector: "app.kubernetes.io/component=konnectivity,cluster.x-k8s.io/cluster-name=test-cluster",
			port:     "8134",
		},
		names.GetCoreDNSScrapeConfigName(testCluster): {
			job:      "coredns",
			selector: "k8s-app=kube-dns",
			port:     "9153",
		},
	} {
		scrapeConfig, err := client.MonitoringV1alpha1().ScrapeConfigs("test-ns").Get(ctx, name, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), name)
		g.Expect(scrapeConfig.Labels).To(HaveKeyWithValue("release", "stack"), name)
		g.Expect(scrapeConfig.OwnerReferences).To(HaveLen(1), name)
		g.Expect(*scrapeConfig.Spec.JobName).To(Equal(expected.job), name)
		g.Expect(*scrapeConfig.Spec.Scheme).To(Equal(monitoringv1.SchemeHTTPS), name)

		// internal Service of the apiserver, not the gateway
		g.Expect(scrapeConfig.Spec.KubernetesSDConfigs).To(HaveLen(1), name)
		sd := scrapeConfig.Spec.KubernetesSDConfigs[0]
		g.Expect(*sd.APIServer).To(Equal("https://" + names.GetInternalServiceHost(testCluster) + ":443"))
		g.Expect(sd.Role).To(Equal(monitoringv1alpha1.KubernetesRolePod))
		g.Expect(sd.Namespaces.Names).To(ConsistOf("kube-system"))
		g.Expect(sd.Selectors).To(HaveLen(1))
		g.Expect(*sd.Selectors[0].Label).To(Equal(expected.selector), name)
		for _, tlsConfig := range []*monitoringv1.SafeTLSConfig{sd.TLSConfig, scrapeConfig.Spec.TLSConfig} {
			g.Expect(tlsConfig.CA.Secret.Name).To(Equal(names.GetCABundleSecretName(testCluster)), name)
			g.Expect(tlsConfig.Cert.Secret.Name).
				To(Equal(names.GetMetricsClientCertificateSecretName(testCluster)), name)
			g.Expect(tlsConfig.KeySecret.Name).To(Equal(names.GetMetricsClientCertificateSecretName(testCluster)), name)
			g.Expect(tlsConfig.ServerName).To(BeNil(), name)
		}

		// scraped through the pod proxy of the apiserver, labelled as workload cluster metrics
		var metricsPath, address string
		for _, relabelConfig := range scrapeConfig.Spec.RelabelConfigs {
			switch relabelConfig.TargetLabel {
			case "__metrics_path__":
				metricsPath = *relabelConfig.Replacement
			case "__address__":
				address = *relabelConfig.Replacement
			}
		}
		g.Expect(metricsPath).To(Equal("/api/v1/namespaces/$1/pods/$2:"+expected.port+"/proxy/metrics"), name)
		g.Expect(address).To(Equal(names.GetInternalServiceHost(testCluster)+":443"), name)
		g.Expect(relabelReplacements(scrapeConfig.Spec.RelabelConfigs)).To(And(
			HaveKeyWithValue(ClusterNameLabel, "test-cluster"),
			HaveKeyWithValue(ClusterNamespaceLabel, "test-ns"),
			HaveKeyWithValue("job", expected.job),
		), name)
	}
}

func TestReconcileMonitoring_DeletesMonitorsWhenDisabled(t *testing.T) {
	g, ctx, _ := G(t)
	client := newTestClient(t)
	reconciler := newTestReconciler(client)
	g.Expect(reconciler.ReconcileMonitoring(ctx, testHCP, testCluster, testTarget)).To(Succeed())

	g.Expect(reconciler.ReconcileMonitoring(ctx, testHCP, testCluster, Target{})).To(Succeed())

	podMonitors, err := client.MonitoringV1().PodMonitors("test-ns").List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(podMonitors.Items).To(BeEmpty())
	scrapeConfigs, err := client.MonitoringV1alpha1().ScrapeConfigs("test-ns").List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(scrapeConfigs.Items).To(BeEmpty())
}

func TestReconcileMonitoring_NothingToDoWithoutCRDs(t *testing.T) {
	g, ctx, _ := G(t)
	reconciler := NewMonitoringReconciler(nil, "api-server", 443, KonnectivityAgentComponent)

	g.Expect(reconciler.ReconcileMonitoring(ctx, testHCP, testCluster, testTarget)).To(Succeed())
	g.Expect(reconciler.ReconcileMonitoring(ctx, testHCP, testCluster, Target{})).To(Succeed())
}

func TestReconcileMonitoring_APIServerPodMonitor(t *testing.T) {
	g, ctx, _ := G(t)
	client := newTestClient(t)

	g.Expect(newTestReconciler(client).ReconcileMonitoring(ctx, testHCP, testCluster, testTarget)).To(Succeed())

	podMonitor, err := client.MonitoringV1().PodMonitors("test-ns").
		Get(ctx, names.GetAPIServerPodMonitorName(testCluster), metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(podMonitor.Labels).To(HaveKeyWithValue("release", "stack"))
	g.Expect(podMonitor.OwnerReferences).To(HaveLen(1))
	g.Expect(podMonitor.Spec.Selector.MatchLabels).To(Equal(names.GetControlPlaneLabels(testCluster, "api-server")))
	g.Expect(podMonitor.Spec.PodMetricsEndpoints).To(HaveLen(1))

	endpoint := podMonitor.Spec.PodMetricsEndpoints[0]
	g.Expect(*endpoint.Port).To(Equal(APIServerPortName))
	g.Expect(endpoint.Path).To(Equal("/metrics"))
	g.Expect(*endpoint.Scheme).To(Equal(monitoringv1.SchemeHTTPS))

	// authenticated as the metrics client, the pods are addressed by IP, so the name of the certificate is needed
	g.Expect(endpoint.TLSConfig).NotTo(BeNil())
	g.Expect(endpoint.TLSConfig.CA.Secret.Name).To(Equal(names.GetCABundleSecretName(testCluster)))
	g.Expect(endpoint.TLSConfig.Cert.Secret.Name).To(Equal(names.GetMetricsClientCertificateSecretName(testCluster)))
	g.Expect(endpoint.TLSConfig.KeySecret.Name).To(Equal(names.GetMetricsClientCertificateSecretName(testCluster)))
	g.Expect(*endpoint.TLSConfig.ServerName).To(Equal(names.GetInternalServiceHost(testCluster)))

	g.Expect(relabelReplacements(endpoint.RelabelConfigs)).To(And(
		HaveKeyWithValue(ClusterNameLabel, "test-cluster"),
		HaveKeyWithValue(ClusterNamespaceLabel, "test-ns"),
		HaveKeyWithValue("job", "kube-apiserver"),
	))

	// the health checks are dropped first, then only the allowlist is kept
	g.Expect(endpoint.MetricRelabelConfigs).To(HaveLen(2))
	drop, keep := endpoint.MetricRelabelConfigs[0], endpoint.MetricRelabelConfigs[1]
	g.Expect(drop.Action).To(Equal("drop"))
	g.Expect(drop.SourceLabels).To(Equal([]monitoringv1.LabelName{"__name__", "subresource"}))
	g.Expect(*drop.Separator).To(Equal(";"))
	g.Expect(drop.Regex).To(Equal(apiServerMetricsHealthCheckDrop))
	g.Expect(keep.Action).To(Equal("keep"))
	g.Expect(keep.SourceLabels).To(Equal([]monitoringv1.LabelName{"__name__", "resource", "subresource", "code"}))
	g.Expect(*keep.Separator).To(Equal(";"))
	g.Expect(keep.Regex).To(Equal(apiServerMetricsAllowlist))
}

func TestReconcileMonitoring_DeletesAPIServerPodMonitorWhenDisabled(t *testing.T) {
	g, ctx, _ := G(t)
	client := newTestClient(t)
	reconciler := newTestReconciler(client)
	g.Expect(reconciler.ReconcileMonitoring(ctx, testHCP, testCluster, testTarget)).To(Succeed())

	g.Expect(reconciler.ReconcileMonitoring(ctx, testHCP, testCluster, Target{})).To(Succeed())

	_, err := client.MonitoringV1().PodMonitors("test-ns").
		Get(ctx, names.GetAPIServerPodMonitorName(testCluster), metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

// The series are matched like Prometheus does it: the source labels of each rule are joined by ";", the regex is
// anchored, and the drop rule runs before the keep rule.
func TestAPIServerMetricsRelabeling(t *testing.T) {
	drop := regexp.MustCompile("^(?:" + apiServerMetricsHealthCheckDrop + ")$")
	keep := regexp.MustCompile("^(?:" + apiServerMetricsAllowlist + ")$")
	kept := func(name, resource, subresource, code string) bool {
		if drop.MatchString(name + ";" + subresource) {
			return false
		}
		return keep.MatchString(strings.Join([]string{name, resource, subresource, code}, ";"))
	}

	tests := []struct {
		name                        string
		metric                      string
		resource, subresource, code string
		keep                        bool
	}{
		{"egress dial failures", "apiserver_egress_dialer_dial_failure_count", "", "", "", true},
		{"egress dial duration buckets", "apiserver_egress_dialer_dial_duration_seconds_bucket", "", "", "", true},
		{"egress dial duration count", "apiserver_egress_dialer_dial_duration_seconds_count", "", "", "", true},
		{"egress dial duration sum", "apiserver_egress_dialer_dial_duration_seconds_sum", "", "", "", true},
		{"egress dial starts", "apiserver_egress_dialer_dial_start_total", "", "", "", true},
		{"inflight requests", "apiserver_current_inflight_requests", "", "", "", true},
		{"exec requests", "apiserver_request_total", "pods", "exec", "101", true},
		{"exec requests with code 0", "apiserver_request_total", "pods", "exec", "0", true},
		{"exec requests with an error", "apiserver_request_total", "pods", "exec", "500", true},
		{"log requests", "apiserver_request_total", "pods", "log", "200", true},
		{"attach requests", "apiserver_request_total", "pods", "attach", "101", true},
		{"port-forward requests", "apiserver_request_total", "pods", "portforward", "101", true},
		{"pod proxy failures (the pod proxy scrape path)", "apiserver_request_total", "pods", "proxy", "503", true},
		{"server errors of other resources", "apiserver_request_total", "configmaps", "", "500", true},
		{"gateway timeouts of other resources", "apiserver_request_total", "nodes", "status", "504", true},
		{"server errors of other non resource requests", "apiserver_request_total", "", "/metrics", "500", true},
		{"readyz failures", "apiserver_request_total", "", "/readyz", "500", false},
		{"livez failures", "apiserver_request_total", "", "/livez", "500", false},
		{"healthz failures", "apiserver_request_total", "", "/healthz", "500", false},
		{"readyz checks", "apiserver_request_total", "", "/readyz/shutdown", "500", false},
		{"healthy readyz", "apiserver_request_total", "", "/readyz", "200", false},
		{"plain pod gets", "apiserver_request_total", "pods", "", "200", false},
		{"pod status requests", "apiserver_request_total", "pods", "status", "200", false},
		{"exec of another resource", "apiserver_request_total", "services", "exec", "200", false},
		{"client errors", "apiserver_request_total", "configmaps", "", "404", false},
		{"other requests", "apiserver_request_total", "secrets", "", "200", false},
		{"etcd latency", "etcd_request_duration_seconds_bucket", "", "", "", false},
		{"request duration", "apiserver_request_duration_seconds_bucket", "pods", "exec", "200", false},
		{"other egress family", "apiserver_egress_dialer_something_else", "", "", "", false},
		{"prefix of an allowed name", "apiserver_egress_dialer_dial_start_total_extra", "", "", "", false},
		{"go metrics", "go_goroutines", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)

			g.Expect(kept(tt.metric, tt.resource, tt.subresource, tt.code)).To(Equal(tt.keep))
		})
	}
}
