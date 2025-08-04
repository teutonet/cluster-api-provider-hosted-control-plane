// Package names provides consistent naming for multiple resource types.
package names

import (
	"fmt"
)

// GetUserName returns the name for an OpenStack user for the cluster.
func GetUserName(clusterName string) string {
	return fmt.Sprintf("capi-%s", clusterName)
}

// GetUserCredentialsSecretName returns the name of the user credentials secret for the cluster.
func GetUserCredentialsSecretName(clusterName string) string {
	return fmt.Sprintf("%s-user-credentials", clusterName)
}

// GetCloudConfigSecretName returns the name of the cloud config secret for the cluster.
func GetCloudConfigSecretName(clusterName string) string {
	return fmt.Sprintf("%s-cloud-config", clusterName)
}

// GetCCMCloudConfigSecretName returns the name of the cloud config secret for the cluster.
// To be compatible with pre-operator clusters, this does not include the cluster name.
func GetCCMCloudConfigSecretName(clusterName string) string {
	return fmt.Sprintf("%s-ccm-cloud-config", clusterName)
}

// GetDescription returns a human-readable description that can be attached to other objects.
// This description describes the source of the object and includes the cluster name.
func GetDescription(clusterName string) string {
	return fmt.Sprintf("Created by t8s-engine cluster %s", clusterName)
}

// GetControlPlaneServerGroupName returns the name of the OpenStack Server
// Group for control plane machines.
func GetControlPlaneServerGroupName() string {
	return "control-plane"
}

// GetNodePoolDefaultServerGroupName returns the name of the default OpenStack Server
// Group used for all worker groups.
func GetNodePoolDefaultServerGroupName() string {
	return "compute-plane"
}

func GetRootIssuerName(controlPlaneName string) string {
	return fmt.Sprintf("%s-root-issuer", controlPlaneName)
}

// GetCAIssuerName returns the name for the CA issuer.
func GetCAIssuerName(controlPlaneName string) string {
	return fmt.Sprintf("%s-ca", controlPlaneName)
}

// GetCASecretName returns the name for the CA secret.
func GetCASecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-ca", controlPlaneName)
}

// GetCACertificateName returns the name for the CA certificate.
func GetCACertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-ca", controlPlaneName)
}

// GetAPIServerCertificateName returns the name for the API server certificate.
func GetAPIServerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver", controlPlaneName)
}

// GetAPIServerSecretName returns the name for the API server secret.
func GetAPIServerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-apiserver", controlPlaneName)
}

// GetFrontProxyCertificateName returns the name for the front-proxy certificate.
func GetFrontProxyCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy", controlPlaneName)
}

// GetFrontProxySecretName returns the name for the front-proxy secret.
func GetFrontProxySecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy", controlPlaneName)
}

// GetFrontProxyCAName returns the name for the front-proxy CA.
func GetFrontProxyCAName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy-ca", controlPlaneName)
}

// GetFrontProxyCASecretName returns the name for the front-proxy CA secret.
func GetFrontProxyCASecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-front-proxy-ca", controlPlaneName)
}

// GetServiceAccountCertificateName returns the name for the service account certificate.
func GetServiceAccountCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-service-account", controlPlaneName)
}

// GetServiceAccountSecretName returns the name for the service account secret.
func GetServiceAccountSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-service-account", controlPlaneName)
}

// GetServiceName returns the name for the service.
// Services cannot start with numbers, so we prefix with "s-".
func GetServiceName(controlPlaneName string) string {
	return fmt.Sprintf("s-%s", controlPlaneName)
}

// GetAdminCertificateName returns the name for the admin certificate.
func GetAdminCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-admin", controlPlaneName)
}

// GetAdminSecretName returns the name for the admin secret.
func GetAdminSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-admin", controlPlaneName)
}

// GetControllerManagerCertificateName returns the name for the controller-manager certificate.
func GetControllerManagerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller-manager", controlPlaneName)
}

// GetControllerManagerSecretName returns the name for the controller-manager secret.
func GetControllerManagerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-controller-manager", controlPlaneName)
}

// GetSchedulerCertificateName returns the name for the scheduler certificate.
func GetSchedulerCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-scheduler", controlPlaneName)
}

// GetSchedulerSecretName returns the name for the scheduler secret.
func GetSchedulerSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-scheduler", controlPlaneName)
}

func GetKonnectivityConfigMapName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity", controlPlaneName)
}

// GetKonnectivityClientCertificateName returns the name for the konnectivity client certificate.
func GetKonnectivityClientCertificateName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity-client", controlPlaneName)
}

// GetKonnectivityClientSecretName returns the name for the konnectivity client secret.
func GetKonnectivityClientSecretName(controlPlaneName string) string {
	return fmt.Sprintf("%s-konnectivity-client", controlPlaneName)
}
