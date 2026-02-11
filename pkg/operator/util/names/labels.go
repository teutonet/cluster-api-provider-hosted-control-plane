package names

import (
	slices "github.com/samber/lo"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	CustomKubeconfigLabel         = "controlplane.cluster.x-k8s.io/custom-kubeconfig"
	CustomKubeconfigUsernameLabel = "controlplane.cluster.x-k8s.io/custom-kubeconfig-username"
)

func GetControlPlaneLabels(cluster *capiv2.Cluster, component string) map[string]string {
	labels := map[string]string{
		"cluster.x-k8s.io/cluster-name": cluster.Name,
	}
	if component != "" {
		labels["app.kubernetes.io/component"] = component
	}
	return labels
}

func GetControlPlaneSelector(cluster *capiv2.Cluster, component string) *metav1ac.LabelSelectorApplyConfiguration {
	labels := GetControlPlaneLabels(cluster, component)
	selector := metav1ac.LabelSelector()
	return selector.WithMatchLabels(labels)
}

func GetCustomKubeconfigUserLabel(username string) map[string]string {
	return map[string]string{
		CustomKubeconfigUsernameLabel: username,
	}
}

func GetCustomKubeconfigLabel() map[string]string {
	return map[string]string{
		CustomKubeconfigLabel: "true",
	}
}

func GetCustomKubeconfigLabels(username string) map[string]string {
	return slices.Assign(
		GetCustomKubeconfigLabel(),
		GetCustomKubeconfigUserLabel(username),
	)
}
