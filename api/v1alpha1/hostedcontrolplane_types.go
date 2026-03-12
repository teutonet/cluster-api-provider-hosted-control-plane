/*
Copyright 2022 teuto.net Netzdienste GmbH.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/paused"
)

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=hostedcontrolplanes,scope=Namespaced,categories=cluster-api,shortName=hcp
//+kubebuilder:subresource:status
//+kubebuilder:storageversion
//+kubebuilder:printcolumn:name="Initialized",type=boolean,JSONPath=`.status.initialization.controlPlaneInitialized`
//+kubebuilder:printcolumn:name="API Server Available",type=string,JSONPath=`.status.conditions[?(@.type == "Ready")].status`
//+kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
//+kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
//+kubebuilder:printcolumn:name="Updated",type=integer,JSONPath=`.status.upToDateReplicas`
//+kubebuilder:printcolumn:name="ETCD Size",type=string,JSONPath=`.status.etcdVolumeSize`
//+kubebuilder:printcolumn:name="Max ETCD Space Usage",type=string,JSONPath=`.status.etcdVolumeUsage`
//+kubebuilder:printcolumn:name="Paused",type=string,JSONPath=`.metadata.annotations['cluster\.x-k8s\.io/paused']`
//+kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
//+kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
//+kubebuilder:metadata:labels={"cluster.x-k8s.io/provider=control-plane-hosted-control-plane","cluster.x-k8s.io/v1beta2=v1alpha1"}

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

// KubeconfigEndpointType specifies which API server endpoint to use.
// +kubebuilder:validation:Enum=internal;external
type KubeconfigEndpointType string

const (
	KubeconfigEndpointTypeInternal KubeconfigEndpointType = "internal"
	KubeconfigEndpointTypeExternal KubeconfigEndpointType = "external"
)

type HostedControlPlaneInlineSpec struct {
	//+kubebuilder:validation:Optional
	Deployment HostedControlPlaneDeployment `json:"deployment,omitempty"`
	//+kubebuilder:validation:Required
	Gateway GatewayReference `json:"gateway"`

	//+kubebuilder:validation:Optional
	KonnectivityClient ScalablePod `json:"konnectivityClient,omitempty"`
	//+kubebuilder:validation:Optional
	KubeProxy KubeProxyComponent `json:"kubeProxy,omitempty"`
	//+kubebuilder:validation:Optional
	CoreDNS ScalablePod `json:"coredns,omitempty"`
	//+kubebuilder:validation:Optional
	ETCD ETCDComponent `json:"etcd,omitempty"`
	// CustomKubeconfigs specifies additional kubeconfigs to generate.
	// The key is the username, the value specifies the endpoint: "internal" or "external".
	// Users are responsible for creating the appropriate RBAC for these users.
	// The resulting kubeconfigs will be stored in a secret named <hosted-control-plane-name>-<username>-kubeconfig.
	//+kubebuilder:validation:Optional
	CustomKubeconfigs map[string]KubeconfigEndpointType `json:"customKubeconfigs,omitempty"`
	// OIDCProviders configures structured JWT/OIDC authentication.
	// The key is the issuer URL. If non-empty, an AuthenticationConfiguration
	// is generated and passed to the API server via --authentication-config.
	//+kubebuilder:validation:Optional
	OIDCProviders map[string]OIDCProvider `json:"oidcProviders,omitempty"`
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
	ControllerManager ScalablePod `json:"controllerManager,omitempty"`
	//+kubebuilder:validation:Optional
	Scheduler ScalablePod `json:"scheduler,omitempty"`
}

type Pod struct {
	Container `json:",inline"`
	//+kubebuilder:validation:Optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
}

type ImageSpec struct {
	//+kubebuilder:validation:Optional
	Registry *string `json:"registry,omitempty"`
	//+kubebuilder:validation:Optional
	Repository *string `json:"repository,omitempty"`
	//+kubebuilder:validation:Optional
	Tag *string `json:"tag,omitempty"`
}

type Container struct {
	//+kubebuilder:validation:Optional
	Image *ImageSpec `json:"image,omitempty"`
	//+kubebuilder:validation:Optional
	ImagePullPolicy *corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	//+kubebuilder:validation:Optional
	Args map[string]string `json:"args,omitempty"`
	//+kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

type ScalablePod struct {
	Pod `json:",inline"`
	//+kubebuilder:validation:Optional
	Replicas *int32 `json:"replicas,omitempty"`
}

type KubeProxyComponent struct {
	Pod `json:",inline"`
	//+kubebuilder:validation:Optional
	Disabled *bool `json:"disabled,omitempty"`
}

type ETCDComponent struct {
	Container `json:",inline"`
	//+kubebuilder:validation:Optional
	VolumeSize *resource.Quantity `json:"volumeSize,omitempty"`
	// AutoGrow will increase the volume size automatically when it is near full.
	//+kubebuilder:validation:Optional
	AutoGrow *bool `json:"autoGrow,omitempty"`
	//+kubebuilder:validation:Optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
	//+kubebuilder:validation:Optional
	Backup *ETCDBackup `json:"backup,omitempty"`
}

type ETCDBackup struct {
	//+kubebuilder:validation:Required
	Schedule string `json:"schedule"`
	//+kubebuilder:validation:Required
	Bucket string `json:"bucket"`
	//+kubebuilder:validation:Required
	Secret ETCDBackupSecret `json:"secret"`
	//+kubebuilder:validation:Optional
	Region string `json:"region,omitempty"`
}

type ETCDBackupSecret struct {
	//+kubebuilder:validation:Required
	Name string `json:"name"`
	//+kubebuilder:validation:Optional
	Namespace *string `json:"namespace"`
	//+kubebuilder:validation:Optional
	AccessKeyIDKey *string `json:"accessKeyIDKey,omitempty"`
	//+kubebuilder:validation:Optional
	SecretAccessKeyKey *string `json:"secretAccessKeyKey,omitempty"`
}

type APIServerPod struct {
	Pod `json:",inline"`
	//+kubebuilder:validation:Optional
	Mounts map[string]Mount `json:"mounts,omitempty"`
	//+kubebuilder:validation:Optional
	Konnectivity Container `json:"konnectivity,omitempty"`
	//+kubebuilder:validation:Optional
	Audit *Audit `json:"audit,omitempty"`
}

type Audit struct {
	//+kubebuilder:validation:Required
	Policy auditv1.Policy `json:"policy"`
	//+kubebuilder:validation:Optional
	//+kubebuilder:validation:Enum=batch;blocking;blocking-strict
	Mode *string `json:"mode,omitempty"`
	//+kubebuilder:validation:Optional
	Webhook *AuditWebhook `json:"webhook,omitempty"`
}

type AuditWebhook struct {
	Container `json:",inline"`
	//+kubebuilder:validation:Required
	//+kubebuilder:validation:MinItems=1
	Targets []AuditWebhookTarget `json:"targets"`
}

type AuditWebhookTarget struct {
	//+kubebuilder:validation:Required
	Server string `json:"server"`
	//+kubebuilder:validation:Optional
	Authentication *AuditWebhookAuthentication `json:"authentication,omitempty"`
}

type AuditWebhookAuthentication struct {
	//+kubebuilder:validation:Required
	SecretName string `json:"secretName"`
	//+kubebuilder:validation:Optional
	// SecretNamespace. If not set, defaults to the namespace of the HostedControlPlane.
	SecretNamespace *string `json:"secretNamespace,omitempty"`
	//+kubebuilder:validation:Optional
	TokenKey *string `json:"tokenKey,omitempty"`
}

//+kubebuilder:validation:MinProperties=2
//+kubebuilder:validation:MaxProperties=2

type Mount struct {
	//+kubebuilder:validation:Required
	Path string `json:"path"`
	//+kubebuilder:validation:Optional
	ConfigMap *corev1.ConfigMapVolumeSource `json:"configMap,omitempty"`
	//+kubebuilder:validation:Optional
	Secret *corev1.SecretVolumeSource `json:"secret,omitempty"`
}

// OIDCProvider configures a single JWT/OIDC authenticator for structured authentication.
type OIDCProvider struct {
	// CertificateAuthority is a PEM-encoded CA bundle used to verify the OIDC
	// provider's TLS connection. If empty, the system verifier is used.
	//+kubebuilder:validation:Optional
	CertificateAuthority string `json:"certificateAuthority,omitempty"`

	// Audiences is the set of acceptable audiences the JWT must be issued to.
	// At least one entry must match the "aud" claim.
	//+kubebuilder:validation:Required
	//+kubebuilder:validation:MinItems=1
	Audiences []string `json:"audiences"`

	// ClaimValidationRules are CEL expressions evaluated against token claims.
	// All rules must return true for the token to be accepted.
	//+kubebuilder:validation:Optional
	ClaimValidationRules []OIDCClaimValidationRule `json:"claimValidationRules,omitempty"`

	// ClaimMappings maps token claims to Kubernetes user attributes.
	//+kubebuilder:validation:Required
	ClaimMappings OIDCClaimMappings `json:"claimMappings"`
}

// OIDCClaimValidationRule is a CEL expression that must evaluate to true for a token to be accepted.
type OIDCClaimValidationRule struct {
	// Expression is a CEL expression that must evaluate to true.
	//+kubebuilder:validation:Required
	Expression string `json:"expression"`
	// Message is the error message returned when Expression evaluates to false.
	//+kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// OIDCClaimMappings maps token claims to Kubernetes user attributes.
type OIDCClaimMappings struct {
	// Username is a CEL expression producing the Kubernetes username.
	//+kubebuilder:validation:Required
	Username string `json:"username"`
	// Groups is a CEL expression producing the Kubernetes groups (string or []string).
	//+kubebuilder:validation:Optional
	Groups string `json:"groups,omitempty"`
}

type HostedControlPlaneStatus struct {
	//+kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	//+kubebuilder:validation:Optional
	LegacyIP string `json:"legacyIP,omitempty"`
	//+kubebuilder:validation:Optional
	ETCDVolumeSize resource.Quantity `json:"etcdVolumeSize,omitempty"`
	//+kubebuilder:validation:Optional
	ETCDVolumeUsage resource.Quantity `json:"etcdVolumeUsage,omitempty"`
	//+kubebuilder:validation:Optional
	ETCDLastBackupTime metav1.Time `json:"etcdLastBackupTime,omitempty"`
	//+kubebuilder:validation:Optional
	ETCDNextBackupTime metav1.Time `json:"etcdNextBackupTime,omitempty"`

	// Required fields by CAPI
	// https://cluster-api.sigs.k8s.io/developer/providers/contracts/control-plane#controlplane-replicas

	//+kubebuilder:validation:Optional
	Selector string `json:"selector,omitempty"`
	//+kubebuilder:validation:Optional
	Replicas int32 `json:"replicas,omitempty"`
	//+kubebuilder:validation:Optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	//+kubebuilder:validation:Optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`
	//+kubebuilder:validation:Optional
	UpToDateReplicas int32 `json:"upToDateReplicas,omitempty"`

	// CAPI Contract fields
	// https://cluster-api.sigs.k8s.io/developer/providers/contracts/control-plane

	//+kubebuilder:validation:Optional
	Initialization HostedControlPlaneInitializationStatus `json:"initialization,omitempty"`
	//+kubebuilder:validation:Optional
	Ready bool `json:"ready"`
	//+kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`
	//+kubebuilder:validation:Optional
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`
	//+kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

type HostedControlPlaneInitializationStatus struct {
	//+kubebuilder:validation:Optional
	ControlPlaneInitialized *bool `json:"controlPlaneInitialized,omitempty"`
}

//+kubebuilder:object:root=true

type HostedControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedControlPlane `json:"items"`
}

func (hcp *HostedControlPlane) GetConditions() []metav1.Condition {
	return hcp.Status.Conditions
}

func (hcp *HostedControlPlane) SetConditions(conditions []metav1.Condition) {
	hcp.Status.Conditions = conditions
}
