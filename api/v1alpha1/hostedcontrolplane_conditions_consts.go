package v1alpha1

import capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

const (
	// ConditionMissingReason is used when trying to mirror a condition that does not
	// exist on the target object.
	ConditionMissingReason = "ConditionMissing"
)

// HostedControlPlane.

const (
	APIServerResourcesReadyCondition       capiv1.ConditionType = "DeploymentReady"
	DeploymentFailedReason                                      = "DeploymentFailed"
	CACertificatesReadyCondition           capiv1.ConditionType = "CACertificatesReady"
	CACertificatesFailedReason                                  = "CACertificatesFailed"
	CertificatesReadyCondition             capiv1.ConditionType = "CertificatesReady"
	CertificatesFailedReason                                    = "CertificatesFailed"
	KubeconfigReadyCondition               capiv1.ConditionType = "KubeconfigReady"
	KubeconfigFailedReason                                      = "KubeconfigFailed"
	KonnectivityConfigReadyCondition       capiv1.ConditionType = "KonnectivityConfigReady"
	KonnectivityConfigFailedReason                              = "KonnectivityConfigFailed"
	TLSRouteReadyCondition                 capiv1.ConditionType = "TLSRouteReady"
	TLSRouteFailedReason                                        = "TLSRouteFailed"
	EtcdClusterReadyCondition              capiv1.ConditionType = "EtcdClusterReady"
	EtcdClusterFailedReason                                     = "EtcdClusterFailed"
	WorkloadClusterResourcesReadyCondition capiv1.ConditionType = "WorkloadSetupReady"
	WorkloadClusterResourcesFailedReason                        = "WorkloadSetupFailed"
	WorkloadRBACReadyCondition             capiv1.ConditionType = "WorkloadRBACReady"
	WorkloadRBACFailedReason                                    = "WorkloadRBACFailed"
	WorkloadClusterInfoReadyCondition      capiv1.ConditionType = "WorkloadClusterInfoReady"
	WorkloadClusterInfoFailedReason                             = "WorkloadClusterInfoFailed"
	WorkloadKubeadmConfigReadyCondition    capiv1.ConditionType = "WorkloadKubeadmConfigReady"
	WorkloadKubeadmConfigFailedReason                           = "WorkloadKubeadmConfigFailed"
	WorkloadKubeletConfigReadyCondition    capiv1.ConditionType = "WorkloadKubeletConfigReady"
	WorkloadKubeletConfigFailedReason                           = "WorkloadKubeletConfigFailed"
)
