package k8scompat

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	"github.com/onsi/gomega"
)

// MustSelfSignedKeyPair generates a throwaway self-signed CA keypair for
// feeding to real Kubernetes binaries that require a CA/key/cert file to
// exist on disk (kube-apiserver's --client-ca-file, kubelet's
// authentication.x509.clientCAFile) — these compat tests only need the
// binary to get past that file-existence check, not real PKI.
func MustSelfSignedKeyPair(g gomega.Gomega) (keyPEM, certPEM []byte) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "k8scompat-test"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return keyPEM, certPEM
}
