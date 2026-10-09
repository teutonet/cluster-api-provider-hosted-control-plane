package names

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// Only the names users, tooling and the platform depend on are pinned here, in two groups by what a change costs.
// Other tests derive names from the helpers instead of hardcoding them, and internal names (e.g. the certificates of
// the control plane components) are not pinned at all.
func stableNamesCluster() *capiv2.Cluster {
	cluster := &capiv2.Cluster{}
	cluster.Name = "example-cluster"
	cluster.Namespace = "example-ns"
	cluster.Spec.ControlPlaneEndpoint.Host = "api.example.com"
	return cluster
}

// TestNamesThatMustNeverChange pins the names whose change breaks existing clusters or their users, there is no safe
// way to rename them:
//   - the LoadBalancer service: a renamed service is a new load balancer with a new IP, which the apiserver
//     certificates, kubeconfigs, DNS and the status of every cluster reference
//   - the hostnames: they end up in certificates, in the agents and their network policy and in the DNS (wildcard)
//     users configured
//   - the etcd names: the statefulset name is part of the volume claim names (a rename orphans the data), the member
//     names end up in the peer URLs and the certificates
//   - the secrets users, CAPI and tooling read the CA and the kubeconfigs from
func TestNamesThatMustNeverChange(t *testing.T) {
	tests := []struct {
		name     string
		function func(*capiv2.Cluster) string
		expected string
	}{
		{"GetServiceName", GetServiceName, "s-example-cluster"},
		{"GetInternalServiceHost", GetInternalServiceHost, "s-example-cluster.example-ns.svc"},
		{"GetKonnectivityServerHost", GetKonnectivityServerHost, "konnectivity.api.example.com"},

		{"GetEtcdStatefulSetName", GetEtcdStatefulSetName, "example-cluster-etcd"},
		{"GetEtcdServiceName", GetEtcdServiceName, "e-example-cluster-etcd"},
		{"GetEtcdClientServiceName", GetEtcdClientServiceName, "e-example-cluster-etcd-client"},
		{
			"GetEtcdClientServiceDNSName",
			GetEtcdClientServiceDNSName,
			"e-example-cluster-etcd-client.example-ns.svc",
		},

		{"GetCASecretName", GetCASecretName, "example-cluster-ca"},
		{"GetCABundleSecretName", GetCABundleSecretName, "example-cluster-ca-bundle"},
		{
			"GetKubeconfigSecretName",
			func(cluster *capiv2.Cluster) string { return GetKubeconfigSecretName(cluster, "admin") },
			"example-cluster-admin-kubeconfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			g.Expect(tt.function(stableNamesCluster())).To(Equal(tt.expected))
		})
	}

	t.Run("GetEtcdDNSNames", func(t *testing.T) {
		g, _, _ := G(t)
		g.Expect(GetEtcdDNSNames(stableNamesCluster())).To(Equal(map[string]string{
			"example-cluster-etcd-0": "example-cluster-etcd-0.e-example-cluster-etcd.example-ns.svc",
			"example-cluster-etcd-1": "example-cluster-etcd-1.e-example-cluster-etcd.example-ns.svc",
			"example-cluster-etcd-2": "example-cluster-etcd-2.e-example-cluster-etcd.example-ns.svc",
		}))
	})
}

// TestNamesWhoseChangeCausesABriefInterruption pins the names where a rename is possible but not free: the operator
// applies resources by name, so the new resource is created next to the old one, and the old one has to be deleted
// explicitly. Done in the wrong order, or with a gateway that drops open connections when a route disappears, the
// clients of the hostname (kubectl, CAPI, the konnectivity agents) get a short blip of failing connections. Create
// the new resource, wait until it is accepted, only then delete the old one. Everything that looks the resource up by
// its name has to change as well.
func TestNamesWhoseChangeCausesABriefInterruption(t *testing.T) {
	tests := []struct {
		name     string
		function func(*capiv2.Cluster) string
		expected string
	}{
		{"GetTLSRouteName", GetTLSRouteName, "example-cluster"},
		{"GetKonnectivityTLSRouteName", GetKonnectivityTLSRouteName, "example-cluster-konnectivity"},
		{"GetKonnectivityServiceName", GetKonnectivityServiceName, "s-example-cluster-konnectivity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			g.Expect(tt.function(stableNamesCluster())).To(Equal(tt.expected))
		})
	}
}
