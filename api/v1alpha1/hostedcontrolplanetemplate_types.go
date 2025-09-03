package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=hostedcontrolplanetemplates,scope=Namespaced,categories=cluster-api,shortName=hcpt
//+kubebuilder:storageversion
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation of HostedControlPlaneTemplate"
//+kubebuilder:metadata:annotations={"cert-manager.io/inject-ca-from=system/controller-manager-serving-certificate"}
//+kubebuilder:metadata:labels={"cluster.x-k8s.io/provider=control-plane-hosted-control-plane","cluster.x-k8s.io/v1beta1=v1alpha1"}

type HostedControlPlaneTemplate struct {
	metav1.TypeMeta `json:",inline"`
	//+kubebuilder:validation:Optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	//+kubebuilder:validation:Optional
	Spec HostedControlPlaneTemplateSpec `json:"spec,omitempty"`
}

type HostedControlPlaneTemplateSpec struct {
	//+kubebuilder:validation:Required
	Template HostedControlPlaneTemplateResource `json:"template"`
}

type HostedControlPlaneTemplateResource struct {
	//+kubebuilder:validation:Optional
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`

	//+kubebuilder:validation:Required
	Spec HostedControlPlaneInlineSpec `json:"spec"`
}

//+kubebuilder:object:root=true

type HostedControlPlaneTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	//+kubebuilder:validation:Optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedControlPlaneTemplate `json:"items"`
}
