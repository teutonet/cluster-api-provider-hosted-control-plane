//+kubebuilder:object:generate=true
//+groupName=controlplane.cluster.x-k8s.io

// Package v1alpha1 contains API Schema definitions for the v1alpha1 API group.
package v1alpha1

import (
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: api.GroupName, Version: "v1alpha1"}
	localSchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = localSchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&HostedControlPlane{},
		&HostedControlPlaneList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
