package names

import (
	"testing"

	. "github.com/onsi/gomega"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func TestGetControlPlaneLabels(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		component   string
		expected    map[string]string
	}{
		{
			name:        "basic cluster with component",
			clusterName: "test-cluster",
			component:   "api-server",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
				"app.kubernetes.io/component":   "api-server",
			},
		},
		{
			name:        "cluster without component",
			clusterName: "my-cluster",
			component:   "",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "my-cluster",
			},
		},
		{
			name:        "cluster with etcd component",
			clusterName: "prod-cluster",
			component:   "etcd",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "prod-cluster",
				"app.kubernetes.io/component":   "etcd",
			},
		},
		{
			name:        "cluster with controller-manager component",
			clusterName: "dev",
			component:   "controller-manager",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "dev",
				"app.kubernetes.io/component":   "controller-manager",
			},
		},
		{
			name:        "cluster with scheduler component",
			clusterName: "staging-cluster",
			component:   "scheduler",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "staging-cluster",
				"app.kubernetes.io/component":   "scheduler",
			},
		},
		{
			name:        "empty cluster name with component",
			clusterName: "",
			component:   "test-component",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "",
				"app.kubernetes.io/component":   "test-component",
			},
		},
		{
			name:        "cluster name with special characters",
			clusterName: "cluster-123_test.example",
			component:   "custom-component",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "cluster-123_test.example",
				"app.kubernetes.io/component":   "custom-component",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetControlPlaneLabels(cluster, tt.component)

			g.Expect(result).To(Equal(tt.expected))

			// Verify the cluster name label is always present
			clusterLabel, exists := result["cluster.x-k8s.io/cluster-name"]
			g.Expect(exists).To(BeTrue())
			g.Expect(clusterLabel).
				To(Equal(tt.clusterName))

			// Verify component label is only present when component is not empty
			if tt.component == "" {
				_, exists := result["app.kubernetes.io/component"]
				g.Expect(exists).
					To(BeFalse())
			} else {
				componentLabel, exists := result["app.kubernetes.io/component"]
				g.Expect(exists).To(BeTrue())
				g.Expect(componentLabel).To(Equal(tt.component))
			}
		})
	}
}

func TestGetControlPlaneSelector(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		component   string
		expected    map[string]string
	}{
		{
			name:        "selector with component",
			clusterName: "test-cluster",
			component:   "api-server",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
				"app.kubernetes.io/component":   "api-server",
			},
		},
		{
			name:        "selector without component",
			clusterName: "my-cluster",
			component:   "",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "my-cluster",
			},
		},
		{
			name:        "selector with etcd component",
			clusterName: "prod",
			component:   "etcd",
			expected: map[string]string{
				"cluster.x-k8s.io/cluster-name": "prod",
				"app.kubernetes.io/component":   "etcd",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetControlPlaneSelector(cluster, tt.component)

			// Verify the selector is not nil
			g.Expect(result).NotTo(BeNil())

			// Verify MatchLabels is set correctly
			g.Expect(result.MatchLabels).NotTo(BeNil())

			g.Expect(result.MatchLabels).
				To(Equal(tt.expected))
		})
	}
}

func TestGetControlPlaneLabelsSelectorConsistency(t *testing.T) {
	// Test that GetControlPlaneLabels and GetControlPlaneSelector produce consistent results
	testCases := []struct {
		clusterName string
		component   string
	}{
		{"test-cluster", "api-server"},
		{"my-cluster", ""},
		{"prod", "etcd"},
		{"", "scheduler"},
	}

	for _, tc := range testCases {
		t.Run("consistency_"+tc.clusterName+"_"+tc.component, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &capiv2.Cluster{}
			cluster.Name = tc.clusterName

			labels := GetControlPlaneLabels(cluster, tc.component)
			selector := GetControlPlaneSelector(cluster, tc.component)

			g.Expect(selector).NotTo(BeNil())
			g.Expect(selector.MatchLabels).NotTo(BeNil())

			g.Expect(labels).To(
				Equal(selector.MatchLabels),
				"GetControlPlaneLabels() and GetControlPlaneSelector() are inconsistent. Labels: %v, Selector MatchLabels: %v",
				labels, selector.MatchLabels,
			)
		})
	}
}
