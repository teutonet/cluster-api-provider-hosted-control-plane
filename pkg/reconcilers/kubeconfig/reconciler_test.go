package kubeconfig

import (
	"fmt"
	"testing"

	slices "github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				require.NoError(t, err)

				expectedServer := fmt.Sprintf("https://%s", tt.cluster.Spec.ControlPlaneEndpoint.String())
				assert.Equal(t, expectedServer, kubeconfig.Clusters[tt.cluster.Name].Server)
				assert.NotEmpty(t, kubeconfig.Clusters[tt.cluster.Name].CertificateAuthorityData)
				assert.NotEmpty(t, kubeconfig.AuthInfos["admin"].ClientCertificateData)
				assert.NotEmpty(t, kubeconfig.AuthInfos["admin"].ClientKeyData)
			} else {
				_, err := getConcreteReconciler(reconciler).generateKubeconfig(
					ctx,
					tt.cluster,
					tt.cluster.Spec.ControlPlaneEndpoint,
					"admin",
					"test-cluster-admin",
				)
				assert.Error(t, err)
			}
		})
	}
}

func TestKubeconfigReconciler_KubeconfigConnectivity(t *testing.T) {
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
			require.NoError(t, err)

			assert.Contains(t, kubeconfig.Clusters, tt.cluster.Name)
			cluster := kubeconfig.Clusters[tt.cluster.Name]
			assert.Equal(t, tt.expectedServer, cluster.Server)

			assert.Equal(t, tt.expectedContext, kubeconfig.CurrentContext)
			assert.Contains(t, kubeconfig.Contexts, tt.expectedContext)

			assert.Contains(t, kubeconfig.AuthInfos, userName)
			authInfo := kubeconfig.AuthInfos[userName]
			assert.NotEmpty(t, authInfo.ClientCertificateData)
			assert.NotEmpty(t, authInfo.ClientKeyData)

			assert.NotEmpty(t, cluster.CertificateAuthorityData)
		})
	}
}

func TestKubeconfigReconciler_CertificateRotation(t *testing.T) {
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
	require.NoError(t, err)

	updatedCertSecret := oldCertSecret.DeepCopy()
	updatedCertSecret.Data[corev1.TLSCertKey] = []byte("new-cert-data")
	updatedCertSecret.Data[corev1.TLSPrivateKeyKey] = []byte("new-key-data")

	_, err = kubeClient.CoreV1().Secrets(cluster.Namespace).Update(
		ctx, updatedCertSecret, metav1.UpdateOptions{})
	require.NoError(t, err)

	kubeconfig2, err := reconciler.generateKubeconfig(ctx, cluster, endpoint, "admin", "test-cluster-admin-kubeconfig")
	require.NoError(t, err)

	authInfo1 := kubeconfig1.AuthInfos["admin"]
	authInfo2 := kubeconfig2.AuthInfos["admin"]

	assert.Equal(t, []byte("old-cert-data"), authInfo1.ClientCertificateData)
	assert.Equal(t, []byte("old-key-data"), authInfo1.ClientKeyData)

	assert.Equal(t, []byte("new-cert-data"), authInfo2.ClientCertificateData)
	assert.Equal(t, []byte("new-key-data"), authInfo2.ClientKeyData)

	assert.Equal(t, kubeconfig1.CurrentContext, kubeconfig2.CurrentContext)
	assert.Equal(t, kubeconfig1.Clusters[cluster.Name].Server, kubeconfig2.Clusters[cluster.Name].Server)
}

func TestKubeconfigReconciler_MultiUserScenarios(t *testing.T) {
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
		require.NoError(t, err, "Failed to generate kubeconfig for user %s", user.name)
		kubeconfigs[user.name] = kubeconfig
	}

	for _, user := range users {
		kubeconfig := kubeconfigs[user.name]

		expectedContext := fmt.Sprintf("%s@%s", user.name, cluster.Name)
		assert.Equal(t, expectedContext, kubeconfig.CurrentContext)

		assert.Contains(t, kubeconfig.AuthInfos, user.name)
		assert.NotEmpty(t, kubeconfig.AuthInfos[user.name].ClientCertificateData)
		assert.NotEmpty(t, kubeconfig.AuthInfos[user.name].ClientKeyData)

		assert.Contains(t, kubeconfig.Clusters, cluster.Name)
		clusterInfo := kubeconfig.Clusters[cluster.Name]
		assert.Equal(t, "https://api.example.com:443", clusterInfo.Server)
		assert.NotEmpty(t, clusterInfo.CertificateAuthorityData)
	}

	caData := kubeconfigs["admin"].Clusters[cluster.Name].CertificateAuthorityData
	for _, user := range users {
		assert.Equal(t, caData, kubeconfigs[user.name].Clusters[cluster.Name].CertificateAuthorityData,
			"All kubeconfigs should use the same CA data")
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
