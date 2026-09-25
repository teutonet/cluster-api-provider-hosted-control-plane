package util

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test/k8scompat"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

func TestAuditPolicy_NoLenientDecodingAcrossSupportedMinors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs real containers, skipped in -short")
	}
	g, ctx, _ := G(t)

	policyYaml, err := PolicyToYaml(auditv1.Policy{
		Rules: []auditv1.PolicyRule{
			{Level: auditv1.LevelNone},
		},
	})
	g.Expect(err).NotTo(HaveOccurred())

	saKeyPEM, saCertPEM := mustSelfSignedKeyPair(g)
	caKeyPEM, caCertPEM := mustSelfSignedKeyPair(g)
	_ = caKeyPEM // only the cert is needed as --client-ca-file

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			g, _, _ := G(t)
			container, err := testcontainers.Run(ctx, "registry.k8s.io/kube-apiserver:"+version,
				testcontainers.WithFiles(
					testcontainers.ContainerFile{
						Reader:            bytes.NewReader([]byte(policyYaml)),
						ContainerFilePath: "/policy.yaml",
						FileMode:          0o644,
					},
					testcontainers.ContainerFile{
						Reader:            bytes.NewReader(saKeyPEM),
						ContainerFilePath: "/sa.key",
						FileMode:          0o644,
					},
					testcontainers.ContainerFile{
						Reader:            bytes.NewReader(saCertPEM),
						ContainerFilePath: "/sa.crt",
						FileMode:          0o644,
					},
					testcontainers.ContainerFile{
						Reader:            bytes.NewReader(caCertPEM),
						ContainerFilePath: "/ca.crt",
						FileMode:          0o644,
					},
				),
				testcontainers.WithCmd(
					"kube-apiserver",
					"--audit-policy-file=/policy.yaml",
					"--audit-log-path=-",
					"--etcd-servers=http://127.0.0.1:1",
					"--service-cluster-ip-range=10.0.0.0/24",
					"--secure-port=6443",
					"--service-account-issuer=https://example.com",
					"--service-account-key-file=/sa.crt",
					"--service-account-signing-key-file=/sa.key",
					"--client-ca-file=/ca.crt",
				),
				// No wait strategy: kube-apiserver never reaches "ready" here (etcd
				// is unreachable on purpose, so it retries forever) and, critically,
				// the "Audit policy ... falling back to lenient decoding" line this
				// test is looking for only gets logged on the *failure* path — a
				// valid policy (what a passing run means) never logs anything
				// matching "Audit policy" at all, so waiting for that string would
				// hang until timeout on every success. Give it a fixed grace period
				// instead, long enough to be well past the audit-policy-loading step
				// (observed well under 1s), then read whatever it logged.
			)
			g.Expect(err).NotTo(HaveOccurred())
			defer func() { _ = container.Terminate(ctx) }()

			time.Sleep(3 * time.Second)

			logs, err := k8scompat.CollectLogs(ctx, container)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(logs).NotTo(ContainSubstring("lenient decoding"),
				"kube-apiserver %s silently dropped an unrecognized field from our generated audit Policy:\n%s",
				version, logs)
		})
	}
}

func mustSelfSignedKeyPair(g Gomega) (keyPEM, certPEM []byte) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	g.Expect(err).NotTo(HaveOccurred())

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "k8scompat-test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	g.Expect(err).NotTo(HaveOccurred())

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return keyPEM, certPEM
}
