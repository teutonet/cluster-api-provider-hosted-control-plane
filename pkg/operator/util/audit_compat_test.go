package util

import (
	"bytes"
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test/k8scompat"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

func testAuditPolicyYAML(g Gomega) string {
	policyYaml, err := PolicyToYaml(auditv1.Policy{
		Rules: []auditv1.PolicyRule{
			{Level: auditv1.LevelNone},
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	return policyYaml
}

// runKubeAPIServerWithAuditPolicy starts a real kube-apiserver for the given
// version with policyYaml as its audit policy, waits until it's logged
// that it finished loading the audit policy (see the wait strategy comment
// below), and returns everything logged up to then.
func runKubeAPIServerWithAuditPolicy(g Gomega, ctx context.Context, version, policyYaml string) string {
	saKeyPEM, saCertPEM := k8scompat.MustSelfSignedKeyPair(g)
	_, caCertPEM := k8scompat.MustSelfSignedKeyPair(g)

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
			// klog.V(4).InfoS("Load audit policy rules success", ...) in
			// k8s.io/apiserver/pkg/audit/policy/reader.go is gated behind -v=4;
			// without this flag that line is never emitted at all.
			"--v=4",
		),
		// kube-apiserver never reaches "ready" here (etcd is unreachable on
		// purpose, so it retries forever) — this has to wait on a log line,
		// not on the container becoming healthy. LoadPolicyFromBytes (in
		// k8s.io/apiserver/pkg/audit/policy/reader.go, confirmed identical
		// across all 5 supported minors' vendored apiserver in this repo's
		// module cache) always logs "Load audit policy rules success" once
		// decoding finishes — on the lenient-fallback path it logs the
		// "falling back to lenient decoding" warning first and then this
		// same success line right after, so waiting for this line is a real
		// completion signal for the exact code path under test, on both
		// outcomes, not a guess about unrelated downstream steps or a fixed
		// duration. (Earlier attempts waited on downstream lines like
		// "Loaded ... admission controller(s) successfully", which raced
		// against etcd-retry-dependent setup and flaked unpredictably, and
		// on a fixed sleep, which is exactly the kind of time-based
		// assumption this is meant to avoid.)
		testcontainers.WithWaitStrategy(
			wait.ForLog("Load audit policy rules success").WithStartupTimeout(15*time.Second),
		),
	)
	g.Expect(err).NotTo(HaveOccurred())
	defer func() { _ = container.Terminate(ctx) }()

	logs, err := k8scompat.CollectLogs(ctx, container)
	g.Expect(err).NotTo(HaveOccurred())
	return logs
}

func TestAuditPolicy_NoLenientDecodingAcrossSupportedMinors(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	policyYaml := testAuditPolicyYAML(g)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			g, _, _ := G(t)
			logs := runKubeAPIServerWithAuditPolicy(g, ctx, version, policyYaml)
			// See kube-apiserver's audit/policy/reader.go (upstream): this exact
			// wording is what it logs when strict decoding fails and it falls
			// back to lenient decoding, silently dropping the unrecognized
			// field. See TestAuditPolicy_LogsLenientDecodingOnUnknownField,
			// which proves today's wording still fires.
			g.Expect(logs).NotTo(ContainSubstring("lenient decoding"),
				"kube-apiserver %s silently dropped an unrecognized field from our generated audit Policy:\n%s",
				version, logs)
		})
	}
}

// TestAuditPolicy_LogsLenientDecodingOnUnknownField is a negative control:
// it proves the "lenient decoding" detector actually fires today, so the
// positive test above isn't silently useless if kube-apiserver ever stops
// logging that line.
func TestAuditPolicy_LogsLenientDecodingOnUnknownField(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	version := versions[len(versions)-1]

	brokenPolicyYaml := testAuditPolicyYAML(g) + "  bogusUnknownField: true\n"

	logs := runKubeAPIServerWithAuditPolicy(g, ctx, version, brokenPolicyYaml)
	g.Expect(logs).To(ContainSubstring("lenient decoding"),
		"kube-apiserver %s did not log a lenient-decoding fallback for an injected unknown field"+
			" — the detector may be broken:\n%s",
		version, logs)
}
