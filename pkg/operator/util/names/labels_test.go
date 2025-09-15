package names

import (
	"reflect"
	"testing"

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
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetControlPlaneLabels(cluster, tt.component)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("GetControlPlaneLabels() = %v, want %v", result, tt.expected)
			}

			// Verify the cluster name label is always present
			if clusterLabel, exists := result["cluster.x-k8s.io/cluster-name"]; !exists {
				t.Errorf("GetControlPlaneLabels() missing required cluster-name label")
			} else if clusterLabel != tt.clusterName {
				t.Errorf("GetControlPlaneLabels() cluster-name label = %v, want %v", clusterLabel, tt.clusterName)
			}

			// Verify component label is only present when component is not empty
			if tt.component == "" {
				if _, exists := result["app.kubernetes.io/component"]; exists {
					t.Errorf("GetControlPlaneLabels() should not include component label when component is empty")
				}
			} else {
				if componentLabel, exists := result["app.kubernetes.io/component"]; !exists {
					t.Errorf("GetControlPlaneLabels() missing component label when component is provided")
				} else if componentLabel != tt.component {
					t.Errorf("GetControlPlaneLabels() component label = %v, want %v", componentLabel, tt.component)
				}
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
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetControlPlaneSelector(cluster, tt.component)

			// Verify the selector is not nil
			if result == nil {
				t.Fatal("GetControlPlaneSelector() returned nil")
			}

			// Verify MatchLabels is set correctly
			if result.MatchLabels == nil {
				t.Fatal("GetControlPlaneSelector() MatchLabels is nil")
			}

			if !reflect.DeepEqual(result.MatchLabels, tt.expected) {
				t.Errorf("GetControlPlaneSelector() MatchLabels = %v, want %v", result.MatchLabels, tt.expected)
			}
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
			cluster := &capiv2.Cluster{}
			cluster.Name = tc.clusterName

			labels := GetControlPlaneLabels(cluster, tc.component)
			selector := GetControlPlaneSelector(cluster, tc.component)

			if selector == nil || selector.MatchLabels == nil {
				t.Fatal("GetControlPlaneSelector() returned invalid selector")
			}

			if !reflect.DeepEqual(labels, selector.MatchLabels) {
				t.Errorf("GetControlPlaneLabels() and GetControlPlaneSelector() are inconsistent")
				t.Errorf("Labels: %v", labels)
				t.Errorf("Selector MatchLabels: %v", selector.MatchLabels)
			}
		})
	}
}
