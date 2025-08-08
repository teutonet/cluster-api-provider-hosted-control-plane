// Package names provides consistent naming for multiple resource types.
package names

import (
	"fmt"
)

func GetRootIssuerName(controlPlaneName string) string {
	return fmt.Sprintf("%s-root-issuer", controlPlaneName)
}

func GetCAIssuerName(controlPlaneName string) string {
	return fmt.Sprintf("%s-ca", controlPlaneName)
}

func GetCASecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-ca", controlPlaneName)
}

func GetCACertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-ca", controlPlaneName)
}

func GetAPIServerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver", controlPlaneName)
}

func GetAPIServerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver", controlPlaneName)
}

func GetAPIServerKubeletClientCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver-kubelet-client", controlPlaneName)
}

func GetAPIServerKubeletClientSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver-kubelet-client", controlPlaneName)
}

func GetFrontProxyCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy", controlPlaneName)
}

func GetFrontProxySecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy", controlPlaneName)
}

func GetFrontProxyCAName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy-ca", controlPlaneName)
}

func GetFrontProxyCASecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy-ca", controlPlaneName)
}

func GetServiceAccountCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-service-account", controlPlaneName)
}

func GetServiceAccountSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-service-account", controlPlaneName)
}

// Services cannot start with numbers, so we prefix with "s-".
func GetServiceName(controlPlaneName string) string {
	return fmt.Sprintf("s-%s", controlPlaneName)
}

func GetAdminCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-admin", controlPlaneName)
}

func GetAdminSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-admin", controlPlaneName)
}

func GetControllerManagerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller-manager", controlPlaneName)
}

func GetControllerManagerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller-manager", controlPlaneName)
}

func GetSchedulerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-scheduler", controlPlaneName)
}

func GetSchedulerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-scheduler", controlPlaneName)
}

func GetKonnectivityConfigMapName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity", controlPlaneName)
}

func GetKonnectivityClientCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity-client", controlPlaneName)
}

func GetKonnectivityClientSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity-client", controlPlaneName)
}

func GetControllerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller", controlPlaneName)
}

func GetControllerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller", controlPlaneName)
}

func GetKubeconfigSecretName(controlPlaneName string, kubeconfigName string) string {
	return fmt.Sprintf("%s-%s-kubeconfig", controlPlaneName, kubeconfigName)
}

func GetEtcdCAName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-ca", controlPlaneName)
}

func GetEtcdCASecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-ca", controlPlaneName)
}

func GetEtcdServerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-server", controlPlaneName)
}

func GetEtcdServerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-server", controlPlaneName)
}

func GetEtcdPeerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-peer", controlPlaneName)
}

func GetEtcdPeerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-peer", controlPlaneName)
}

func GetEtcdAPIServerClientCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver-etcd-client", controlPlaneName)
}

func GetEtcdAPIServerClientSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver-etcd-client", controlPlaneName)
}

func GetEtcdBackupCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-backup", controlPlaneName)
}

func GetEtcdBackupSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd-backup", controlPlaneName)
}
