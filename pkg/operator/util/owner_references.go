package util

import (
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
)

func GetOwnerReferenceApplyConfiguration(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *metav1ac.OwnerReferenceApplyConfiguration {
	return metav1ac.OwnerReference().
		WithAPIVersion(hostedControlPlane.APIVersion).
		WithKind(hostedControlPlane.Kind).
		WithName(hostedControlPlane.Name).
		WithUID(hostedControlPlane.UID).
		WithController(true).
		WithBlockOwnerDeletion(true)
}
