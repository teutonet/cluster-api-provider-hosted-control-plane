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

func buildTestClusterConfigurationYAML(g Gomega, version string) []byte {
	reconciler := newTestReconciler(nil).(*configReconciler)
	hostedControlPlane := &v1alpha1.HostedControlPlane{}
	hostedControlPlane.Spec.Version = version
	cluster := &capiv2.Cluster{}
	cluster.Name = "compat-test"
	cluster.Spec.ControlPlaneEndpoint = capiv2.APIEndpoint{Host: "cp.example.com", Port: 6443}

	conf, err := reconciler.buildClusterConfiguration(hostedControlPlane, cluster)
	g.Expect(err).NotTo(HaveOccurred())

	yamlBytes, err := config.MarshalKubeadmConfigObject(&conf, kubeadmv1beta4.SchemeGroupVersion)
	g.Expect(err).NotTo(HaveOccurred())
	return yamlBytes
}

func TestClusterConfiguration_ValidAcrossSupportedMinors(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			g, _, _ := G(t)
			// Build against the minor under test, not a fixed version — an
			// older minor's kubeadm should see a ClusterConfiguration for a
			// cluster actually running that minor, matching what a real
			// workload cluster would carry.
			yamlBytes := buildTestClusterConfigurationYAML(g, version)

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

// TestClusterConfiguration_KubeadmRejectsUnknownField is a negative control:
// it proves kubeadm config validate actually performs strict decoding on
// this repo's real, generated ClusterConfiguration, rather than the
// positive test above passing vacuously (e.g. because kubeadm silently
// stopped validating, or failed to start for an unrelated reason).
func TestClusterConfiguration_KubeadmRejectsUnknownField(t *testing.T) {
	k8scompat.RequireEnabled(t)
	g, ctx, _ := G(t)

	versions, err := k8scompat.SupportedVersions(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	version := versions[len(versions)-1]

	yamlBytes := buildTestClusterConfigurationYAML(g, version)
	brokenYAML := append(append([]byte{}, yamlBytes...), []byte("\nbogusUnknownField: true\n")...)

	container, err := k8scompat.KubeadmKubeletContainer(ctx, version,
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            bytes.NewReader(brokenYAML),
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
		To(Equal(1), "kubeadm %s accepted a config with an injected unknown field:\n%s", version, logs)
	g.Expect(logs).To(ContainSubstring("unknown field"))
}
