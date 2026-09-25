package k8scompat

import (
	"context"
	"fmt"
	"io"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// KubeadmKubeletContainer builds (once per version; the container engine's
// own build cache dedupes repeat calls) the fetch image containing the real
// kubeadm and kubelet binaries for the given Kubernetes patch version, and
// starts a container from it. Callers pick what to run inside via
// testcontainers.WithCmd, and how to wait for it via
// testcontainers.WithWaitStrategy — this only wires up the build.
func KubeadmKubeletContainer(
	ctx context.Context,
	version string,
	opts ...testcontainers.ContainerCustomizer,
) (testcontainers.Container, error) {
	buildArgValue := version
	dockerfile := testcontainers.WithDockerfile(testcontainers.FromDockerfile{
		Context:    "testdata/kubeadm-kubelet",
		Dockerfile: "Containerfile",
		BuildArgs:  map[string]*string{"K8S_VERSION": &buildArgValue},
	})

	allOpts := append([]testcontainers.ContainerCustomizer{
		dockerfile,
		testcontainers.WithWaitStrategy(wait.ForExit()),
	}, opts...)

	container, err := testcontainers.Run(ctx, "", allOpts...)
	if err != nil {
		return container, fmt.Errorf("starting kubeadm/kubelet fetch container for %s: %w", version, err)
	}
	return container, nil
}

// CollectLogs reads the full stdout/stderr of a container. Meant for
// short-lived containers (config validate, a briefly-started kubelet/
// kube-proxy/kube-apiserver) — not suitable for long-running ones.
func CollectLogs(ctx context.Context, container testcontainers.Container) (string, error) {
	reader, err := container.Logs(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching container logs: %w", err)
	}
	defer func() { _ = reader.Close() }()
	logs, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("reading container logs: %w", err)
	}
	return string(logs), nil
}
