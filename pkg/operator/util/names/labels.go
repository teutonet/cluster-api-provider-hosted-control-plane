package names

import (
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func GetControlPlaneLabels(cluster *capiv1.Cluster, component string) map[string]string {
	labels := map[string]string{
		"cluster.x-k8s.io/cluster-name": cluster.Name,
	}
	if component != "" {
		labels["component"] = component
	}
	return labels
}

func GetControlPlaneSelector(cluster *capiv1.Cluster, component string) *metav1ac.LabelSelectorApplyConfiguration {
	selector := metav1ac.LabelSelector()
	selector.WithMatchLabels(map[string]string{
		"cluster.x-k8s.io/cluster-name": cluster.Name,
	})
	if component != "" {
		selector.WithMatchLabels(map[string]string{
			"component": component,
		})
	}
	return selector
}
