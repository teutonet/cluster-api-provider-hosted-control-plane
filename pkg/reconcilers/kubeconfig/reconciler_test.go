package kubeconfig

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd/api"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func getConcreteReconciler(r KubeconfigReconciler) *kubeconfigReconciler {
	return r.(*kubeconfigReconciler)
}

func TestKubeconfigReconciler_ReconcileWorkflow(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name               string
		hostedControlPlane *v1alpha1.HostedControlPlane
		cluster            *capiv2.Cluster
		existingSecrets    []*corev1.Secret
		expectedSecrets    []string
		expectedError      bool
	}{
		{
			name: "successful kubeconfig generation for all components",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
			existingSecrets: []*corev1.Secret{
				createCertificateSecret("test-cluster-ca", "default", true),
				createCertificateSecret("test-cluster-admin", "default", false),
				createCertificateSecret("test-cluster-kube-controller-manager", "default", false),
				createCertificateSecret("test-cluster-kube-scheduler", "default", false),
				createCertificateSecret("test-cluster-konnectivity-client", "default", false),
				createCertificateSecret("test-cluster-controller", "default", false),
			},
			expectedSecrets: []string{
				"test-cluster-kubeconfig",
				"test-cluster-kube-controller-manager-kubeconfig-secret",
				"test-cluster-kube-scheduler-kubeconfig-secret",
				"test-cluster-konnectivity-client-kubeconfig-secret",
				"test-cluster-controller-kubeconfig-secret",
			},
			expectedError: false,
		},
		{
			name: "missing certificate secret should cause error",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
			},
			existingSecrets: []*corev1.Secret{
				createCertificateSecret("test-cluster-ca", "default", true),
			},
			expectedError: true,
		},
		{
			name: "missing CA secret should cause error",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
			},
			existingSecrets: []*corev1.Secret{
				createCertificateSecret("test-cluster-admin", "default", false),
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewClientset(slices.Map(tt.existingSecrets,
				func(s *corev1.Secret, _ int) runtime.Object {
					return s
				},
			)...)

			reconciler := NewKubeconfigReconciler(
				kubeClient,
				443,
				"konnectivity-client",
				"controller",
			)

			ctx := t.Context()

			if !tt.expectedError {
				kubeconfig, err := getConcreteReconciler(reconciler).generateKubeconfig(
					ctx,
					tt.cluster,
					tt.cluster.Spec.ControlPlaneEndpoint,
					"admin",
					"test-cluster-admin",
				)
				g.Expect(err).NotTo(HaveOccurred())

				expectedServer := fmt.Sprintf("https://%s", tt.cluster.Spec.ControlPlaneEndpoint.String())
				g.Expect(kubeconfig.Clusters[tt.cluster.Name].Server).To(Equal(expectedServer))
				g.Expect(kubeconfig.Clusters[tt.cluster.Name].CertificateAuthorityData).ToNot(BeEmpty())
				g.Expect(kubeconfig.AuthInfos["admin"].ClientCertificateData).ToNot(BeEmpty())
				g.Expect(kubeconfig.AuthInfos["admin"].ClientKeyData).ToNot(BeEmpty())
			} else {
				_, err := getConcreteReconciler(reconciler).generateKubeconfig(
					ctx,
					tt.cluster,
					tt.cluster.Spec.ControlPlaneEndpoint,
					"admin",
					"test-cluster-admin",
				)
				g.Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestKubeconfigReconciler_KubeconfigConnectivity(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name            string
		cluster         *capiv2.Cluster
		endpointType    string
		expectedServer  string
		expectedContext string
	}{
		{
			name: "admin kubeconfig uses external endpoint",
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
			endpointType:    "external",
			expectedServer:  "https://api.test-cluster.example.com:443",
			expectedContext: "admin@test-cluster",
		},
		{
			name: "controller manager kubeconfig uses internal service endpoint",
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
			},
			endpointType:    "internal",
			expectedServer:  "https://test-cluster:443",
			expectedContext: "kube-controller-manager@test-cluster",
		},
		{
			name: "konnectivity kubeconfig uses localhost endpoint",
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
			},
			endpointType:    "localhost",
			expectedServer:  "https://localhost:6443",
			expectedContext: "konnectivity-client@test-cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets := []*corev1.Secret{
				createCertificateSecret("test-cluster-ca", "default", true),
			}

			var certSecretName, userName string
			var endpoint capiv2.APIEndpoint

			switch tt.endpointType {
			case "external":
				certSecretName = "test-cluster-admin"
				userName = "admin"
				endpoint = tt.cluster.Spec.ControlPlaneEndpoint
			case "internal":
				certSecretName = "test-cluster-kube-controller-manager"
				userName = "kube-controller-manager"
				endpoint = capiv2.APIEndpoint{Host: "test-cluster", Port: 443}
			case "localhost":
				certSecretName = "test-cluster-konnectivity-client"
				userName = "konnectivity-client"
				endpoint = capiv2.APIEndpoint{Host: "localhost", Port: 6443}
			}

			secrets = append(secrets, createCertificateSecret(certSecretName, "default", false))

			kubeClient := fake.NewClientset(slices.Map(secrets,
				func(s *corev1.Secret, _ int) runtime.Object {
					return s
				},
			)...)

			reconciler := &kubeconfigReconciler{
				ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
					KubernetesClient: kubeClient,
				},
			}

			ctx := t.Context()
			kubeconfig, err := getConcreteReconciler(
				reconciler,
			).generateKubeconfig(ctx, tt.cluster, endpoint, userName, certSecretName)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(kubeconfig.Clusters).To(HaveKey(tt.cluster.Name))
			cluster := kubeconfig.Clusters[tt.cluster.Name]
			g.Expect(cluster.Server).To(Equal(tt.expectedServer))

			g.Expect(kubeconfig.CurrentContext).To(Equal(tt.expectedContext))
			g.Expect(kubeconfig.Contexts).To(HaveKey(tt.expectedContext))

			g.Expect(kubeconfig.AuthInfos).To(HaveKey(userName))
			authInfo := kubeconfig.AuthInfos[userName]
			g.Expect(authInfo.ClientCertificateData).ToNot(BeEmpty())
			g.Expect(authInfo.ClientKeyData).ToNot(BeEmpty())

			g.Expect(cluster.CertificateAuthorityData).ToNot(BeEmpty())
		})
	}
}

func TestKubeconfigReconciler_CertificateRotation(t *testing.T) {
	g := NewWithT(t)
	cluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	oldCertSecret := createCertificateSecret("test-cluster-admin-kubeconfig", "default", false)
	oldCertSecret.Data[corev1.TLSCertKey] = []byte("old-cert-data")
	oldCertSecret.Data[corev1.TLSPrivateKeyKey] = []byte("old-key-data")

	caSecret := createCertificateSecret("test-cluster-ca", "default", true)

	kubeClient := fake.NewClientset(oldCertSecret, caSecret)

	reconciler := &kubeconfigReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			KubernetesClient: kubeClient,
		},
	}

	ctx := t.Context()
	endpoint := capiv2.APIEndpoint{Host: "api.example.com", Port: 443}
	kubeconfig1, err := reconciler.generateKubeconfig(ctx, cluster, endpoint, "admin", "test-cluster-admin-kubeconfig")
	g.Expect(err).NotTo(HaveOccurred())

	updatedCertSecret := oldCertSecret.DeepCopy()
	updatedCertSecret.Data[corev1.TLSCertKey] = []byte("new-cert-data")
	updatedCertSecret.Data[corev1.TLSPrivateKeyKey] = []byte("new-key-data")

	_, err = kubeClient.CoreV1().Secrets(cluster.Namespace).Update(
		ctx, updatedCertSecret, metav1.UpdateOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	kubeconfig2, err := reconciler.generateKubeconfig(ctx, cluster, endpoint, "admin", "test-cluster-admin-kubeconfig")
	g.Expect(err).NotTo(HaveOccurred())

	authInfo1 := kubeconfig1.AuthInfos["admin"]
	authInfo2 := kubeconfig2.AuthInfos["admin"]

	g.Expect(authInfo1.ClientCertificateData).To(Equal([]byte("old-cert-data")))
	g.Expect(authInfo1.ClientKeyData).To(Equal([]byte("old-key-data")))

	g.Expect(authInfo2.ClientCertificateData).To(Equal([]byte("new-cert-data")))
	g.Expect(authInfo2.ClientKeyData).To(Equal([]byte("new-key-data")))

	g.Expect(kubeconfig1.CurrentContext).To(Equal(kubeconfig2.CurrentContext))
	g.Expect(kubeconfig1.Clusters[cluster.Name].Server).To(Equal(kubeconfig2.Clusters[cluster.Name].Server))
}

func TestKubeconfigReconciler_MultiUserScenarios(t *testing.T) {
	g := NewWithT(t)
	cluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	users := []struct {
		name       string
		secretName string
	}{
		{"admin", "test-cluster-admin-kubeconfig"},
		{"controller-manager", "test-cluster-controller-manager-kubeconfig"},
		{"scheduler", "test-cluster-scheduler-kubeconfig"},
	}

	secrets := []*corev1.Secret{createCertificateSecret("test-cluster-ca", "default", true)}
	for _, user := range users {
		secrets = append(secrets, createCertificateSecret(user.secretName, "default", false))
	}

	kubeClient := fake.NewClientset(slices.Map(secrets,
		func(s *corev1.Secret, _ int) runtime.Object {
			return s
		},
	)...)

	reconciler := &kubeconfigReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			KubernetesClient: kubeClient,
		},
	}

	ctx := t.Context()
	endpoint := capiv2.APIEndpoint{Host: "api.example.com", Port: 443}
	kubeconfigs := make(map[string]*api.Config)

	for _, user := range users {
		kubeconfig, err := getConcreteReconciler(
			reconciler,
		).generateKubeconfig(ctx, cluster, endpoint, user.name, user.secretName)
		g.Expect(err).NotTo(HaveOccurred())
		kubeconfigs[user.name] = kubeconfig
	}

	for _, user := range users {
		kubeconfig := kubeconfigs[user.name]

		expectedContext := fmt.Sprintf("%s@%s", user.name, cluster.Name)
		g.Expect(kubeconfig.CurrentContext).To(Equal(expectedContext))

		g.Expect(kubeconfig.AuthInfos).To(HaveKey(user.name))
		g.Expect(kubeconfig.AuthInfos[user.name].ClientCertificateData).ToNot(BeEmpty())
		g.Expect(kubeconfig.AuthInfos[user.name].ClientKeyData).ToNot(BeEmpty())

		g.Expect(kubeconfig.Clusters).To(HaveKey(cluster.Name))
		clusterInfo := kubeconfig.Clusters[cluster.Name]
		g.Expect(clusterInfo.Server).To(Equal("https://api.example.com:443"))
		g.Expect(clusterInfo.CertificateAuthorityData).ToNot(BeEmpty())
	}

	caData := kubeconfigs["admin"].Clusters[cluster.Name].CertificateAuthorityData
	for _, user := range users {
		g.Expect(kubeconfigs[user.name].Clusters[cluster.Name].CertificateAuthorityData).To(Equal(caData))
	}
}

func createCertificateSecret(name, namespace string, isCA bool) *corev1.Secret {
	data := map[string][]byte{
		corev1.TLSCertKey:       []byte(fmt.Sprintf("cert-data-for-%s", name)),
		corev1.TLSPrivateKeyKey: []byte(fmt.Sprintf("key-data-for-%s", name)),
	}

	if isCA {
		data[corev1.ServiceAccountRootCAKey] = []byte(fmt.Sprintf("ca-cert-data-for-%s", name))
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: data,
	}
}
