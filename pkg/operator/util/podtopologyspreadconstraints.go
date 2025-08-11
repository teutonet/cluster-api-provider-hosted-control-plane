package util

import (
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
)

func CreatePodTopologySpreadConstraints(
	labelSelector *metav1ac.LabelSelectorApplyConfiguration,
) *corev1ac.TopologySpreadConstraintApplyConfiguration {
	return corev1ac.TopologySpreadConstraint().
		WithTopologyKey(corev1.LabelHostname).
		WithLabelSelector(labelSelector).
		WithMaxSkew(1).
		WithWhenUnsatisfiable(corev1.ScheduleAnyway)
}
