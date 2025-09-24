package networkpolicy

import (
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	corev1 "k8s.io/api/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type NetworkPolicyTarget interface {
	ApplyToVanillaNetworkPolicy(
		networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
	) *networkingv1ac.NetworkPolicyPeerApplyConfiguration
}

type DNSNetworkPolicyTarget struct {
	Hostname string
}

func NewDNSNetworkPolicyTarget(hostname string) DNSNetworkPolicyTarget {
	return DNSNetworkPolicyTarget{
		Hostname: hostname,
	}
}

var _ NetworkPolicyTarget = DNSNetworkPolicyTarget{}

func (d DNSNetworkPolicyTarget) ApplyToVanillaNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	// DNS filtering is not supported in NetworkPolicy, so we allow traffic from/to anywhere.
	return NewCIDRNetworkPolicyTarget("0.0.0.0/0").
		ApplyToVanillaNetworkPolicy(networkPolicyPeerApplyConfiguration)
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

var _ NetworkPolicyTarget = CIDRNetworkPolicyTarget{}

func (c CIDRNetworkPolicyTarget) ApplyToVanillaNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return networkPolicyPeerApplyConfiguration.
		WithIPBlock(
			networkingv1ac.IPBlock().
				WithCIDR(c.CIDR),
		)
}

type APIServerNetworkPolicyTarget struct {
	HostedControlPlane *v1alpha1.HostedControlPlane
}

func NewAPIServerNetworkPolicyTarget(hostedControlPlane *v1alpha1.HostedControlPlane) APIServerNetworkPolicyTarget {
	return APIServerNetworkPolicyTarget{
		HostedControlPlane: hostedControlPlane,
	}
}

var _ NetworkPolicyTarget = APIServerNetworkPolicyTarget{}

func (a APIServerNetworkPolicyTarget) ApplyToVanillaNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return NewIPNetworkPolicyTarget(a.HostedControlPlane.Status.LegacyIP).
		ApplyToVanillaNetworkPolicy(networkPolicyPeerApplyConfiguration)
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

var _ NetworkPolicyTarget = ComponentNetworkPolicyTarget{}

func (c ComponentNetworkPolicyTarget) ApplyToVanillaNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return NewLabelNetworkPolicyTarget(
		names.GetControlPlaneLabels(c.Cluster, c.Component),
		map[string]string{
			corev1.LabelMetadataName: c.Cluster.Namespace,
		},
	).ApplyToVanillaNetworkPolicy(networkPolicyPeerApplyConfiguration)
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

func NewPodLabelNetworkPolicyTarget(podLabels map[string]string) LabelNetworkPolicyTarget {
	return LabelNetworkPolicyTarget{
		PodLabels: podLabels,
	}
}

func NewNamespaceLabelNetworkPolicyTarget(namespaceLabels map[string]string) LabelNetworkPolicyTarget {
	return LabelNetworkPolicyTarget{
		NamespaceLabels: namespaceLabels,
	}
}

var _ NetworkPolicyTarget = LabelNetworkPolicyTarget{}

func (l LabelNetworkPolicyTarget) ApplyToVanillaNetworkPolicy(
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

type HCPControllerNetworkPolicyTarget struct {
	ControllerNamespace string
}

func NewHCPControllerNetworkPolicyTarget(controllerNamespace string) HCPControllerNetworkPolicyTarget {
	return HCPControllerNetworkPolicyTarget{
		ControllerNamespace: controllerNamespace,
	}
}

var _ NetworkPolicyTarget = HCPControllerNetworkPolicyTarget{}

func (h HCPControllerNetworkPolicyTarget) ApplyToVanillaNetworkPolicy(
	networkPolicyPeerApplyConfiguration *networkingv1ac.NetworkPolicyPeerApplyConfiguration,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	return NewNamespaceLabelNetworkPolicyTarget(
		map[string]string{
			corev1.LabelMetadataName: h.ControllerNamespace,
		},
	).ApplyToVanillaNetworkPolicy(networkPolicyPeerApplyConfiguration)
}
