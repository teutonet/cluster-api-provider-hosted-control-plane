/*
Copyright 2022 teuto.net Netzdienste GmbH.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/paused"
)

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=hostedcontrolplanes,scope=Namespaced,categories=cluster-api,shortName=hcp
//+kubebuilder:subresource:status
//+kubebuilder:storageversion
//+kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
//+kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
//+kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type == "Ready")].status`
//+kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
//+kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
//+kubebuilder:metadata:annotations={"cert-manager.io/inject-ca-from=system/controller-manager-serving-certificate"}
//+kubebuilder:metadata:labels={"cluster.x-k8s.io/provider=control-plane-hosted-control-plane","cluster.x-k8s.io/v1beta1=v1alpha1"}

type HostedControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostedControlPlaneSpec   `json:"spec,omitempty"`
	Status HostedControlPlaneStatus `json:"status,omitempty"`
}

var (
	_ conditions.Setter      = &HostedControlPlane{}
	_ paused.ConditionSetter = &HostedControlPlane{}
)

type HostedControlPlaneSpec struct {
	// The Kubernetes version of the cluster.
	//+kubebuilder:validation:Required
	//+kubebuilder:validation:MinLength=1
	//+kubebuilder:validation:MaxLength=256
	Version string `json:"version"`
	//+kubebuilder:default=2
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:validation:Optional
	Replicas *int32 `json:"replicas,omitempty"`
	//+kubebuilder:validation:Optional
	Deployment HostedControlPlaneDeployment `json:"deployment,omitempty"`
}

type HostedControlPlaneDeployment struct {
	//+kubebuilder:validation:Optional
	Scheduler HostedControlPlaneComponent `json:"scheduler,omitempty"`
	//+kubebuilder:validation:Optional
	APIServer HostedControlPlaneComponent `json:"apiServer,omitempty"`
	//+kubebuilder:validation:Optional
	ControllerManager HostedControlPlaneComponent `json:"controllerManager,omitempty"`
	//+kubebuilder:validation:Optional
	Konnectivity HostedControlPlaneComponent `json:"konnectivity,omitempty"`
}

type HostedControlPlaneComponent struct {
	//+kubebuilder:validation:Optional
	Args map[string]string `json:"args,omitempty"`
}

type HostedControlPlaneStatus struct {
	//+kubebuilder:validation:Optional
	Conditions capiv1.Conditions `json:"conditions,omitempty"`

	// Required fields by CAPI
	// https://cluster-api.sigs.k8s.io/developer/providers/contracts/control-plane#controlplane-replicas

	//+kubebuilder:validation:Optional
	Selector string `json:"selector,omitempty"`
	//+kubebuilder:validation:Optional
	Replicas int32 `json:"replicas,omitempty"`
	//+kubebuilder:validation:Optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`
	//+kubebuilder:validation:Optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	//+kubebuilder:validation:Optional
	UnavailableReplicas int32 `json:"unavailableReplicas,omitempty"`

	// CAPI Contract fields
	// https://cluster-api.sigs.k8s.io/developer/providers/contracts/control-plane

	//+kubebuilder:validation:Optional
	Initialized bool `json:"initialized"`
	//+kubebuilder:validation:Optional
	Ready bool `json:"ready"`

	// Compatibility with upstream CAPI v1beta2 fields
	//+kubebuilder:validation:Optional
	V1Beta2 *HostedControlPlaneV1Beta2Status `json:"v1beta2,omitempty"`
}

type HostedControlPlaneV1Beta2Status struct {
	//+kubebuilder:validation:MaxItems=32
	//+kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true

type HostedControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedControlPlane `json:"items"`
}

func (c *HostedControlPlane) GetConditions() capiv1.Conditions {
	return c.Status.Conditions
}

func (c *HostedControlPlane) SetConditions(conditions capiv1.Conditions) {
	c.Status.Conditions = conditions
}

func (c *HostedControlPlane) GetV1Beta2Conditions() []metav1.Condition {
	if c.Status.V1Beta2 == nil {
		return nil
	}
	return c.Status.V1Beta2.Conditions
}

func (c *HostedControlPlane) SetV1Beta2Conditions(conditions []metav1.Condition) {
	if c.Status.V1Beta2 == nil {
		c.Status.V1Beta2 = &HostedControlPlaneV1Beta2Status{}
	}
	c.Status.V1Beta2.Conditions = conditions
}
