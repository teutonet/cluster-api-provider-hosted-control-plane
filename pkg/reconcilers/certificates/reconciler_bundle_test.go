package certificates

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	bundleTestCluster = &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-cluster-uid"),
		},
	}
	bundleTestHCP = &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "default",
			UID:       types.UID("test-hcp-uid"),
		},
	}
)

func bundleCASecret(caCert []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.GetCASecretName(bundleTestCluster),
			Namespace: bundleTestHCP.Namespace,
		},
		Data: map[string][]byte{konstants.CACertName: caCert},
	}
}

func existingBundleSecret(current, old []byte) *corev1.Secret {
	bundle := append(append([]byte{}, old...), current...)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.GetCABundleSecretName(bundleTestCluster),
			Namespace: bundleTestHCP.Namespace,
		},
		Data: map[string][]byte{
			caBundleCurrentCertKey: current,
			caBundleOldCertKey:     old,
			konstants.CACertName:   bundle,
		},
	}
}

func newBundleReconciler(kubeClient *fake.Clientset) *certificateReconciler {
	return &certificateReconciler{
		kubernetesClient: kubeClient,
		tracer:           "test",
	}
}

func TestReconcileCABundle_InitialCreate(t *testing.T) {
	g, ctx, _ := G(t)
	cert := []byte("ca-cert-v1")
	kubeClient := fake.NewClientset(bundleCASecret(cert))
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundle(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	bundle, err := kubeClient.CoreV1().Secrets(bundleTestHCP.Namespace).
		Get(ctx, names.GetCABundleSecretName(bundleTestCluster), metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(bundle.Data[caBundleCurrentCertKey]).To(Equal(cert))
	g.Expect(bundle.Data[caBundleOldCertKey]).NotTo(BeNil())
	g.Expect(bundle.Data[caBundleOldCertKey]).To(BeEmpty())
	g.Expect(bundle.Data[konstants.CACertName]).To(Equal(cert))
}

func TestReconcileCABundle_NoChange(t *testing.T) {
	g, ctx, _ := G(t)
	cert := []byte("ca-cert-v1")
	kubeClient := fake.NewClientset(bundleCASecret(cert), existingBundleSecret(cert, nil))
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundle(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	bundle, err := kubeClient.CoreV1().Secrets(bundleTestHCP.Namespace).
		Get(ctx, names.GetCABundleSecretName(bundleTestCluster), metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(bundle.Data[caBundleCurrentCertKey]).To(Equal(cert))
	g.Expect(bundle.Data[caBundleOldCertKey]).To(BeEmpty())
}

func TestReconcileCABundle_Rotation(t *testing.T) {
	g, ctx, _ := G(t)
	oldCert := []byte("ca-cert-v1")
	newCert := []byte("ca-cert-v2")
	kubeClient := fake.NewClientset(bundleCASecret(newCert), existingBundleSecret(oldCert, nil))
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundle(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	bundle, err := kubeClient.CoreV1().Secrets(bundleTestHCP.Namespace).
		Get(ctx, names.GetCABundleSecretName(bundleTestCluster), metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(bundle.Data[caBundleCurrentCertKey]).To(Equal(newCert))
	g.Expect(bundle.Data[caBundleOldCertKey]).To(Equal(oldCert))
	g.Expect(bundle.Data[konstants.CACertName]).To(Equal(append(append([]byte{}, oldCert...), newCert...)))
}

func TestReconcileCABundle_MissingCASecret_NotReady(t *testing.T) {
	g, ctx, _ := G(t)
	r := newBundleReconciler(fake.NewClientset()) // no CA secret

	notReady, err := r.ReconcileCABundle(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).NotTo(BeEmpty())
}
