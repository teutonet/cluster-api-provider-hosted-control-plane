package networkpolicy

import (
	cilium "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	ciliummetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	"github.com/cilium/cilium/pkg/policy/api"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	corev1 "k8s.io/api/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type CiliumPolicyPeer struct {
	Endpoints  []api.EndpointSelector
	CIDR       []api.CIDR
	FQDNs      []string
	Identities []api.Entity
}

type IngressNetworkPolicyTarget interface {
	ApplyToKubernetesIngressNetworkPolicy(
		networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	) *networkingv1ac.NetworkPolicyPeerApplyConfiguration
	ApplyToCiliumIngressNetworkPolicy(
		apiEndpointSelector *CiliumPolicyPeer,
	) *CiliumPolicyPeer
}

type EgressNetworkPolicyTarget interface {
	ApplyToKubernetesEgressNetworkPolicy(
		networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	) *networkingv1ac.NetworkPolicyPeerApplyConfiguration
	ApplyToCiliumEgressNetworkPolicy(
		apiEndpointSelector *CiliumPolicyPeer,
	) *CiliumPolicyPeer
}

type DNSNetworkPolicyTarget struct {
	Hostname string
}

func NewDNSNetworkPolicyTarget(hostname string) DNSNetworkPolicyTarget {
	return DNSNetworkPolicyTarget{
		Hostname: hostname,
	}
}

var _ EgressNetworkPolicyTarget = DNSNetworkPolicyTarget{}

func (d DNSNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	// DNS filtering is not supported in NetworkPolicy, so we allow traffic to anywhere.
	return NewCIDRNetworkPolicyTarget("0.0.0.0/0").
		ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (d DNSNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	ciliumPolicyPeer.FQDNs = append(ciliumPolicyPeer.FQDNs, d.Hostname)
	return ciliumPolicyPeer
}

type CIDRNetworkPolicyTarget struct {
	CIDR string
}

func NewCIDRNetworkPolicyTarget(cidr string) CIDRNetworkPolicyTarget {
	return CIDRNetworkPolicyTarget{
		CIDR: cidr,
	}
}

func NewIPNetworkPolicyTarget(ip string) CIDRNetworkPolicyTarget {
	return CIDRNetworkPolicyTarget{
		CIDR: ip + "/32",
	}
}

var (
	_ IngressNetworkPolicyTarget = CIDRNetworkPolicyTarget{}
	_ EgressNetworkPolicyTarget  = CIDRNetworkPolicyTarget{}
)

func (c CIDRNetworkPolicyTarget) ApplyToKubernetesIngressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return networkPolicyPeerApplyConfiguration.WithIPBlock(networkingv1ac.IPBlock().
		WithCIDR(c.CIDR),
	)
}

func (c CIDRNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return c.ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (c CIDRNetworkPolicyTarget) ApplyToCiliumIngressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	ciliumPolicyPeer.CIDR = append(ciliumPolicyPeer.CIDR, api.CIDR(c.CIDR))
	return ciliumPolicyPeer
}

func (c CIDRNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	return c.ApplyToCiliumIngressNetworkPolicy(ciliumPolicyPeer)
}

type ClusterNetworkPolicyTarget struct{}

func NewClusterNetworkPolicyTarget() ClusterNetworkPolicyTarget {
	return ClusterNetworkPolicyTarget{}
}

var _ EgressNetworkPolicyTarget = ClusterNetworkPolicyTarget{}

func (c ClusterNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	// Cluster filtering is not supported in NetworkPolicy, so we allow traffic to anywhere.
	return NewCIDRNetworkPolicyTarget("0.0.0.0/0").
		ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (c ClusterNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	ciliumPolicyPeer.Identities = append(ciliumPolicyPeer.Identities, api.EntityCluster)
	return ciliumPolicyPeer
}

type WorldNetworkPolicyTarget struct{}

func NewWorldNetworkPolicyTarget() WorldNetworkPolicyTarget {
	return WorldNetworkPolicyTarget{}
}

var (
	_ IngressNetworkPolicyTarget = WorldNetworkPolicyTarget{}
	_ EgressNetworkPolicyTarget  = WorldNetworkPolicyTarget{}
)

func (w WorldNetworkPolicyTarget) ApplyToKubernetesIngressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	// World filtering is not supported in NetworkPolicy, so we allow traffic to anywhere.
	return NewCIDRNetworkPolicyTarget("0.0.0.0/0").
		ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (w WorldNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return w.ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (w WorldNetworkPolicyTarget) ApplyToCiliumIngressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	ciliumPolicyPeer.Identities = append(ciliumPolicyPeer.Identities, api.EntityWorld)
	return ciliumPolicyPeer
}

func (w WorldNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	return w.ApplyToCiliumIngressNetworkPolicy(ciliumPolicyPeer)
}

type APIServerNetworkPolicyTarget struct {
	HostedControlPlane *v1alpha1.HostedControlPlane
}

func NewAPIServerNetworkPolicyTarget(hostedControlPlane *v1alpha1.HostedControlPlane) APIServerNetworkPolicyTarget {
	return APIServerNetworkPolicyTarget{
		HostedControlPlane: hostedControlPlane,
	}
}

var (
	_ IngressNetworkPolicyTarget = APIServerNetworkPolicyTarget{}
	_ EgressNetworkPolicyTarget  = APIServerNetworkPolicyTarget{}
)

func (a APIServerNetworkPolicyTarget) ApplyToKubernetesIngressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return NewIPNetworkPolicyTarget(a.HostedControlPlane.Status.LegacyIP).
		ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (a APIServerNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return a.ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (a APIServerNetworkPolicyTarget) ApplyToCiliumIngressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	ciliumPolicyPeer.Identities = append(ciliumPolicyPeer.Identities, api.EntityKubeAPIServer)
	return ciliumPolicyPeer
}

func (a APIServerNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	return a.ApplyToCiliumIngressNetworkPolicy(ciliumPolicyPeer)
}

type ComponentNetworkPolicyTarget struct {
	Cluster   *capiv2.Cluster
	Component string
}

func NewComponentNetworkPolicyTarget(cluster *capiv2.Cluster, component string) ComponentNetworkPolicyTarget {
	return ComponentNetworkPolicyTarget{
		Cluster:   cluster,
		Component: component,
	}
}

var (
	_ IngressNetworkPolicyTarget = ComponentNetworkPolicyTarget{}
	_ EgressNetworkPolicyTarget  = ComponentNetworkPolicyTarget{}
)

func (c ComponentNetworkPolicyTarget) ApplyToKubernetesIngressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return NewLabelNetworkPolicyTarget(
		names.GetControlPlaneLabels(c.Cluster, c.Component),
		map[string]string{
			corev1.LabelMetadataName: c.Cluster.Namespace,
		},
	).ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (c ComponentNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return c.ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (c ComponentNetworkPolicyTarget) ApplyToCiliumIngressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	labels := names.GetControlPlaneLabels(c.Cluster, c.Component)
	labels[cilium.PodNamespaceLabel] = c.Cluster.Namespace
	ciliumPolicyPeer.Endpoints = append(ciliumPolicyPeer.Endpoints, api.EndpointSelector{
		LabelSelector: &ciliummetav1.LabelSelector{
			MatchLabels: labels,
		},
	})
	return ciliumPolicyPeer
}

func (c ComponentNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	return c.ApplyToCiliumIngressNetworkPolicy(ciliumPolicyPeer)
}

type LabelNetworkPolicyTarget struct {
	PodLabels       map[string]string
	NamespaceLabels map[string]string
}

func NewLabelNetworkPolicyTarget(podLabels, namespaceLabels map[string]string) LabelNetworkPolicyTarget {
	return LabelNetworkPolicyTarget{
		PodLabels:       podLabels,
		NamespaceLabels: namespaceLabels,
	}
}

func NewNamespaceLabelNetworkPolicyTarget(namespaceLabels map[string]string) LabelNetworkPolicyTarget {
	return LabelNetworkPolicyTarget{
		NamespaceLabels: namespaceLabels,
	}
}

var (
	_ IngressNetworkPolicyTarget = LabelNetworkPolicyTarget{}
	_ EgressNetworkPolicyTarget  = LabelNetworkPolicyTarget{}
)

func (l LabelNetworkPolicyTarget) ApplyToKubernetesIngressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	policyPeerApplyConfiguration := networkPolicyPeerApplyConfiguration
	if l.PodLabels == nil {
		policyPeerApplyConfiguration = policyPeerApplyConfiguration.
			WithPodSelector(metav1ac.LabelSelector().WithMatchLabels(l.PodLabels))
	}
	if l.NamespaceLabels != nil {
		policyPeerApplyConfiguration = policyPeerApplyConfiguration.
			WithNamespaceSelector(metav1ac.LabelSelector().WithMatchLabels(l.NamespaceLabels))
	}
	return policyPeerApplyConfiguration
}

func (l LabelNetworkPolicyTarget) ApplyToKubernetesEgressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return l.ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (l LabelNetworkPolicyTarget) ApplyToCiliumIngressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	if l.PodLabels != nil {
		ciliumPolicyPeer.Endpoints = append(ciliumPolicyPeer.Endpoints, api.EndpointSelector{
			LabelSelector: &ciliummetav1.LabelSelector{
				MatchLabels: l.PodLabels,
			},
		})
	}
	if l.NamespaceLabels != nil {
		ciliumPolicyPeer.Endpoints = append(ciliumPolicyPeer.Endpoints, api.EndpointSelector{
			LabelSelector: &ciliummetav1.LabelSelector{
				MatchLabels: slices.MapEntries(l.NamespaceLabels, func(k string, v string) (string, string) {
					return cilium.PodNamespaceMetaLabelsPrefix + k, v
				}),
			},
		})
	}
	return ciliumPolicyPeer
}

func (l LabelNetworkPolicyTarget) ApplyToCiliumEgressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	return l.ApplyToCiliumIngressNetworkPolicy(ciliumPolicyPeer)
}

type HCPControllerNetworkPolicyTarget struct {
	ControllerNamespace string
}

func NewHCPControllerNetworkPolicyTarget(controllerNamespace string) HCPControllerNetworkPolicyTarget {
	return HCPControllerNetworkPolicyTarget{
		ControllerNamespace: controllerNamespace,
	}
}

var _ IngressNetworkPolicyTarget = HCPControllerNetworkPolicyTarget{}

func (h HCPControllerNetworkPolicyTarget) ApplyToKubernetesIngressNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return NewNamespaceLabelNetworkPolicyTarget(
		map[string]string{
			corev1.LabelMetadataName: h.ControllerNamespace,
		},
	).ApplyToKubernetesIngressNetworkPolicy(networkPolicyPeerApplyConfiguration)
}

func (h HCPControllerNetworkPolicyTarget) ApplyToCiliumIngressNetworkPolicy(
	ciliumPolicyPeer *CiliumPolicyPeer,
) *CiliumPolicyPeer {
	return NewNamespaceLabelNetworkPolicyTarget(
		map[string]string{
			corev1.LabelMetadataName: h.ControllerNamespace,
		},
	).ApplyToCiliumIngressNetworkPolicy(ciliumPolicyPeer)
}
