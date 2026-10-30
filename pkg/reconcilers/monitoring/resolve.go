package monitoring

import (
	"context"
	"fmt"
	"maps"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KonnectivityAgentMetricsPort is the port of the konnectivity agent serving /metrics in the workload cluster.
	KonnectivityAgentMetricsPort int32 = 8134
	// CoreDNSMetricsPort is the port of CoreDNS serving /metrics in the workload cluster.
	CoreDNSMetricsPort int32 = 9153
)

// Target describes what to do with the monitoring resources of a HostedControlPlane.
type Target struct {
	// Enabled says whether the monitoring resources should exist.
	Enabled bool
	// MonitorLabels are put on the monitors, so the Prometheus selects them.
	MonitorLabels map[string]string
	// NotReadyReason is the condition reason saying why monitoring is not set up, empty if everything is fine.
	NotReadyReason string
	// NotReadyMessage explains NotReadyReason.
	NotReadyMessage string
}

// Resolve determines whether and how the HostedControlPlane is monitored.
// The monitoringClient is nil if the Prometheus Operator CRDs are not installed.
func Resolve(
	ctx context.Context,
	monitoringClient monitoringclient.Interface,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (Target, error) {
	prometheusSpec := hostedControlPlane.Spec.Monitoring.Prometheus
	if prometheusSpec.IsDisabled() {
		return Target{}, nil
	}
	if monitoringClient == nil {
		return Target{
			NotReadyReason:  v1alpha1.MonitoringPrometheusOperatorMissingReason,
			NotReadyMessage: "Prometheus Operator CRDs not installed",
		}, nil
	}
	if prometheusSpec != nil && len(prometheusSpec.MonitorLabels) > 0 {
		return Target{Enabled: true, MonitorLabels: maps.Clone(prometheusSpec.MonitorLabels)}, nil
	}

	prometheuses, err := monitoringClient.MonitoringV1().Prometheuses(metav1.NamespaceAll).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return Target{}, fmt.Errorf("failed to list prometheuses: %w", err)
	}
	switch len(prometheuses.Items) {
	case 0:
		return Target{
			NotReadyReason:  v1alpha1.MonitoringPrometheusMissingReason,
			NotReadyMessage: "no Prometheus found",
		}, nil
	case 1:
		return targetFromPrometheus(&prometheuses.Items[0]), nil
	default:
		return Target{
			NotReadyReason:  v1alpha1.MonitoringPrometheusAmbiguousReason,
			NotReadyMessage: "multiple Prometheus found, set spec.monitoring.prometheus.monitorLabels",
		}, nil
	}
}

func targetFromPrometheus(prometheus *monitoringv1.Prometheus) Target {
	selectors := []*metav1.LabelSelector{
		prometheus.Spec.PodMonitorSelector,
		prometheus.Spec.ScrapeConfigSelector,
	}
	labels := map[string]string{}
	selectsAnything := false
	for _, selector := range selectors {
		if selector == nil {
			continue
		}
		if len(selector.MatchExpressions) > 0 {
			return Target{
				NotReadyReason: v1alpha1.MonitoringPrometheusSelectorUnsupportedReason,
				NotReadyMessage: fmt.Sprintf(
					"Prometheus %s/%s uses match expressions in its monitor selectors, "+
						"set spec.monitoring.prometheus.monitorLabels",
					prometheus.Namespace, prometheus.Name,
				),
			}
		}
		selectsAnything = true
		maps.Copy(labels, selector.MatchLabels)
	}
	if !selectsAnything {
		return Target{
			NotReadyReason: v1alpha1.MonitoringPrometheusSelectsNothingReason,
			NotReadyMessage: fmt.Sprintf(
				"Prometheus %s/%s selects no PodMonitors and no ScrapeConfigs",
				prometheus.Namespace, prometheus.Name,
			),
		}
	}
	return Target{Enabled: true, MonitorLabels: labels}
}
