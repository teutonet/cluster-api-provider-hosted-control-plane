package monitoring

import (
	"testing"

	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringfake "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/fake"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func prometheus(
	name string,
	podMonitorSelector *metav1.LabelSelector,
	scrapeConfigSelector *metav1.LabelSelector,
) *monitoringv1.Prometheus {
	return &monitoringv1.Prometheus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "monitoring"},
		Spec: monitoringv1.PrometheusSpec{
			CommonPrometheusFields: monitoringv1.CommonPrometheusFields{
				PodMonitorSelector:   podMonitorSelector,
				ScrapeConfigSelector: scrapeConfigSelector,
			},
		},
	}
}

func hcpWithPrometheus(spec *v1alpha1.PrometheusSpec) *v1alpha1.HostedControlPlane {
	return &v1alpha1.HostedControlPlane{
		Spec: v1alpha1.HostedControlPlaneSpec{
			HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
				Monitoring: v1alpha1.MonitoringSpec{Prometheus: spec},
			},
		},
	}
}

func TestResolve(t *testing.T) {
	selector := func(labels map[string]string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: labels}
	}
	tests := []struct {
		name           string
		noClient       bool
		prometheuses   []runtime.Object
		spec           *v1alpha1.PrometheusSpec
		enabled        bool
		labels         map[string]string
		notReadyReason string
	}{
		{
			name:           "CRDs not installed",
			noClient:       true,
			notReadyReason: v1alpha1.MonitoringPrometheusOperatorMissingReason,
		},
		{
			name:     "disabled wins over missing CRDs",
			noClient: true,
			spec:     &v1alpha1.PrometheusSpec{Disabled: new(true)},
		},
		{
			name:         "disabled wins over a single Prometheus",
			prometheuses: []runtime.Object{prometheus("a", selector(nil), selector(nil))},
			spec:         &v1alpha1.PrometheusSpec{Disabled: new(true)},
		},
		{
			name:           "no Prometheus",
			notReadyReason: v1alpha1.MonitoringPrometheusMissingReason,
		},
		{
			name: "single Prometheus, labels of both selectors",
			prometheuses: []runtime.Object{
				prometheus(
					"a",
					selector(map[string]string{"release": "stack"}),
					selector(map[string]string{"scrape": "me"}),
				),
			},
			enabled: true,
			labels:  map[string]string{"release": "stack", "scrape": "me"},
		},
		{
			name:         "single Prometheus selecting everything",
			prometheuses: []runtime.Object{prometheus("a", selector(nil), selector(nil))},
			enabled:      true,
			labels:       map[string]string{},
		},
		{
			name:           "single Prometheus selecting nothing",
			prometheuses:   []runtime.Object{prometheus("a", nil, nil)},
			notReadyReason: v1alpha1.MonitoringPrometheusSelectsNothingReason,
		},
		{
			name: "single Prometheus with match expressions",
			prometheuses: []runtime.Object{prometheus("a", &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "a", Operator: metav1.LabelSelectorOpExists}},
			}, nil)},
			notReadyReason: v1alpha1.MonitoringPrometheusSelectorUnsupportedReason,
		},
		{
			name: "multiple Prometheus",
			prometheuses: []runtime.Object{
				prometheus("a", selector(nil), selector(nil)),
				prometheus("b", selector(nil), selector(nil)),
			},
			notReadyReason: v1alpha1.MonitoringPrometheusAmbiguousReason,
		},
		{
			name: "multiple Prometheus with explicit labels",
			prometheuses: []runtime.Object{
				prometheus("a", selector(nil), selector(nil)),
				prometheus("b", selector(nil), selector(nil)),
			},
			spec:    &v1alpha1.PrometheusSpec{MonitorLabels: map[string]string{"prometheus": "b"}},
			enabled: true,
			labels:  map[string]string{"prometheus": "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, ctx, _ := G(t)

			var target Target
			var err error
			if tt.noClient {
				target, err = Resolve(ctx, nil, hcpWithPrometheus(tt.spec))
			} else {
				target, err = Resolve(ctx, monitoringfake.NewSimpleClientset(tt.prometheuses...), hcpWithPrometheus(tt.spec))
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(target.Enabled).To(Equal(tt.enabled))
			g.Expect(target.NotReadyReason).To(Equal(tt.notReadyReason))
			if tt.notReadyReason != "" {
				g.Expect(target.NotReadyMessage).NotTo(BeEmpty())
			}
			if tt.enabled {
				g.Expect(target.MonitorLabels).To(Equal(tt.labels))
			}
		})
	}
}
