// Package names provides consistent naming for multiple resource types.
package names

import (
	"fmt"

	slices "github.com/samber/lo"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func GetRootIssuerName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-root"
}

func GetCAIssuerName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-ca"
}

func GetCASecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-ca"
}

func GetCACertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-ca"
}

func GetAPIServerCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-apiserver"
}

func GetAPIServerSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-apiserver"
}

func GetAPIServerKubeletClientCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-apiserver-kubelet-client"
}

func GetAPIServerKubeletClientSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-apiserver-kubelet-client"
}

func GetFrontProxyCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-front-proxy"
}

func GetFrontProxySecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-front-proxy"
}

func GetFrontProxyCAName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-front-proxy-ca"
}

func GetFrontProxyCASecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-front-proxy-ca"
}

func GetServiceAccountCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-service-account"
}

func GetServiceAccountSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-service-account"
}

func GetServiceName(cluster *capiv2.Cluster) string {
	return "s-" + cluster.Name
}

func GetAdminCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-admin"
}

func GetAdminKubeconfigCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-admin"
}

func GetAdminKubeconfigCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-admin"
}

func GetControllerManagerKubeconfigCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-controller-manager"
}

func GetControllerManagerKubeconfigCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-controller-manager"
}

func GetSchedulerKubeconfigCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-scheduler"
}

func GetSchedulerKubeconfigCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-scheduler"
}

func GetKonnectivityConfigMapName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-konnectivity"
}

func GetAuditWebhookSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-audit-webhook"
}

func GetKonnectivityClientKubeconfigCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-konnectivity-client"
}

func GetKonnectivityClientKubeconfigCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-konnectivity-client"
}

func GetControlPlaneControllerKubeconfigCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-control-plane-controller"
}

func GetControlPlaneControllerKubeconfigCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-control-plane-controller"
}

func GetKubeconfigSecretName(cluster *capiv2.Cluster, username string) string {
	return cluster.Name + "-" + username + "-kubeconfig"
}

func GetCustomKubeconfigCertificateName(cluster *capiv2.Cluster, username string) string {
	return cluster.Name + "-custom-" + username
}

func GetCustomKubeconfigSecretName(cluster *capiv2.Cluster, username string) string {
	return cluster.Name + "-" + username + "-kubeconfig"
}

func GetEtcdCAName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-ca"
}

func GetEtcdCASecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-ca"
}

func GetEtcdServerCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-server"
}

func GetEtcdServerSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-server"
}

func GetEtcdPeerCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-peer"
}

func GetEtcdPeerSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-peer"
}

func GetEtcdAPIServerClientCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-apiserver-client"
}

func GetEtcdAPIServerClientCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-apiserver-client"
}

func GetEtcdControllerClientCertificateName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-controller-client"
}

func GetEtcdControllerClientCertificateSecretName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd-controller-client"
}

func GetEtcdClientServiceName(cluster *capiv2.Cluster) string {
	return GetEtcdServiceName(cluster) + "-client"
}

func GetEtcdClientServiceDNSName(cluster *capiv2.Cluster) string {
	return GetEtcdClientServiceName(cluster) + "." + cluster.Namespace + ".svc"
}

func GetEtcdServiceName(cluster *capiv2.Cluster) string {
	return "e-" + cluster.Name + "-etcd"
}

func GetEtcdStatefulSetName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-etcd"
}

func GetInternalServiceHost(cluster *capiv2.Cluster) string {
	return GetServiceName(cluster) + "." + cluster.Namespace + ".svc"
}

func GetEtcdDNSNames(cluster *capiv2.Cluster) map[string]string {
	serviceName := GetEtcdServiceName(cluster)
	return slices.Associate(slices.RepeatBy(3, func(i int) string {
		return fmt.Sprintf("%s-%d", GetEtcdStatefulSetName(cluster), i)
	}), func(host string) (string, string) {
		return host, fmt.Sprintf("%s.%s.%s.svc", host, serviceName, cluster.Namespace)
	})
}

func GetTLSRouteName(cluster *capiv2.Cluster) string {
	return cluster.Name
}

func GetKonnectivityTLSRouteName(cluster *capiv2.Cluster) string {
	return cluster.Name + "-konnectivity"
}

func GetKonnectivityServerHost(cluster *capiv2.Cluster) string {
	return fmt.Sprintf("konnectivity.%s", cluster.Spec.ControlPlaneEndpoint.Host)
}
