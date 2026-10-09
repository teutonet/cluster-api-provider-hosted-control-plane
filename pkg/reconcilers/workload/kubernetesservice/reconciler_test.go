package kubernetesservice

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestReconciler(kubeClient *fake.Clientset) KubernetesServiceReconciler {
	return NewKubernetesServiceReconciler(&alias.WorkloadClusterClient{Interface: kubeClient})
}

func hostedControlPlaneWithIP(ip string) *v1alpha1.HostedControlPlane {
	return &v1alpha1.HostedControlPlane{Status: v1alpha1.HostedControlPlaneStatus{LegacyIP: ip}}
}

func TestReconcileKubernetesEndpointsWaitsForLoadBalancerIP(t *testing.T) {
	g, ctx, _ := G(t)

	kubeClient := fake.NewClientset()
	reconciler := newTestReconciler(kubeClient)

	notReadyReason, err := reconciler.ReconcileKubernetesEndpoints(ctx, hostedControlPlaneWithIP(""))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReadyReason).To(Equal("LoadBalancerIPNotReady"))

	slices, err := kubeClient.DiscoveryV1().EndpointSlices(metav1.NamespaceDefault).List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(slices.Items).To(BeEmpty())
}

func TestReconcileKubernetesEndpointsCreatesSliceAndEndpoints(t *testing.T) {
	g, ctx, _ := G(t)

	kubeClient := fake.NewClientset()
	reconciler := newTestReconciler(kubeClient)

	notReadyReason, err := reconciler.ReconcileKubernetesEndpoints(ctx, hostedControlPlaneWithIP("192.0.2.10"))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReadyReason).To(BeEmpty())

	slice, err := kubeClient.DiscoveryV1().EndpointSlices(metav1.NamespaceDefault).
		Get(ctx, "kubernetes", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(slice.Labels).To(HaveKeyWithValue(discoveryv1.LabelServiceName, "kubernetes"))
	g.Expect(slice.AddressType).To(Equal(discoveryv1.AddressTypeIPv4))
	g.Expect(slice.Endpoints).To(ConsistOf(discoveryv1.Endpoint{
		Addresses:  []string{"192.0.2.10"},
		Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
	}))
	g.Expect(slice.Ports).To(ConsistOf(discoveryv1.EndpointPort{
		Name:     new("https"),
		Protocol: new(corev1.ProtocolTCP),
		Port:     new(int32(6443)),
	}))

	endpoints, err := kubeClient.CoreV1().Endpoints(metav1.NamespaceDefault).
		Get(ctx, "kubernetes", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(endpoints.Labels).To(HaveKeyWithValue(discoveryv1.LabelSkipMirror, "true"))
	//nolint:staticcheck // SA1019: Endpoints are deprecated, but still read by older clients
	g.Expect(endpoints.Subsets).To(ConsistOf(corev1.EndpointSubset{
		Addresses: []corev1.EndpointAddress{{IP: "192.0.2.10"}},
		Ports: []corev1.EndpointPort{{
			Name:     "https",
			Protocol: corev1.ProtocolTCP,
			Port:     6443,
		}},
	}))
}

// A gracefully shutting down kube-apiserver with the default lease reconciler empties the slice.
// The reconciler must restore it, and follow a changed load balancer IP.
func TestReconcileKubernetesEndpointsRestoresEmptiedSliceAndFollowsIPChange(t *testing.T) {
	g, ctx, _ := G(t)

	kubeClient := fake.NewClientset()
	reconciler := newTestReconciler(kubeClient)

	_, err := reconciler.ReconcileKubernetesEndpoints(ctx, hostedControlPlaneWithIP("192.0.2.10"))
	g.Expect(err).NotTo(HaveOccurred())

	slices := kubeClient.DiscoveryV1().EndpointSlices(metav1.NamespaceDefault)
	slice, err := slices.Get(ctx, "kubernetes", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	slice.Endpoints = nil
	_, err = slices.Update(ctx, slice, metav1.UpdateOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	notReadyReason, err := reconciler.ReconcileKubernetesEndpoints(ctx, hostedControlPlaneWithIP("192.0.2.20"))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReadyReason).To(BeEmpty())

	slice, err = slices.Get(ctx, "kubernetes", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(slice.Endpoints).To(HaveLen(1))
	g.Expect(slice.Endpoints[0].Addresses).To(ConsistOf("192.0.2.20"))

	endpoints, err := kubeClient.CoreV1().Endpoints(metav1.NamespaceDefault).
		Get(ctx, "kubernetes", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(endpoints.Subsets).To(HaveLen(1))
	g.Expect(endpoints.Subsets[0].Addresses).To(ConsistOf(corev1.EndpointAddress{IP: "192.0.2.20"}))
}

func TestReconcileKubernetesEndpointsIPv6(t *testing.T) {
	g, ctx, _ := G(t)

	kubeClient := fake.NewClientset()
	reconciler := newTestReconciler(kubeClient)

	_, err := reconciler.ReconcileKubernetesEndpoints(ctx, hostedControlPlaneWithIP("2001:db8::10"))
	g.Expect(err).NotTo(HaveOccurred())

	slice, err := kubeClient.DiscoveryV1().EndpointSlices(metav1.NamespaceDefault).
		Get(ctx, "kubernetes", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(slice.AddressType).To(Equal(discoveryv1.AddressTypeIPv6))
}
