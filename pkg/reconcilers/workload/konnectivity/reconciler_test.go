package konnectivity

import (
	"net/netip"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestBuildKonnectivityClientArgs_ReconnectTuning(t *testing.T) {
	g, ctx, _ := G(t)
	reconciler := &konnectivityReconciler{
		konnectivityServicePort:             443,
		konnectivityServiceAccountTokenName: "konnectivity-agent-token",
	}
	hostedControlPlane := &v1alpha1.HostedControlPlane{}
	cluster := &capiv2.Cluster{}
	cluster.Spec.ControlPlaneEndpoint.Host = "cluster.example.com"

	args := reconciler.buildKonnectivityClientArgs(
		ctx,
		hostedControlPlane,
		cluster,
		corev1ac.VolumeMount().WithMountPath("/var/run/secrets/tokens"),
		corev1ac.ContainerPort().WithContainerPort(8134).WithProtocol(corev1.ProtocolTCP),
	)

	g.Expect(args).To(ContainElements(
		"--count-server-leases=true",
		"--server-count-source=max",
		"--sync-interval-cap=2s",
		"--sync-interval=250ms",
	))
}

func TestNewKonnectivityContainer_FallsBackToLogsOnError(t *testing.T) {
	g, ctx, _ := G(t)
	reconciler := &konnectivityReconciler{
		konnectivityServicePort:             443,
		konnectivityServiceAccountTokenName: "konnectivity-agent-token",
	}
	hostedControlPlane := &v1alpha1.HostedControlPlane{Spec: v1alpha1.HostedControlPlaneSpec{Version: "v1.34.0"}}
	cluster := &capiv2.Cluster{}
	cluster.Spec.ControlPlaneEndpoint.Host = "cluster.example.com"

	container, err := reconciler.newKonnectivityContainer(
		ctx,
		hostedControlPlane,
		cluster,
		corev1ac.VolumeMount().WithMountPath("/var/run/secrets/tokens"),
		corev1ac.ContainerPort().WithName("health").WithContainerPort(8134).WithProtocol(corev1.ProtocolTCP),
	)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(container.TerminationMessagePolicy).To(HaveValue(Equal(corev1.TerminationMessageFallbackToLogsOnError)))
}

func TestKonnectivityPodOptions_ResolvesViaNodeResolver(t *testing.T) {
	g, _, _ := G(t)

	g.Expect(konnectivityPodOptions("konnectivity-agent").DNSPolicy).To(Equal(corev1.DNSDefault))
}

func TestKonnectivityEgressTargets_DoNotDependOnClusterDNS(t *testing.T) {
	g, _, _ := G(t)

	targets := konnectivityEgressTargets(443, []netip.Addr{netip.MustParseAddr("192.0.2.10")})

	g.Expect(targets).To(HaveKeyWithValue(int32(53), ConsistOf(networkpolicy.NewWorldNetworkPolicyTarget())))
	g.Expect(targets).To(HaveKeyWithValue(int32(443), ConsistOf(networkpolicy.NewIPNetworkPolicyTarget("192.0.2.10"))))
	g.Expect(targets).To(HaveKeyWithValue(int32(0), ConsistOf(networkpolicy.NewClusterNetworkPolicyTarget())))
	for _, port := range targets {
		g.Expect(port).NotTo(ContainElement(BeAssignableToTypeOf(networkpolicy.DNSNetworkPolicyTarget{})))
	}
}

func TestGatewayIPs(t *testing.T) {
	gateway := func(addresses ...gwv1.GatewayStatusAddress) *gwv1.Gateway {
		return &gwv1.Gateway{Status: gwv1.GatewayStatus{Addresses: addresses}}
	}
	address := func(addressType gwv1.AddressType, value string) gwv1.GatewayStatusAddress {
		return gwv1.GatewayStatusAddress{Type: &addressType, Value: value}
	}

	tests := []struct {
		name     string
		gateway  *gwv1.Gateway
		expected []netip.Addr
		wantErr  bool
	}{
		{
			name:     "ip address",
			gateway:  gateway(address(gwv1.IPAddressType, "192.0.2.10")),
			expected: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:     "untyped address defaults to ip",
			gateway:  gateway(gwv1.GatewayStatusAddress{Value: "192.0.2.10"}),
			expected: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:     "nip.io hostname is trimmed to its ip",
			gateway:  gateway(address(gwv1.HostnameAddressType, "192.0.2.10.nip.io")),
			expected: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name: "ip address wins over nip.io hostname",
			gateway: gateway(
				address(gwv1.HostnameAddressType, "192.0.2.11.nip.io"),
				address(gwv1.IPAddressType, "192.0.2.10"),
			),
			expected: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:    "other hostname is no ip",
			gateway: gateway(address(gwv1.HostnameAddressType, "gateway.example.com")),
			wantErr: true,
		},
		{
			name:    "nip.io hostname without ip",
			gateway: gateway(address(gwv1.HostnameAddressType, "example.nip.io")),
			wantErr: true,
		},
		{
			name:    "no addresses",
			gateway: gateway(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)

			ips, err := gatewayIPs(tt.gateway)

			if tt.wantErr {
				g.Expect(err).To(MatchError(operatorutil.ErrNoIPAddress))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ips).To(Equal(tt.expected))
		})
	}
}
