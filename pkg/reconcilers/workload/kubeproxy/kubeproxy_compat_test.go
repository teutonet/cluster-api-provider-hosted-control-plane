package kubeproxy

import (
	"bytes"
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

func TestKubeProxyConfiguration_NoLenientDecodingAcrossSupportedMinors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs real containers, skipped in -short")
	}
	g, ctx, _ := G(t)

	reconciler := &kubeProxyReconciler{
		podCIDR:                  net.IPNet{IP: net.IPv4(10, 244, 0, 0), Mask: net.CIDRMask(16, 32)},
		kubeProxyConfigMountPath: "/var/lib/kube-proxy",
	}
	kubeProxyConfig := reconciler.buildKubeProxyConfiguration("kubeconfig.conf")
	configYaml, err := kubeadmutil.MarshalToYamlForCodecs(
		&kubeProxyConfig,
		kubeproxyv1alpha1.SchemeGroupVersion,
		componentconfigs.Codecs,
	)
	g.Expect(err).NotTo(HaveOccurred())

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			g, _, _ := G(t)
			container, err := testcontainers.Run(ctx, "registry.k8s.io/kube-proxy:"+version,
				testcontainers.WithFiles(testcontainers.ContainerFile{
					Reader:            bytes.NewReader(configYaml),
					ContainerFilePath: "/config.yaml",
					FileMode:          0o644,
				}),
				testcontainers.WithCmd("kube-proxy", "--config", "/config.yaml"),
				// kube-proxy exits almost immediately here (no real kubeconfig file
				// mounted) — that's expected and fine, we only care whether it
				// logged a lenient-decoding fallback before it got that far.
				testcontainers.WithWaitStrategy(wait.ForExit()),
			)
			g.Expect(err).NotTo(HaveOccurred())
			defer func() { _ = container.Terminate(ctx) }()

			logs, err := k8scompat.CollectLogs(ctx, container)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(logs).NotTo(ContainSubstring("lenient decoding"),
				"kube-proxy %s silently dropped an unrecognized field from our generated KubeProxyConfiguration:\n%s",
				version, logs)
		})
	}
}
