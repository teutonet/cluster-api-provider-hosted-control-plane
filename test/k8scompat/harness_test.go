package k8scompat

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestKubeadmKubeletContainer_KubeadmVersionMatches(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs real containers, skipped in -short")
	}
	g, ctx, _ := G(t)

	container, err := KubeadmKubeletContainer(
		ctx,
		"v1.37.1",
		testcontainers.WithCmd("/kubeadm", "version", "-o", "short"),
	)
	g.Expect(err).NotTo(HaveOccurred())
	defer func() { _ = container.Terminate(ctx) }()

	logs, err := CollectLogs(ctx, container)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(logs).To(ContainSubstring("v1.37.1"))
}
