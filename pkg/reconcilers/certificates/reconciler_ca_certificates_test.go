package certificates

import (
	"testing"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagermetav1 "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	cmfake "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/fake"
	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCAReconciler(cmClient *cmfake.Clientset) *certificateReconciler {
	return &certificateReconciler{
		certManagerClient:         cmClient,
		rootCACertificateDuration: time.Hour,
		caCertificateDuration:     time.Hour,
		tracer:                    "test",
	}
}

func readyIssuer(name string) *certmanagerv1.Issuer {
	return &certmanagerv1.Issuer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: bundleTestHCP.Namespace},
		Status: certmanagerv1.IssuerStatus{
			Conditions: []certmanagerv1.IssuerCondition{
				{Type: certmanagerv1.IssuerConditionReady, Status: certmanagermetav1.ConditionTrue},
			},
		},
	}
}

func readyCertificate(name string, secretName string) *certmanagerv1.Certificate {
	return &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: bundleTestHCP.Namespace},
		Spec:       certmanagerv1.CertificateSpec{SecretName: secretName},
		Status: certmanagerv1.CertificateStatus{
			Conditions: []certmanagerv1.CertificateCondition{
				{Type: certmanagerv1.CertificateConditionReady, Status: certmanagermetav1.ConditionTrue},
			},
		},
	}
}

func TestReconcileCACertificates_AllReady(t *testing.T) {
	g, ctx, _ := G(t)

	rootIssuerName := names.GetRootIssuerName(bundleTestCluster)
	kubeCAIssuerName := names.GetCAIssuerName(bundleTestCluster)
	etcdCAName := names.GetEtcdCAName(bundleTestCluster)
	frontProxyCAName := names.GetFrontProxyCAName(bundleTestCluster)

	cmClient := cmfake.NewClientset(
		readyIssuer(rootIssuerName),
		readyCertificate(names.GetCACertificateName(bundleTestCluster), names.GetCASecretName(bundleTestCluster)),
		readyIssuer(kubeCAIssuerName),
		readyCertificate(etcdCAName, names.GetEtcdCASecretName(bundleTestCluster)),
		readyIssuer(etcdCAName),
		readyCertificate(frontProxyCAName, names.GetFrontProxyCASecretName(bundleTestCluster)),
		readyIssuer(frontProxyCAName),
	)
	r := newCAReconciler(cmClient)

	notReady, err := r.ReconcileCACertificates(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	certificateClient := cmClient.CertmanagerV1().Certificates(bundleTestHCP.Namespace)
	etcdCert, err := certificateClient.Get(ctx, etcdCAName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(etcdCert.Spec.IssuerRef.Name).To(Equal(kubeCAIssuerName))

	frontProxyCert, err := certificateClient.Get(ctx, frontProxyCAName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(frontProxyCert.Spec.IssuerRef.Name).To(Equal(kubeCAIssuerName))
}

func TestReconcileCACertificates_RootIssuerNotReady(t *testing.T) {
	g, ctx, _ := G(t)
	r := newCAReconciler(cmfake.NewClientset())

	notReady, err := r.ReconcileCACertificates(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(Equal("root issuer not ready"))

	certificates, err := r.certManagerClient.CertmanagerV1().Certificates(bundleTestHCP.Namespace).
		List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(certificates.Items).To(BeEmpty())
}

func TestReconcileCACertificates_KubernetesCANotReady_AttemptsSiblingsAnyway(t *testing.T) {
	g, ctx, _ := G(t)
	rootIssuerName := names.GetRootIssuerName(bundleTestCluster)

	cmClient := cmfake.NewClientset(readyIssuer(rootIssuerName))
	r := newCAReconciler(cmClient)

	notReady, err := r.ReconcileCACertificates(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(Equal(
		"kubernetes CA certificate not ready,etcd CA certificate not ready,front-proxy CA certificate not ready",
	))
}

func TestReconcileCACertificates_IntermediateCertNotReady(t *testing.T) {
	g, ctx, _ := G(t)

	rootIssuerName := names.GetRootIssuerName(bundleTestCluster)
	kubeCAIssuerName := names.GetCAIssuerName(bundleTestCluster)
	frontProxyCAName := names.GetFrontProxyCAName(bundleTestCluster)

	cmClient := cmfake.NewClientset(
		readyIssuer(rootIssuerName),
		readyCertificate(names.GetCACertificateName(bundleTestCluster), names.GetCASecretName(bundleTestCluster)),
		readyIssuer(kubeCAIssuerName),
		readyCertificate(frontProxyCAName, names.GetFrontProxyCASecretName(bundleTestCluster)),
		readyIssuer(frontProxyCAName),
	)
	r := newCAReconciler(cmClient)

	notReady, err := r.ReconcileCACertificates(ctx, bundleTestHCP, bundleTestCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(Equal("etcd CA certificate not ready"))
}
