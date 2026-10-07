package konnectivity

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
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
