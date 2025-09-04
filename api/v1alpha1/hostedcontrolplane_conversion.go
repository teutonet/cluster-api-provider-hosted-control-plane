package v1alpha1

import "sigs.k8s.io/controller-runtime/pkg/conversion"

var _ conversion.Hub = &HostedControlPlane{}

func (*HostedControlPlane) Hub()     {}
func (*HostedControlPlaneList) Hub() {}
