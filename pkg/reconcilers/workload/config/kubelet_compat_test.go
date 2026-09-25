package config

import (
	"bytes"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test/k8scompat"
)

func TestKubeletConfiguration_NoLenientDecodingAcrossSupportedMinors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs real containers, skipped in -short")
	}
	g, ctx, _ := G(t)

	reconciler := newTestReconciler(nil).(*configReconciler)
	kubeletConfiguration := reconciler.buildKubeletConfiguration()
	configYaml, err := operatorutil.ToYaml(&kubeletConfiguration)
	g.Expect(err).NotTo(HaveOccurred())

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			g, _, _ := G(t)
			container, err := k8scompat.KubeadmKubeletContainer(ctx, version,
				testcontainers.WithFiles(testcontainers.ContainerFile{
					Reader:            bytes.NewReader([]byte(configYaml)),
					ContainerFilePath: "/kubelet-config.yaml",
					FileMode:          0o644,
				}),
				testcontainers.WithCmd("/kubelet", "--config", "/kubelet-config.yaml"),
				// kubelet always exits non-zero here — no real container runtime,
				// no CA file, no API server — but it decodes and logs its effective
				// config (or a lenient-decoding fallback) before any of that, so we
				// only need it to exit, then read whatever it logged.
				testcontainers.WithWaitStrategy(wait.ForExit()),
			)
			g.Expect(err).NotTo(HaveOccurred())
			defer func() { _ = container.Terminate(ctx) }()

			logs, err := k8scompat.CollectLogs(ctx, container)
			g.Expect(err).NotTo(HaveOccurred())
			// See plan's Review Focus: this substring is upstream's current wording for
			// "I couldn't strictly decode this and silently dropped a field."
			g.Expect(logs).NotTo(ContainSubstring("lenient decoding"),
				"kubelet %s silently dropped an unrecognized field from our generated KubeletConfiguration:\n%s",
				version, logs)
		})
	}
}
