package util

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

var (
	HostedControlPlaneControllerName = "hosted-control-plane-controller"
	ApplyOptions                     = metav1.ApplyOptions{
		FieldManager: HostedControlPlaneControllerName,
		Force:        true,
	}
)
