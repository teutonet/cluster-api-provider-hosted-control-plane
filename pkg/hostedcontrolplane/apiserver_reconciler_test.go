package hostedcontrolplane

import (
	"context"
	"testing"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type testSecret struct {
	name string
	data map[string][]byte
}

func createTestCluster(name string) *capiv1.Cluster {
	return &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func createTestSecrets(hostedControlPlane *v1alpha1.HostedControlPlane, dataSuffix string) []testSecret {
	cluster := createTestCluster(hostedControlPlane.Name)
	return []testSecret{
		{
			name: names.GetCASecretName(cluster),
			data: map[string][]byte{
				"tls.crt": []byte("ca-cert-data" + dataSuffix),
				"tls.key": []byte("ca-key-data" + dataSuffix),
			},
		},
		{
			name: names.GetFrontProxyCASecretName(cluster),
			data: map[string][]byte{
				"tls.crt": []byte("front-proxy-ca-cert-data" + dataSuffix),
			},
		},
		{
			name: names.GetFrontProxySecretName(cluster),
			data: map[string][]byte{
				"tls.crt": []byte("front-proxy-cert-data" + dataSuffix),
				"tls.key": []byte("front-proxy-key-data" + dataSuffix),
			},
		},
		{
			name: names.GetServiceAccountSecretName(cluster),
			data: map[string][]byte{
				"tls.crt": []byte("service-account-cert-data" + dataSuffix),
				"tls.key": []byte("service-account-key-data" + dataSuffix),
			},
		},
		{
			name: names.GetAPIServerSecretName(cluster),
			data: map[string][]byte{
				"tls.crt": []byte("apiserver-cert-data" + dataSuffix),
				"tls.key": []byte("apiserver-key-data" + dataSuffix),
			},
		},
		{
			name: names.GetAPIServerKubeletClientSecretName(cluster),
			data: map[string][]byte{
				"tls.crt": []byte("apiserver-kubelet-client-cert-data" + dataSuffix),
				"tls.key": []byte("apiserver-kubelet-client-key-data" + dataSuffix),
			},
		},
		{
			name: names.GetKubeconfigSecretName(cluster, konstants.KubeScheduler),
			data: map[string][]byte{
				"value": []byte("scheduler-kubeconfig-data" + dataSuffix),
			},
		},
		{
			name: names.GetKubeconfigSecretName(cluster, konstants.KubeControllerManager),
			data: map[string][]byte{
				"value": []byte("controller-manager-kubeconfig-data" + dataSuffix),
			},
		},
	}
}

func TestDeploymentReconciler_calculateSecretChecksum(t *testing.T) {
	fakeClient := fake.NewClientset()

	hostedControlPlane := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	testSecrets := createTestSecrets(hostedControlPlane, "")

	for _, secret := range testSecrets {
		_, err := fakeClient.CoreV1().Secrets(hostedControlPlane.Namespace).Create(
			context.Background(),
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secret.name,
					Namespace: hostedControlPlane.Namespace,
				},
				Data: secret.data,
			},
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("failed to create test secret %s: %v", secret.name, err)
		}
	}

	checksum, err := util.CalculateSecretChecksum(t.Context(), fakeClient,
		hostedControlPlane.Namespace,
		slices.Map(testSecrets, func(s testSecret, _ int) string {
			return s.name
		}),
	)
	if err != nil {
		t.Fatalf("failed to calculate secret checksum: %v", err)
	}

	expectedValue := "ec5094f462cf427eb76d9c5792ff73c8"
	if checksum != expectedValue {
		t.Errorf("expected checksum to be %s, got %s", expectedValue, checksum)
	}

	if len(checksum) != 32 {
		t.Errorf("expected checksum to be 32 characters long, got %d", len(checksum))
	}

	for _, char := range checksum {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			t.Errorf("checksum contains invalid hex character: %c", char)
		}
	}

	checksum2, err := util.CalculateSecretChecksum(t.Context(), fakeClient,
		hostedControlPlane.Namespace,
		slices.Map(testSecrets, func(s testSecret, _ int) string {
			return s.name
		}),
	)
	if err != nil {
		t.Fatalf("failed to calculate secret checksum second time: %v", err)
	}

	if checksum != checksum2 {
		t.Errorf("checksums should be identical, got %s and %s", checksum, checksum2)
	}
}

func TestDeploymentReconciler_calculateSecretChecksum_DifferentValues(t *testing.T) {
	fakeClient := fake.NewClientset()

	hostedControlPlane := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	// Create secrets with different values
	testSecrets := createTestSecrets(hostedControlPlane, "-different")

	for _, secret := range testSecrets {
		_, err := fakeClient.CoreV1().Secrets(hostedControlPlane.Namespace).Create(
			context.Background(),
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secret.name,
					Namespace: hostedControlPlane.Namespace,
				},
				Data: secret.data,
			},
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("failed to create test secret %s: %v", secret.name, err)
		}
	}

	checksum, err := util.CalculateSecretChecksum(t.Context(), fakeClient,
		hostedControlPlane.Namespace,
		slices.Map(testSecrets, func(s testSecret, _ int) string {
			return s.name
		}),
	)
	if err != nil {
		t.Fatalf("failed to calculate secret checksum: %v", err)
	}

	// This should be different from the original test
	expectedValue := "ec5094f462cf427eb76d9c5792ff73c8"
	if checksum == expectedValue {
		t.Errorf("checksum should be different from original test, but got same value: %s", checksum)
	}

	// Verify it's still a valid checksum
	if len(checksum) != 32 {
		t.Errorf("expected checksum to be 32 characters long, got %d", len(checksum))
	}

	for _, char := range checksum {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			t.Errorf("checksum contains invalid hex character: %c", char)
		}
	}
}

func TestDeploymentReconciler_calculateSecretChecksum_MissingSecret(t *testing.T) {
	fakeClient := fake.NewClientset()

	hostedControlPlane := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	// Create only some of the required secrets, leaving one missing
	testSecrets := []testSecret{
		{
			name: names.GetCASecretName(createTestCluster(hostedControlPlane.Name)),
			data: map[string][]byte{
				"tls.crt": []byte("ca-cert-data"),
				"tls.key": []byte("ca-key-data"),
			},
		},
		{
			name: names.GetFrontProxyCASecretName(createTestCluster(hostedControlPlane.Name)),
			data: map[string][]byte{
				"tls.crt": []byte("front-proxy-ca-cert-data"),
			},
		},
		// Intentionally missing other secrets
	}

	for _, secret := range testSecrets {
		_, err := fakeClient.CoreV1().Secrets(hostedControlPlane.Namespace).Create(
			context.Background(),
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secret.name,
					Namespace: hostedControlPlane.Namespace,
				},
				Data: secret.data,
			},
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("failed to create test secret %s: %v", secret.name, err)
		}
	}

	_, err := util.CalculateSecretChecksum(t.Context(), fakeClient,
		hostedControlPlane.Namespace,
		append(slices.Map(testSecrets, func(s testSecret, _ int) string {
			return s.name
		}), "missing-secret-name"),
	)
	if err == nil {
		t.Fatal("expected error when secret is missing, but got nil")
	}

	if !apierrors.IsNotFound(err) {
		t.Errorf("expected error to be NotFound, but got: %v", err)
	}
}
