// Package names provides consistent naming for multiple resource types.
package names

import (
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
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

func GetAdminKubeconfigCertificateSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-admin", controlPlaneName)
}

func GetControllerManagerKubeconfigCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller-manager", controlPlaneName)
}

func GetControllerManagerKubeconfigCertificateSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller-manager", controlPlaneName)
}

func GetSchedulerKubeconfigCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-scheduler", controlPlaneName)
}

func GetSchedulerKubeconfigCertificateSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-scheduler", controlPlaneName)
}

func GetKonnectivityConfigMapName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity", controlPlaneName)
}

func GetKonnectivityClientKubeconfigCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity-client", controlPlaneName)
}

func GetKonnectivityClientKubeconfigCertificateSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity-client", controlPlaneName)
}

func GetControllerKubeconfigCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller", controlPlaneName)
}

func GetControllerKubeconfigCertificateSecretName(controlPlaneName string) string {
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

func GetEtcdServiceName(controlPlaneName string) string {
	return fmt.Sprintf("e-%s", controlPlaneName)
}

func GetEtcdStatefulSetName(controlPlaneName string) string {
	return fmt.Sprintf("%s-etcd", controlPlaneName)
}

func GetInternalServiceEndpoint(controlPlaneName string, namespace string) string {
	return fmt.Sprintf("%s.%s.svc", GetServiceName(controlPlaneName), namespace)
}

func GetEtcdDNSNames(hostedControlPlane *v1alpha1.HostedControlPlane) map[string]string {
	serviceName := GetEtcdServiceName(hostedControlPlane.Name)
	return slices.Associate(slices.RepeatBy(3, func(i int) string {
		return fmt.Sprintf("%s-%d", GetEtcdStatefulSetName(hostedControlPlane.Name), i)
	}), func(host string) (string, string) {
		return host, fmt.Sprintf("%s.%s.%s.svc", host, serviceName, hostedControlPlane.Namespace)
	})
}
