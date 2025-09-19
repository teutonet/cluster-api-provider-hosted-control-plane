package names

import (
	"testing"

	. "github.com/onsi/gomega"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func TestGetRootIssuerName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		expected    string
	}{
		{
			name:        "simple cluster name",
			clusterName: "test-cluster",
			expected:    "test-cluster-root",
		},
		{
			name:        "cluster with dashes",
			clusterName: "my-production-cluster",
			expected:    "my-production-cluster-root",
		},
		{
			name:        "short name",
			clusterName: "c1",
			expected:    "c1-root",
		},
		{
			name:        "empty name",
			clusterName: "",
			expected:    "-root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetRootIssuerName(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetCAIssuerName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		expected    string
	}{
		{
			name:        "simple cluster name",
			clusterName: "test-cluster",
			expected:    "test-cluster-ca",
		},
		{
			name:        "cluster with numbers",
			clusterName: "cluster123",
			expected:    "cluster123-ca",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetCAIssuerName(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetServiceName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		expected    string
	}{
		{
			name:        "simple cluster name",
			clusterName: "test-cluster",
			expected:    "s-test-cluster",
		},
		{
			name:        "long cluster name",
			clusterName: "very-long-cluster-name-with-many-parts",
			expected:    "s-very-long-cluster-name-with-many-parts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetServiceName(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetInternalServiceHost(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		clusterNS   string
		expected    string
	}{
		{
			name:        "basic cluster",
			clusterName: "test",
			clusterNS:   "default",
			expected:    "s-test.default.svc",
		},
		{
			name:        "cluster in custom namespace",
			clusterName: "prod-cluster",
			clusterNS:   "production",
			expected:    "s-prod-cluster.production.svc",
		},
		{
			name:        "cluster in kube-system",
			clusterName: "system-cluster",
			clusterNS:   "kube-system",
			expected:    "s-system-cluster.kube-system.svc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			cluster.Namespace = tt.clusterNS
			result := GetInternalServiceHost(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetEtcdClientServiceDNSName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		clusterNS   string
		expected    string
	}{
		{
			name:        "basic cluster",
			clusterName: "test",
			clusterNS:   "default",
			expected:    "e-test-etcd-client.default.svc",
		},
		{
			name:        "cluster with dashes",
			clusterName: "my-cluster",
			clusterNS:   "production",
			expected:    "e-my-cluster-etcd-client.production.svc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			cluster.Namespace = tt.clusterNS
			result := GetEtcdClientServiceDNSName(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetEtcdDNSNames(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		clusterNS   string
		expected    map[string]string
	}{
		{
			name:        "simple cluster",
			clusterName: "test",
			clusterNS:   "default",
			expected: map[string]string{
				"test-etcd-0": "test-etcd-0.e-test-etcd.default.svc",
				"test-etcd-1": "test-etcd-1.e-test-etcd.default.svc",
				"test-etcd-2": "test-etcd-2.e-test-etcd.default.svc",
			},
		},
		{
			name:        "cluster with dashes",
			clusterName: "my-cluster",
			clusterNS:   "production",
			expected: map[string]string{
				"my-cluster-etcd-0": "my-cluster-etcd-0.e-my-cluster-etcd.production.svc",
				"my-cluster-etcd-1": "my-cluster-etcd-1.e-my-cluster-etcd.production.svc",
				"my-cluster-etcd-2": "my-cluster-etcd-2.e-my-cluster-etcd.production.svc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			cluster.Namespace = tt.clusterNS
			result := GetEtcdDNSNames(cluster)
			g.Expect(result).To(Equal(tt.expected))

			// Verify we always get exactly 3 entries
			g.Expect(result).To(HaveLen(3))
		})
	}
}

func TestGetKubeconfigSecretName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name           string
		clusterName    string
		kubeconfigName string
		expected       string
	}{
		{
			name:           "admin kubeconfig",
			clusterName:    "test-cluster",
			kubeconfigName: "admin",
			expected:       "test-cluster-admin-kubeconfig",
		},
		{
			name:           "controller-manager kubeconfig",
			clusterName:    "prod",
			kubeconfigName: "controller-manager",
			expected:       "prod-controller-manager-kubeconfig",
		},
		{
			name:           "scheduler kubeconfig",
			clusterName:    "dev-cluster",
			kubeconfigName: "scheduler",
			expected:       "dev-cluster-scheduler-kubeconfig",
		},
		{
			name:           "empty kubeconfig name",
			clusterName:    "test",
			kubeconfigName: "",
			expected:       "test--kubeconfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetKubeconfigSecretName(cluster, tt.kubeconfigName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetKonnectivityServerHost(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name                     string
		clusterName              string
		controlPlaneEndpointHost string
		expected                 string
	}{
		{
			name:                     "basic endpoint",
			clusterName:              "test-cluster",
			controlPlaneEndpointHost: "api.example.com",
			expected:                 "konnectivity.api.example.com",
		},
		{
			name:                     "ip endpoint",
			clusterName:              "test",
			controlPlaneEndpointHost: "192.168.1.100",
			expected:                 "konnectivity.192.168.1.100",
		},
		{
			name:                     "localhost endpoint",
			clusterName:              "local",
			controlPlaneEndpointHost: "localhost",
			expected:                 "konnectivity.localhost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			cluster.Spec.ControlPlaneEndpoint.Host = tt.controlPlaneEndpointHost
			result := GetKonnectivityServerHost(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetTLSRouteName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		expected    string
	}{
		{
			name:        "simple name",
			clusterName: "test",
			expected:    "test",
		},
		{
			name:        "name with dashes",
			clusterName: "my-production-cluster",
			expected:    "my-production-cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetTLSRouteName(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetKonnectivityTLSRouteName(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name        string
		clusterName string
		expected    string
	}{
		{
			name:        "simple name",
			clusterName: "test",
			expected:    "test-konnectivity",
		},
		{
			name:        "name with dashes",
			clusterName: "my-cluster",
			expected:    "my-cluster-konnectivity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &capiv2.Cluster{}
			cluster.Name = tt.clusterName
			result := GetKonnectivityTLSRouteName(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

// Test multiple certificate name functions together since they follow the same pattern.
func TestCertificateNames(t *testing.T) {
	g := NewWithT(t)
	cluster := &capiv2.Cluster{}
	cluster.Name = "test-cluster"

	tests := []struct {
		name     string
		function func(*capiv2.Cluster) string
		expected string
	}{
		{"GetCACertificateName", GetCACertificateName, "test-cluster-ca"},
		{"GetAPIServerCertificateName", GetAPIServerCertificateName, "test-cluster-apiserver"},
		{
			"GetAPIServerKubeletClientCertificateName",
			GetAPIServerKubeletClientCertificateName,
			"test-cluster-apiserver-kubelet-client",
		},
		{"GetFrontProxyCertificateName", GetFrontProxyCertificateName, "test-cluster-front-proxy"},
		{"GetServiceAccountCertificateName", GetServiceAccountCertificateName, "test-cluster-service-account"},
		{"GetAdminCertificateName", GetAdminCertificateName, "test-cluster-admin"},
		{
			"GetControllerManagerKubeconfigCertificateName",
			GetControllerManagerKubeconfigCertificateName,
			"test-cluster-controller-manager",
		},
		{"GetSchedulerKubeconfigCertificateName", GetSchedulerKubeconfigCertificateName, "test-cluster-scheduler"},
		{"GetEtcdServerCertificateName", GetEtcdServerCertificateName, "test-cluster-etcd-server"},
		{"GetEtcdPeerCertificateName", GetEtcdPeerCertificateName, "test-cluster-etcd-peer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.function(cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
