package config

import (
	"bytes"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test/k8scompat"
	kubeadmv1beta4 "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta4"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/config"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestClusterConfiguration_ValidAcrossSupportedMinors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs real containers, skipped in -short")
	}
	g, ctx, _ := G(t)

	reconciler := newTestReconciler(nil).(*configReconciler)
	hostedControlPlane := &v1alpha1.HostedControlPlane{}
	hostedControlPlane.Spec.Version = "v1.37.1"
	cluster := &capiv2.Cluster{}
	cluster.Name = "compat-test"
	cluster.Spec.ControlPlaneEndpoint = capiv2.APIEndpoint{Host: "cp.example.com", Port: 6443}

	conf, err := reconciler.buildClusterConfiguration(hostedControlPlane, cluster)
	g.Expect(err).NotTo(HaveOccurred())

	yamlBytes, err := config.MarshalKubeadmConfigObject(&conf, kubeadmv1beta4.SchemeGroupVersion)
	g.Expect(err).NotTo(HaveOccurred())

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			g, _, _ := G(t)
			container, err := k8scompat.KubeadmKubeletContainer(ctx, version,
				testcontainers.WithFiles(testcontainers.ContainerFile{
					Reader:            bytes.NewReader(yamlBytes),
					ContainerFilePath: "/cluster-configuration.yaml",
					FileMode:          0o644,
				}),
				testcontainers.WithCmd("/kubeadm", "config", "validate", "--config", "/cluster-configuration.yaml"),
				testcontainers.WithWaitStrategy(wait.ForExit()),
			)
			g.Expect(err).NotTo(HaveOccurred())
			defer func() { _ = container.Terminate(ctx) }()

			state, err := container.State(ctx)
			g.Expect(err).NotTo(HaveOccurred())

			logs, logErr := k8scompat.CollectLogs(ctx, container)
			g.Expect(logErr).NotTo(HaveOccurred())
			g.Expect(state.ExitCode).
				To(Equal(0), "kubeadm %s rejected our generated ClusterConfiguration:\n%s", version, logs)
		})
	}
}
