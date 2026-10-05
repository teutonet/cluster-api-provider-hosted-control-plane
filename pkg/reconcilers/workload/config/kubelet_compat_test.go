package config

import (
	"bytes"
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test/k8scompat"
)

func buildTestKubeletConfigYAML(g Gomega) string {
	reconciler := newTestReconciler(nil).(*configReconciler)
	kubeletConfiguration := reconciler.buildKubeletConfiguration()
	configYaml, err := operatorutil.ToYaml(&kubeletConfiguration)
	g.Expect(err).NotTo(HaveOccurred())
	return configYaml
}

// runKubeletWithConfig starts the real kubelet for the given version with
// configYaml, waits for it to exit (it always does here — no container
// runtime is ever present), and returns everything it logged.
func runKubeletWithConfig(g Gomega, ctx context.Context, version, configYaml string) string {
	// Our generated KubeletConfiguration points authentication.x509.clientCAFile
	// at /etc/kubernetes/pki/ca.crt. Without a real file there, kubelet fails
	// on that check before logging anything else at all in some minors (the
	// exact point where it dies varies enough across versions that whether
	// "Kubelet version" gets logged first depends on the minor) — mounting a
	// throwaway CA cert gets every version equally far past config decode,
	// regardless of minor, before it hits the next (unrelated) missing
	// dependency.
	_, caCertPEM := k8scompat.MustSelfSignedKeyPair(g)

	container, err := k8scompat.KubeadmKubeletContainer(ctx, version,
		testcontainers.WithFiles(
			testcontainers.ContainerFile{
				Reader:            bytes.NewReader([]byte(configYaml)),
				ContainerFilePath: "/kubelet-config.yaml",
				FileMode:          0o644,
			},
			testcontainers.ContainerFile{
				Reader:            bytes.NewReader(caCertPEM),
				ContainerFilePath: "/etc/kubernetes/pki/ca.crt",
				FileMode:          0o644,
			},
		),
		testcontainers.WithCmd("/kubelet", "--config", "/kubelet-config.yaml"),
		testcontainers.WithWaitStrategy(wait.ForExit()),
	)
	g.Expect(err).NotTo(HaveOccurred())
	defer func() { _ = container.Terminate(ctx) }()

	logs, err := k8scompat.CollectLogs(ctx, container)
	g.Expect(err).NotTo(HaveOccurred())
	return logs
}

func TestKubeletConfiguration_NoLenientDecodingAcrossSupportedMinors(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	configYaml := buildTestKubeletConfigYAML(g)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			g, _, _ := G(t)
			logs := runKubeletWithConfig(g, ctx, version, configYaml)
			// Positive sentinel: proves kubelet actually got far enough to log
			// its version — an unconditional, very early log call — so the
			// negative assertion below isn't vacuously true (e.g. because the
			// binary crashed before ever parsing our config).
			g.Expect(logs).To(ContainSubstring("Kubelet version"),
				"kubelet %s never logged its version — it may have failed before reaching config decode:\n%s",
				version, logs)
			// See kubelet's codec.go (upstream): this exact wording is what it
			// logs when strict decoding fails and it falls back to lenient
			// decoding, silently dropping the unrecognized field. See
			// TestKubeletConfiguration_LogsLenientDecodingOnUnknownField, which
			// proves today's wording still fires.
			g.Expect(logs).NotTo(ContainSubstring("lenient decoding"),
				"kubelet %s silently dropped an unrecognized field from our generated KubeletConfiguration:\n%s",
				version, logs)
		})
	}
}

// TestKubeletConfiguration_LogsLenientDecodingOnUnknownField is a negative
// control: it proves the "lenient decoding" detector actually fires today,
// so the positive test above isn't silently useless if kubelet ever stops
// logging that line (or never did in some environment).
func TestKubeletConfiguration_LogsLenientDecodingOnUnknownField(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	version := versions[len(versions)-1]

	configYaml := buildTestKubeletConfigYAML(g) + "bogusUnknownField: true\n"

	logs := runKubeletWithConfig(g, ctx, version, configYaml)
	g.Expect(logs).To(ContainSubstring("lenient decoding"),
		"kubelet %s did not log a lenient-decoding fallback for an injected unknown field — the detector may be broken:\n%s",
		version, logs)
}
