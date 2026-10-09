package names

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// The names are the contract with everything outside of the controller: already deployed clusters, CAPI, the gateway
// and users' tooling. Renaming a resource orphans the old one and breaks every lookup of it, so a change of a name
// must be deliberate. This is the one place where the literal strings live; every other test derives names from the
// helpers instead of hardcoding them.
func stableNamesCluster() *capiv2.Cluster {
	cluster := &capiv2.Cluster{}
	cluster.Name = "example-cluster"
	cluster.Namespace = "example-ns"
	cluster.Spec.ControlPlaneEndpoint.Host = "api.example.com"
	return cluster
}

func TestResourceNamesAreStable(t *testing.T) {
	tests := []struct {
		name     string
		function func(*capiv2.Cluster) string
		expected string
	}{
		// issuers and certificate authorities
		{"GetRootIssuerName", GetRootIssuerName, "example-cluster-root"},
		{"GetCAIssuerName", GetCAIssuerName, "example-cluster-ca"},
		{"GetCACertificateName", GetCACertificateName, "example-cluster-ca"},
		{"GetCASecretName", GetCASecretName, "example-cluster-ca"},
		{"GetCABundleSecretName", GetCABundleSecretName, "example-cluster-ca-bundle"},
		{"GetFrontProxyCAName", GetFrontProxyCAName, "example-cluster-front-proxy-ca"},
		{"GetFrontProxyCASecretName", GetFrontProxyCASecretName, "example-cluster-front-proxy-ca"},
		{"GetFrontProxyCABundleSecretName", GetFrontProxyCABundleSecretName, "example-cluster-front-proxy-ca-bundle"},
		{"GetEtcdCAName", GetEtcdCAName, "example-cluster-etcd-ca"},
		{"GetEtcdCASecretName", GetEtcdCASecretName, "example-cluster-etcd-ca"},
		{"GetEtcdCABundleSecretName", GetEtcdCABundleSecretName, "example-cluster-etcd-ca-bundle"},

		// control plane certificates and their secrets
		{"GetAPIServerCertificateName", GetAPIServerCertificateName, "example-cluster-apiserver"},
		{"GetAPIServerSecretName", GetAPIServerSecretName, "example-cluster-apiserver"},
		{
			"GetAPIServerKubeletClientCertificateName",
			GetAPIServerKubeletClientCertificateName,
			"example-cluster-apiserver-kubelet-client",
		},
		{
			"GetAPIServerKubeletClientSecretName",
			GetAPIServerKubeletClientSecretName,
			"example-cluster-apiserver-kubelet-client",
		},
		{"GetFrontProxyCertificateName", GetFrontProxyCertificateName, "example-cluster-front-proxy"},
		{"GetFrontProxySecretName", GetFrontProxySecretName, "example-cluster-front-proxy"},
		{"GetServiceAccountCertificateName", GetServiceAccountCertificateName, "example-cluster-service-account"},
		{"GetServiceAccountSecretName", GetServiceAccountSecretName, "example-cluster-service-account"},

		// client certificates behind the generated kubeconfigs
		{"GetAdminCertificateName", GetAdminCertificateName, "example-cluster-admin"},
		{"GetAdminKubeconfigCertificateName", GetAdminKubeconfigCertificateName, "example-cluster-admin"},
		{
			"GetAdminKubeconfigCertificateSecretName",
			GetAdminKubeconfigCertificateSecretName,
			"example-cluster-admin",
		},
		{
			"GetControllerManagerKubeconfigCertificateName",
			GetControllerManagerKubeconfigCertificateName,
			"example-cluster-controller-manager",
		},
		{
			"GetControllerManagerKubeconfigCertificateSecretName",
			GetControllerManagerKubeconfigCertificateSecretName,
			"example-cluster-controller-manager",
		},
		{
			"GetSchedulerKubeconfigCertificateName",
			GetSchedulerKubeconfigCertificateName,
			"example-cluster-scheduler",
		},
		{
			"GetSchedulerKubeconfigCertificateSecretName",
			GetSchedulerKubeconfigCertificateSecretName,
			"example-cluster-scheduler",
		},
		{
			"GetKonnectivityClientKubeconfigCertificateName",
			GetKonnectivityClientKubeconfigCertificateName,
			"example-cluster-konnectivity-client",
		},
		{
			"GetKonnectivityClientKubeconfigCertificateSecretName",
			GetKonnectivityClientKubeconfigCertificateSecretName,
			"example-cluster-konnectivity-client",
		},
		{
			"GetControlPlaneControllerKubeconfigCertificateName",
			GetControlPlaneControllerKubeconfigCertificateName,
			"example-cluster-control-plane-controller",
		},
		{
			"GetControlPlaneControllerKubeconfigCertificateSecretName",
			GetControlPlaneControllerKubeconfigCertificateSecretName,
			"example-cluster-control-plane-controller",
		},
		{
			"GetKubeconfigSecretName",
			func(cluster *capiv2.Cluster) string { return GetKubeconfigSecretName(cluster, "admin") },
			"example-cluster-admin-kubeconfig",
		},

		// etcd certificates and their secrets
		{"GetEtcdServerCertificateName", GetEtcdServerCertificateName, "example-cluster-etcd-server"},
		{"GetEtcdServerSecretName", GetEtcdServerSecretName, "example-cluster-etcd-server"},
		{"GetEtcdPeerCertificateName", GetEtcdPeerCertificateName, "example-cluster-etcd-peer"},
		{"GetEtcdPeerSecretName", GetEtcdPeerSecretName, "example-cluster-etcd-peer"},
		{
			"GetEtcdAPIServerClientCertificateName",
			GetEtcdAPIServerClientCertificateName,
			"example-cluster-etcd-apiserver-client",
		},
		{
			"GetEtcdAPIServerClientCertificateSecretName",
			GetEtcdAPIServerClientCertificateSecretName,
			"example-cluster-etcd-apiserver-client",
		},
		{
			"GetEtcdControllerClientCertificateName",
			GetEtcdControllerClientCertificateName,
			"example-cluster-etcd-controller-client",
		},
		{
			"GetEtcdControllerClientCertificateSecretName",
			GetEtcdControllerClientCertificateSecretName,
			"example-cluster-etcd-controller-client",
		},

		// services, workloads and the hostnames derived from them
		{"GetServiceName", GetServiceName, "s-example-cluster"},
		{"GetInternalServiceHost", GetInternalServiceHost, "s-example-cluster.example-ns.svc"},
		{"GetEtcdServiceName", GetEtcdServiceName, "e-example-cluster-etcd"},
		{"GetEtcdClientServiceName", GetEtcdClientServiceName, "e-example-cluster-etcd-client"},
		{
			"GetEtcdClientServiceDNSName",
			GetEtcdClientServiceDNSName,
			"e-example-cluster-etcd-client.example-ns.svc",
		},
		{"GetEtcdStatefulSetName", GetEtcdStatefulSetName, "example-cluster-etcd"},

		// configuration
		{"GetKonnectivityConfigMapName", GetKonnectivityConfigMapName, "example-cluster-konnectivity"},
		{"GetAuthenticationConfigMapName", GetAuthenticationConfigMapName, "example-cluster-authentication"},
		{"GetAuditWebhookSecretName", GetAuditWebhookSecretName, "example-cluster-audit-webhook"},

		// gateway
		{"GetTLSRouteName", GetTLSRouteName, "example-cluster"},
		{"GetKonnectivityTLSRouteName", GetKonnectivityTLSRouteName, "example-cluster-konnectivity"},
		{"GetKonnectivityServerHost", GetKonnectivityServerHost, "konnectivity.api.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			g.Expect(tt.function(stableNamesCluster())).To(Equal(tt.expected))
		})
	}
}

func TestEtcdDNSNamesAreStable(t *testing.T) {
	g, _, _ := G(t)
	g.Expect(GetEtcdDNSNames(stableNamesCluster())).To(Equal(map[string]string{
		"example-cluster-etcd-0": "example-cluster-etcd-0.e-example-cluster-etcd.example-ns.svc",
		"example-cluster-etcd-1": "example-cluster-etcd-1.e-example-cluster-etcd.example-ns.svc",
		"example-cluster-etcd-2": "example-cluster-etcd-2.e-example-cluster-etcd.example-ns.svc",
	}))
}

// TestEveryNameHelperIsStabilityTested fails when a helper is added to names.go without being referenced by the
// tests of this package, so a new name can't slip in without its exact value being pinned.
func TestEveryNameHelperIsStabilityTested(t *testing.T) {
	g, _, _ := G(t)
	fileSet := token.NewFileSet()

	source, err := parser.ParseFile(fileSet, "names.go", nil, 0)
	g.Expect(err).NotTo(HaveOccurred())
	helpers := map[string]struct{}{}
	for _, declaration := range source.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Name.IsExported() {
			helpers[function.Name.Name] = struct{}{}
		}
	}
	g.Expect(helpers).NotTo(BeEmpty())

	referenced := map[string]struct{}{}
	for _, testFile := range []string{"names_stability_test.go", "names_test.go"} {
		parsed, err := parser.ParseFile(fileSet, testFile, nil, 0)
		g.Expect(err).NotTo(HaveOccurred())
		ast.Inspect(parsed, func(node ast.Node) bool {
			if identifier, ok := node.(*ast.Ident); ok {
				referenced[identifier.Name] = struct{}{}
			}
			return true
		})
	}

	var untested []string
	for helper := range helpers {
		if _, ok := referenced[helper]; !ok {
			untested = append(untested, helper)
		}
	}
	sort.Strings(untested)
	g.Expect(untested).To(BeEmpty(), "add the exact expected value of each of these to TestResourceNamesAreStable")
}
