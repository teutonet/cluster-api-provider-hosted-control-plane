// Package names provides consistent naming for multiple resource types.
package names

import (
	"fmt"

	slices "github.com/samber/lo"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func GetRootIssuerName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-root"
}

func GetCAIssuerName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-ca"
}

func GetCASecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-ca"
}

func GetCACertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-ca"
}

func GetAPIServerCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-apiserver"
}

func GetAPIServerSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-apiserver"
}

func GetAPIServerKubeletClientCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-apiserver-kubelet-client"
}

func GetAPIServerKubeletClientSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-apiserver-kubelet-client"
}

func GetFrontProxyCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-front-proxy"
}

func GetFrontProxySecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-front-proxy"
}

func GetFrontProxyCAName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-front-proxy-ca"
}

func GetFrontProxyCASecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-front-proxy-ca"
}

func GetServiceAccountCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-service-account"
}

func GetServiceAccountSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-service-account"
}

func GetServiceName(cluster *capiv1.Cluster) string {
	return "s-" + cluster.Name
}

func GetAdminCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-admin"
}

func GetAdminKubeconfigCertificateSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-admin"
}

func GetControllerManagerKubeconfigCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-controller-manager"
}

func GetControllerManagerKubeconfigCertificateSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-controller-manager"
}

func GetSchedulerKubeconfigCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-scheduler"
}

func GetSchedulerKubeconfigCertificateSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-scheduler"
}

func GetKonnectivityConfigMapName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-konnectivity"
}

func GetKonnectivityClientKubeconfigCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-konnectivity-client"
}

func GetKonnectivityClientKubeconfigCertificateSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-konnectivity-client"
}

func GetControllerKubeconfigCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-controller"
}

func GetControllerKubeconfigCertificateSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-controller"
}

func GetKubeconfigSecretName(cluster *capiv1.Cluster, kubeconfigName string) string {
	return cluster.Name + "-" + kubeconfigName + "-kubeconfig"
}

func GetEtcdCAName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-ca"
}

func GetEtcdCASecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-ca"
}

func GetEtcdServerCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-server"
}

func GetEtcdServerSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-server"
}

func GetEtcdPeerCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-peer"
}

func GetEtcdPeerSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-peer"
}

func GetEtcdAPIServerClientCertificateName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-apiserver-client"
}

func GetEtcdAPIServerClientSecretName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd-apiserver-client"
}

func GetEtcdServiceName(cluster *capiv1.Cluster) string {
	return "e-" + cluster.Name + "-etcd"
}

func GetEtcdStatefulSetName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-etcd"
}

func GetInternalServiceHost(cluster *capiv1.Cluster) string {
	return GetServiceName(cluster) + "." + cluster.Namespace + ".svc"
}

func GetEtcdDNSNames(cluster *capiv1.Cluster) map[string]string {
	serviceName := GetEtcdServiceName(cluster)
	return slices.Associate(slices.RepeatBy(3, func(i int) string {
		return fmt.Sprintf("%s-%d", GetEtcdStatefulSetName(cluster), i)
	}), func(host string) (string, string) {
		return host, fmt.Sprintf("%s.%s.%s.svc", host, serviceName, cluster.Namespace)
	})
}

func GetTLSRouteName(cluster *capiv1.Cluster) string {
	return cluster.Name
}

func GetKonnectivityTLSRouteName(cluster *capiv1.Cluster) string {
	return cluster.Name + "-konnectivity"
}

func GetKonnectivityServerHost(cluster *capiv1.Cluster) string {
	return fmt.Sprintf("k-%s", cluster.Spec.ControlPlaneEndpoint.Host)
}
