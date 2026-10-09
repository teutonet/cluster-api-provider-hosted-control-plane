package kubeproxy

import (
	"bytes"
	"context"
	"net"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test/k8scompat"
	kubeproxyv1alpha1 "k8s.io/kube-proxy/config/v1alpha1"
	"k8s.io/kubernetes/cmd/kubeadm/app/componentconfigs"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
)

const testKubeProxyConfigMountPath = "/var/lib/kube-proxy"

func buildTestKubeProxyConfigYAML(g Gomega, kubeconfigFileName string) []byte {
	reconciler := &kubeProxyReconciler{
		podCIDR:                  net.IPNet{IP: net.IPv4(10, 244, 0, 0), Mask: net.CIDRMask(16, 32)},
		kubeProxyConfigMountPath: testKubeProxyConfigMountPath,
	}
	kubeProxyConfig := reconciler.buildKubeProxyConfiguration(kubeconfigFileName)
	configYaml, err := kubeadmutil.MarshalToYamlForCodecs(
		&kubeProxyConfig,
		kubeproxyv1alpha1.SchemeGroupVersion,
		componentconfigs.Codecs,
	)
	g.Expect(err).NotTo(HaveOccurred())
	return configYaml
}

// runKubeProxyWithConfig starts the real kube-proxy for the given version
// with configYaml, waits for it to exit (it always does here — no real
// kubeconfig file is ever mounted, deliberately: see the positive-sentinel
// comment in the caller for why that's actually what makes this a useful
// test rather than a slower, privilege-dependent one), and returns
// everything it logged.
func runKubeProxyWithConfig(g Gomega, ctx context.Context, version string, configYaml []byte) string {
	container, err := testcontainers.Run(ctx, "registry.k8s.io/kube-proxy:"+version,
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            bytes.NewReader(configYaml),
			ContainerFilePath: "/config.yaml",
			FileMode:          0o644,
		}),
		testcontainers.WithCmd("kube-proxy", "--config", "/config.yaml"),
		testcontainers.WithWaitStrategy(wait.ForExit()),
	)
	g.Expect(err).NotTo(HaveOccurred())
	defer func() { _ = container.Terminate(ctx) }()

	logs, err := k8scompat.CollectLogs(ctx, container)
	g.Expect(err).NotTo(HaveOccurred())
	return logs
}

func TestKubeProxyConfiguration_NoLenientDecodingAcrossSupportedMinors(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	const kubeconfigFileName = "kubeconfig.conf"
	configYaml := buildTestKubeProxyConfigYAML(g, kubeconfigFileName)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			g, _, _ := G(t)
			logs := runKubeProxyWithConfig(g, ctx, version, configYaml)
			// Positive sentinel: kube-proxy can only know to look for
			// testKubeProxyConfigMountPath/kubeconfigFileName by having
			// already decoded our ClientConnection.Kubeconfig field out of
			// this exact YAML — it has no other source for that path. Seeing
			// it quoted back in the "no such file" error is direct proof our
			// config was decoded, not vacuous proof via an unrelated log
			// line whose presence varies by version (tried and abandoned
			// for kubelet/kube-apiserver's equivalent tests — see their
			// history for why).
			g.Expect(logs).To(ContainSubstring(testKubeProxyConfigMountPath+"/"+kubeconfigFileName),
				"kube-proxy %s never referenced our configured kubeconfig path"+
					" — it may have failed before reaching config decode:\n%s",
				version, logs)
			g.Expect(logs).NotTo(ContainSubstring("lenient decoding"),
				"kube-proxy %s silently dropped an unrecognized field from our generated KubeProxyConfiguration:\n%s",
				version, logs)
		})
	}
}

// TestKubeProxyConfiguration_LogsLenientDecodingOnUnknownField is a
// negative control: it proves the "lenient decoding" detector actually
// fires today, so the positive test above isn't silently useless if
// kube-proxy ever stops logging that line.
func TestKubeProxyConfiguration_LogsLenientDecodingOnUnknownField(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	version := versions[len(versions)-1]

	configYaml := append(buildTestKubeProxyConfigYAML(g, "kubeconfig.conf"), []byte("bogusUnknownField: true\n")...)

	logs := runKubeProxyWithConfig(g, ctx, version, configYaml)
	g.Expect(logs).To(ContainSubstring("lenient decoding"),
		"kube-proxy %s did not log a lenient-decoding fallback for an injected unknown field"+
			" — the detector may be broken:\n%s",
		version, logs)
}
