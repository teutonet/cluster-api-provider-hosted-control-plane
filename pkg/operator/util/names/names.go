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
