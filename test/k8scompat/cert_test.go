package k8scompat

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestMustSelfSignedKeyPair_ProducesAParsableCACert(t *testing.T) {
	g, _, _ := G(t)

	_, certPEM := MustSelfSignedKeyPair(g)

	block, rest := pem.Decode(certPEM)
	g.Expect(block).NotTo(BeNil())
	g.Expect(rest).To(BeEmpty())

	cert, err := x509.ParseCertificate(block.Bytes)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cert.IsCA).To(BeTrue())
}
