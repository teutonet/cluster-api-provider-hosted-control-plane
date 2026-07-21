package certificates

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

type caBundleFixture struct {
	kind             string
	caSecretName     string
	bundleSecretName string
}

func caBundleFixtures() []caBundleFixture {
	return []caBundleFixture{
		{"CA", names.GetCASecretName(bundleTestCluster), names.GetCABundleSecretName(bundleTestCluster)},
		{"etcd CA", names.GetEtcdCASecretName(bundleTestCluster), names.GetEtcdCABundleSecretName(bundleTestCluster)},
		{
			"front-proxy CA",
			names.GetFrontProxyCASecretName(bundleTestCluster),
			names.GetFrontProxyCABundleSecretName(bundleTestCluster),
		},
	}
}

func newBundleReconciler(kubeClient *fake.Clientset) *certificateReconciler {
	return &certificateReconciler{
		kubernetesClient: kubeClient,
		tracer:           "test",
	}
}

func caSecret(name string, caCert []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: bundleTestHCP.Namespace,
		},
		Data: map[string][]byte{konstants.CACertName: caCert},
	}
}

func existingBundleSecret(name string, current, old []byte) *corev1.Secret {
	bundle := append(append([]byte{}, old...), current...)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: bundleTestHCP.Namespace,
		},
		Data: map[string][]byte{
			caBundleCurrentCertKey: current,
			caBundleOldCertKey:     old,
			konstants.CACertName:   bundle,
		},
	}
}

func getBundle(ctx context.Context, g Gomega, kubeClient *fake.Clientset, name string) *corev1.Secret {
	bundle, err := kubeClient.CoreV1().Secrets(bundleTestHCP.Namespace).
		Get(ctx, name, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	return bundle
}

func TestReconcileCABundles_InitialCreate(t *testing.T) {
	g, ctx, _ := G(t)
	fixtures := caBundleFixtures()
	certs := make(map[string][]byte, len(fixtures))
	objects := []runtime.Object{}
	for _, fixture := range fixtures {
		cert := []byte(fixture.kind + "-cert-v1")
		certs[fixture.kind] = cert
		objects = append(objects, caSecret(fixture.caSecretName, cert))
	}
	kubeClient := fake.NewClientset(objects...)
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundles(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	for _, fixture := range fixtures {
		bundle := getBundle(ctx, g, kubeClient, fixture.bundleSecretName)
		g.Expect(bundle.Data[caBundleCurrentCertKey]).To(Equal(certs[fixture.kind]))
		g.Expect(bundle.Data[caBundleOldCertKey]).To(BeEmpty())
		g.Expect(bundle.Data[konstants.CACertName]).To(Equal(certs[fixture.kind]))
	}
}

func TestReconcileCABundles_NoChange(t *testing.T) {
	g, ctx, _ := G(t)
	fixtures := caBundleFixtures()
	objects := []runtime.Object{}
	for _, fixture := range fixtures {
		cert := []byte(fixture.kind + "-cert-v1")
		objects = append(objects,
			caSecret(fixture.caSecretName, cert),
			existingBundleSecret(fixture.bundleSecretName, cert, nil),
		)
	}
	kubeClient := fake.NewClientset(objects...)
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundles(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	for _, fixture := range fixtures {
		bundle := getBundle(ctx, g, kubeClient, fixture.bundleSecretName)
		g.Expect(bundle.Data[caBundleOldCertKey]).To(BeEmpty())
	}
}

func TestReconcileCABundles_Rotation(t *testing.T) {
	g, ctx, _ := G(t)
	fixtures := caBundleFixtures()
	oldCerts := make(map[string][]byte, len(fixtures))
	newCerts := make(map[string][]byte, len(fixtures))
	objects := []runtime.Object{}
	for _, fixture := range fixtures {
		oldCert := []byte(fixture.kind + "-cert-v1")
		newCert := []byte(fixture.kind + "-cert-v2")
		oldCerts[fixture.kind] = oldCert
		newCerts[fixture.kind] = newCert
		objects = append(objects,
			caSecret(fixture.caSecretName, newCert),
			existingBundleSecret(fixture.bundleSecretName, oldCert, nil),
		)
	}
	kubeClient := fake.NewClientset(objects...)
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundles(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	for _, fixture := range fixtures {
		bundle := getBundle(ctx, g, kubeClient, fixture.bundleSecretName)
		g.Expect(bundle.Data[caBundleCurrentCertKey]).To(Equal(newCerts[fixture.kind]))
		g.Expect(bundle.Data[caBundleOldCertKey]).To(Equal(oldCerts[fixture.kind]))
	}
}

func TestReconcileCABundles_MissingCASecrets_NotReady(t *testing.T) {
	g, ctx, _ := G(t)
	r := newBundleReconciler(fake.NewClientset()) // no CA secrets at all

	notReady, err := r.ReconcileCABundles(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(ContainSubstring("CA secret not yet available"))
	g.Expect(notReady).To(ContainSubstring(","))
}

func TestReconcileCABundles_PartiallyMissing_ReconcilesTheOthers(t *testing.T) {
	g, ctx, _ := G(t)
	fixtures := caBundleFixtures()
	rootCert := []byte("ca-cert-v1")
	kubeClient := fake.NewClientset(
		caSecret(fixtures[0].caSecretName, rootCert),
		// remaining CA secrets not present yet
	)
	r := newBundleReconciler(kubeClient)

	notReady, err := r.ReconcileCABundles(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).NotTo(BeEmpty())

	rootBundle := getBundle(ctx, g, kubeClient, fixtures[0].bundleSecretName)
	g.Expect(rootBundle.Data[caBundleCurrentCertKey]).To(Equal(rootCert))

	for _, fixture := range fixtures[1:] {
		_, err = kubeClient.CoreV1().Secrets(bundleTestHCP.Namespace).
			Get(ctx, fixture.bundleSecretName, metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
	}
}
