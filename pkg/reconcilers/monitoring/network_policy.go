package monitoring

import (
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	corev1 "k8s.io/api/core/v1"
)

// KonnectivityAgentComponent is the component label of the konnectivity agent pods in the workload cluster.
const KonnectivityAgentComponent = "konnectivity"

// NewKonnectivityAgentNetworkPolicyTarget matches the konnectivity agent pods in the workload cluster, which open the
// connections of the pod proxy of the apiserver.
func NewKonnectivityAgentNetworkPolicyTarget(namespace string) networkpolicy.LabelNetworkPolicyTarget {
	return networkpolicy.NewLabelNetworkPolicyTarget(
		map[string]string{"app.kubernetes.io/component": KonnectivityAgentComponent},
		map[string]string{corev1.LabelMetadataName: namespace},
	)
}
