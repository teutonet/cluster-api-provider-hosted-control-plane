package networkpolicy

import (
	"testing"

	cilium "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
)

func TestLabelNetworkPolicyTarget_Kubernetes(t *testing.T) {
	podLabels := map[string]string{"app": "test"}
	namespaceLabels := map[string]string{corev1.LabelMetadataName: "test-ns"}

	tests := []struct {
		name            string
		target          LabelNetworkPolicyTarget
		expectedPods    map[string]string
		expectedNsLabel map[string]string
	}{
		{
			name:            "pod and namespace labels",
			target:          NewLabelNetworkPolicyTarget(podLabels, namespaceLabels),
			expectedPods:    podLabels,
			expectedNsLabel: namespaceLabels,
		},
		{
			name:            "only namespace labels select no pod selector",
			target:          NewNamespaceLabelNetworkPolicyTarget(namespaceLabels),
			expectedNsLabel: namespaceLabels,
		},
		{
			name:         "only pod labels select the pods of the own namespace",
			target:       NewLabelNetworkPolicyTarget(podLabels, nil),
			expectedPods: podLabels,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)

			peer := tt.target.ApplyToKubernetesIngressNetworkPolicy(networkingv1ac.NetworkPolicyPeer())

			if tt.expectedPods == nil {
				g.Expect(peer.PodSelector).To(BeNil())
			} else {
				g.Expect(peer.PodSelector.MatchLabels).To(Equal(tt.expectedPods))
			}
			if tt.expectedNsLabel == nil {
				g.Expect(peer.NamespaceSelector).To(BeNil())
			} else {
				g.Expect(peer.NamespaceSelector.MatchLabels).To(Equal(tt.expectedNsLabel))
			}
		})
	}
}

func TestLabelNetworkPolicyTarget_Cilium(t *testing.T) {
	t.Run("pod and namespace labels have to match together", func(t *testing.T) {
		g, _, _ := G(t)

		peer := NewLabelNetworkPolicyTarget(
			map[string]string{"app": "test"},
			map[string]string{corev1.LabelMetadataName: "test-ns"},
		).ApplyToCiliumIngressNetworkPolicy(&CiliumPolicyPeer{})

		g.Expect(peer.Endpoints).To(HaveLen(1))
		g.Expect(peer.Endpoints[0].MatchLabels).To(And(
			HaveLen(2),
			HaveKeyWithValue("app", "test"),
			HaveKeyWithValue(cilium.PodNamespaceMetaLabelsPrefix+corev1.LabelMetadataName, "test-ns"),
		))
	})
	t.Run("no labels select nothing extra", func(t *testing.T) {
		g, _, _ := G(t)

		peer := NewLabelNetworkPolicyTarget(nil, nil).ApplyToCiliumIngressNetworkPolicy(&CiliumPolicyPeer{})

		g.Expect(peer.Endpoints).To(BeEmpty())
	})
}
