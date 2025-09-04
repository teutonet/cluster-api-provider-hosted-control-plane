/*
Copyright 2022 teuto.net Netzdienste GmbH.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/paused"
)

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=hostedcontrolplanes,scope=Namespaced,categories=cluster-api,shortName=hcp
//+kubebuilder:subresource:status
//+kubebuilder:storageversion
//+kubebuilder:printcolumn:name="Initialized",type=boolean,JSONPath=`.status.initialized`
//+kubebuilder:printcolumn:name="API Server Available",type=string,JSONPath=`.status.conditions[?(@.type == "Ready")].status`
//+kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
//+kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
//+kubebuilder:printcolumn:name="Updated",type=integer,JSONPath=`.status.updatedReplicas`
//+kubebuilder:printcolumn:name="Unavailable",type=integer,JSONPath=`.status.unavailableReplicas`
//+kubebuilder:printcolumn:name="ETCD Size",type=string,JSONPath=`.status.etcdVolumeSize`
//+kubebuilder:printcolumn:name="Max ETCD Space Usage",type=string,JSONPath=`.status.etcdVolumeUsage`
//+kubebuilder:printcolumn:name="Paused",type=string,JSONPath=`.metadata.annotations['cluster\.x-k8s\.io/paused']`
//+kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
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

	HostedControlPlaneInlineSpec `json:",inline"`
}

type HostedControlPlaneInlineSpec struct {
	//+kubebuilder:validation:Optional
	Deployment HostedControlPlaneDeployment `json:"deployment,omitempty"`
	//+kubebuilder:validation:Required
	Gateway GatewayReference `json:"gateway"`

	//+kubebuilder:validation:Optional
	KonnectivityClient HostedControlPlaneContainer `json:"konnectivityClient,omitempty"`
	//+kubebuilder:validation:Optional
	KubeProxy KubeProxyComponent `json:"kubeProxy,omitempty"`
	//+kubebuilder:default={}
	ETCD *ETCDComponent `json:"etcd,omitempty"`
}

type GatewayReference struct {
	//+kubebuilder:validation:Required
	Namespace string `json:"namespace"`
	//+kubebuilder:validation:Required
	Name string `json:"name"`
}

type HostedControlPlaneDeployment struct {
	//+kubebuilder:validation:Optional
	APIServer APIServerPod `json:"apiServer,omitempty"`
	//+kubebuilder:validation:Optional
	ControllerManager ScalableHostedControlPlanePod `json:"controllerManager,omitempty"`
	//+kubebuilder:validation:Optional
	Scheduler ScalableHostedControlPlanePod `json:"scheduler,omitempty"`
}

type HostedControlPlanePod struct {
	HostedControlPlaneContainer `json:",inline"`
	//+kubebuilder:validation:Optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
}

type HostedControlPlaneContainer struct {
	//+kubebuilder:validation:Optional
	Args map[string]string `json:"args,omitempty"`
	//+kubebuilder:validation:Optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

type ScalableHostedControlPlanePod struct {
	HostedControlPlanePod `json:",inline"`
	//+kubebuilder:validation:Optional
	//+kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`
}

type KubeProxyComponent struct {
	HostedControlPlanePod `json:",inline"`
	//+kubebuilder:validation:Optional
	Disabled bool `json:"enabled,omitempty"`
}

type ETCDComponent struct {
	//+kubebuilder:validation:Optional
	VolumeSize resource.Quantity `json:"volumeSize,omitempty"`
	// AutoGrow will increase the volume size automatically when it is near full.
	//+kubebuilder:default=true
	AutoGrow bool `json:"autoGrow,omitempty"`
	//+kubebuilder:validation:Optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
}

type APIServerPod struct {
	HostedControlPlanePod `json:",inline"`
	//+kubebuilder:validation:Optional
	Mounts map[string]HostedControlPlaneMount `json:"mounts,omitempty"`
	//+kubebuilder:validation:Optional
	Konnectivity HostedControlPlaneContainer `json:"konnectivity,omitempty"`
}

//+kubebuilder:validation:MinProperties=2
//+kubebuilder:validation:MaxProperties=2

type HostedControlPlaneMount struct {
	//+kubebuilder:validation:Required
	Path string `json:"path"`
	//+kubebuilder:validation:Optional
	ConfigMap *corev1.ConfigMapVolumeSource `json:"configMap,omitempty"`
	//+kubebuilder:validation:Optional
	Secret *corev1.SecretVolumeSource `json:"secret,omitempty"`
}

type HostedControlPlaneStatus struct {
	//+kubebuilder:validation:Optional
	Conditions capiv1.Conditions `json:"conditions,omitempty"`
	//+kubebuilder:validation:Optional
	LegacyIP string `json:"legacyIP,omitempty"`
	//+kubebuilder:validation:Optional
	ETCDVolumeSize resource.Quantity `json:"etcdVolumeSize,omitempty"`
	//+kubebuilder:validation:Optional
	ETCDVolumeUsage resource.Quantity `json:"etcdVolumeUsage,omitempty"`

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
	//+kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`

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
