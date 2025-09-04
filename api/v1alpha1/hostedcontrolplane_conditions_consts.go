package v1alpha1

import clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

const (
	// ConditionMissingReason is used when trying to mirror a condition that does not
	// exist on the target object.
	ConditionMissingReason = "ConditionMissing"
)

// HostedControlPlane.

const (
	DeploymentReadyCondition   clusterv1.ConditionType = "DeploymentReady"
	DeploymentFailedReason                             = "DeploymentFailed"
	CertificatesReadyCondition clusterv1.ConditionType = "CertificatesReady"
	CertificatesFailedReason                           = "CertificatesFailed"
)
