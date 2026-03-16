package names

import (
	slices "github.com/samber/lo"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type CertificateKind string

const (
	CACertificateKind     CertificateKind = "ca"
	ClientCertificateKind CertificateKind = "client"
)

const (
	KubeconfigLabel         = "controlplane.cluster.x-k8s.io/kubeconfig"
	KubeconfigUsernameLabel = "controlplane.cluster.x-k8s.io/kubeconfig-username"

	CertificateKindLabel = "controlplane.cluster.x-k8s.io/certificate-kind"
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

func GetKubeconfigUserLabel(username string) map[string]string {
	return map[string]string{
		KubeconfigUsernameLabel: username,
	}
}

func GetKubeconfigLabel() map[string]string {
	return map[string]string{
		KubeconfigLabel: "true",
	}
}

func GetKubeconfigLabels(username string) map[string]string {
	return slices.Assign(
		GetKubeconfigLabel(),
		GetKubeconfigUserLabel(username),
	)
}
