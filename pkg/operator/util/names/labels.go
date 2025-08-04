package names

import (
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func GetLabels(controlPlaneName string) map[string]string {
	return map[string]string{
		capiv1.ClusterNameLabel: controlPlaneName,
	}
}

func GetSelector(controlPlaneName string) map[string]string {
	return GetLabels(controlPlaneName)
}
