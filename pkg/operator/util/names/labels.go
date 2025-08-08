package names

import (
	slices "github.com/samber/lo"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func GetControlPlaneLabels(controlPlaneName string) map[string]string {
	return map[string]string{
		capiv1.ClusterNameLabel: controlPlaneName,
	}
}

func GetControlPlaneSelector(controlPlaneName string) *metav1ac.LabelSelectorApplyConfiguration {
	return metav1ac.LabelSelector().
		WithMatchLabels(GetControlPlaneLabels(controlPlaneName))
}

func GetEtcdLabels(controlPlaneName string) map[string]string {
	return slices.Assign(GetControlPlaneLabels(controlPlaneName), map[string]string{
		"component": "etcd",
	})
}

func GetEtcdSelector(controlPlaneName string) *metav1ac.LabelSelectorApplyConfiguration {
	return metav1ac.LabelSelector().
		WithMatchLabels(GetEtcdLabels(controlPlaneName))
}
