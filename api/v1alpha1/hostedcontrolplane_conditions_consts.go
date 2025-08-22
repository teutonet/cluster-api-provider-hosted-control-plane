package v1alpha1

import capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

const (
	// ConditionMissingReason is used when trying to mirror a condition that does not
	// exist on the target object.
	ConditionMissingReason = "ConditionMissing"
)

// HostedControlPlane.

const (
	APIServerServiceReadyCondition         capiv1.ConditionType = "APIServerServiceReady"
	APIServerServiceFailedReason                                = "APIServerServiceFailed"
	APIServerTLSRoutesReadyCondition       capiv1.ConditionType = "APIServerTLSRoutesReady"
	APIServerTLSRoutesFailedReason                              = "APIServerTLSRoutesFailed"
	SyncControlPlaneEndpointReadyCondition capiv1.ConditionType = "SyncControlPlaneEndpointReady"
	SyncControlPlaneEndpointFailedReason                        = "SyncControlPlaneEndpointFailed"
	APIServerDeploymentsReadyCondition     capiv1.ConditionType = "APIServerDeploymentsReady"
	APIServerDeploymentsFailedReason                            = "APIServerDeploymentsFailed"
	CACertificatesReadyCondition           capiv1.ConditionType = "CACertificatesReady"
	CACertificatesFailedReason                                  = "CACertificatesFailed"
	CertificatesReadyCondition             capiv1.ConditionType = "CertificatesReady"
	CertificatesFailedReason                                    = "CertificatesFailed"
	KubeconfigReadyCondition               capiv1.ConditionType = "KubeconfigReady"
	KubeconfigFailedReason                                      = "KubeconfigFailed"
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
	WorkloadKonnectivityReadyCondition     capiv1.ConditionType = "WorkloadKonnectivityReady"
	WorkloadKonnectivityFailedReason                            = "WorkloadKonnectivityFailed"
	WorkloadCoreDNSReadyCondition          capiv1.ConditionType = "WorkloadCoreDNSReady"
	WorkloadCoreDNSFailedReason                                 = "WorkloadCoreDNSFailed"
	WorkloadKubeProxyReadyCondition        capiv1.ConditionType = "WorkloadKubeProxyReady"
	WorkloadKubeProxyFailedReason                               = "WorkloadKubeProxyFailed"
)
