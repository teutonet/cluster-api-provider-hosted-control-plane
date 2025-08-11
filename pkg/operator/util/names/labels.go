package names

import (
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func GetControlPlaneLabels(controlPlaneName string, component string) map[string]string {
	labels := map[string]string{
		capiv1.ClusterNameLabel: controlPlaneName,
	}
	if component != "" {
		labels["component"] = component
	}
	return labels
}

func GetControlPlaneSelector(controlPlaneName string, component string) *metav1ac.LabelSelectorApplyConfiguration {
	return metav1ac.LabelSelector().
		WithMatchLabels(GetControlPlaneLabels(controlPlaneName, component))
}
