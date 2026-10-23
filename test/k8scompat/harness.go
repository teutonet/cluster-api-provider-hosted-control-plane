package k8scompat

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// kubeadmKubeletBuildContext resolves the Containerfile fixture relative to
// this source file, not the calling test binary's working directory — the
// working directory is the *caller's* package dir under `go test`, which
// differs from this package's own dir whenever a test outside
// test/k8scompat calls KubeadmKubeletContainer.
func kubeadmKubeletBuildContext() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller(0) failed to report this file's own path")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "kubeadm-kubelet")
}

// KubeadmKubeletContainer builds the fetch image containing the real
// kubeadm and kubelet binaries for the given Kubernetes patch version, and
// starts a container from it. The image is tagged deterministically by
// version and kept after the container stops (KeepImage), so the several
// compat tests that each call this for the same version reuse one image
// instead of rebuilding (and re-downloading both binaries) from scratch
// every time. Callers pick what to run inside via testcontainers.WithCmd,
// and how to wait for it via testcontainers.WithWaitStrategy — this only
// wires up the build.
func KubeadmKubeletContainer(
	ctx context.Context,
	version string,
	opts ...testcontainers.ContainerCustomizer,
) (testcontainers.Container, error) {
	buildArgValue := version
	dockerfile := testcontainers.WithDockerfile(testcontainers.FromDockerfile{
		Context:    kubeadmKubeletBuildContext(),
		Dockerfile: "Containerfile",
		BuildArgs:  map[string]*string{"K8S_VERSION": &buildArgValue},
		Repo:       "k8scompat-kubeadm-kubelet",
		Tag:        version,
		KeepImage:  true,
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
