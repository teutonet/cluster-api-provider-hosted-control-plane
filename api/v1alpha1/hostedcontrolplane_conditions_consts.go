package v1alpha1

const (
	// ConditionMissingReason is used when trying to mirror a condition that does not
	// exist on the target object.
	ConditionMissingReason = "ConditionMissing"
)

// HostedControlPlane.

const (
	APIServerServiceReadyCondition         = "APIServerServiceReady"
	APIServerServiceFailedReason           = "APIServerServiceFailed"
	APIServerTLSRoutesReadyCondition       = "APIServerTLSRoutesReady"
	APIServerTLSRoutesFailedReason         = "APIServerTLSRoutesFailed"
	SyncControlPlaneEndpointReadyCondition = "SyncControlPlaneEndpointReady"
	SyncControlPlaneEndpointFailedReason   = "SyncControlPlaneEndpointFailed"
	APIServerDeploymentsReadyCondition     = "APIServerDeploymentsReady"
	APIServerDeploymentsFailedReason       = "APIServerDeploymentsFailed"
	CACertificatesReadyCondition           = "CACertificatesReady"
	CACertificatesFailedReason             = "CACertificatesFailed"
	CertificatesReadyCondition             = "CertificatesReady"
	CertificatesFailedReason               = "CertificatesFailed"
	KubeconfigReadyCondition               = "KubeconfigReady"
	KubeconfigFailedReason                 = "KubeconfigFailed"
	EtcdClusterReadyCondition              = "EtcdClusterReady"
	EtcdClusterFailedReason                = "EtcdClusterFailed"
	WorkloadClusterResourcesReadyCondition = "WorkloadSetupReady"
	WorkloadClusterResourcesFailedReason   = "WorkloadSetupFailed"
	WorkloadRBACReadyCondition             = "WorkloadRBACReady"
	WorkloadRBACFailedReason               = "WorkloadRBACFailed"
	WorkloadClusterInfoReadyCondition      = "WorkloadClusterInfoReady"
	WorkloadClusterInfoFailedReason        = "WorkloadClusterInfoFailed"
	WorkloadKubeadmConfigReadyCondition    = "WorkloadKubeadmConfigReady"
	WorkloadKubeadmConfigFailedReason      = "WorkloadKubeadmConfigFailed"
	WorkloadKubeletConfigReadyCondition    = "WorkloadKubeletConfigReady"
	WorkloadKubeletConfigFailedReason      = "WorkloadKubeletConfigFailed"
	WorkloadKonnectivityReadyCondition     = "WorkloadKonnectivityReady"
	WorkloadKonnectivityFailedReason       = "WorkloadKonnectivityFailed"
	WorkloadCoreDNSReadyCondition          = "WorkloadCoreDNSReady"
	WorkloadCoreDNSFailedReason            = "WorkloadCoreDNSFailed"
	WorkloadKubeProxyReadyCondition        = "WorkloadKubeProxyReady"
	WorkloadKubeProxyFailedReason          = "WorkloadKubeProxyFailed"
)
