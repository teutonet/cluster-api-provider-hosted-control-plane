package k8scompat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultStableReleaseBaseURL is where Kubernetes publishes the latest patch
// version per minor, e.g. https://dl.k8s.io/release/stable-1.34.txt.
const DefaultStableReleaseBaseURL = "https://dl.k8s.io/release"

// SupportedVersions returns the patch version (e.g. "v1.34.12") of every
// Kubernetes minor from 3 below the minor this module's own k8s.io/kubernetes
// dependency is pinned to, up to the latest minor Kubernetes has released.
// The upper bound is discovered dynamically so this list never goes stale as
// new minors ship or go.mod gets bumped.
func SupportedVersions(ctx context.Context) ([]string, error) {
	currentMinor, err := currentKubernetesMinor(ctx)
	if err != nil {
		return nil, fmt.Errorf("determining this module's pinned Kubernetes minor: %w", err)
	}
	return walkStableVersions(ctx, DefaultStableReleaseBaseURL, currentMinor-3)
}

func currentKubernetesMinor(ctx context.Context) (int, error) {
	// debug.ReadBuildInfo() only lists modules actually linked into the test
	// binary — this package doesn't import anything from k8s.io/kubernetes,
	// so it never shows up there even though go.mod requires it. Ask the go
	// toolchain directly instead, which resolves the real effective version
	// (including replace directives) without us re-parsing go.mod by hand.
	out, err := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Version}}", "k8s.io/kubernetes").Output()
	if err != nil {
		return 0, fmt.Errorf("running go list -m k8s.io/kubernetes: %w", err)
	}
	return parseMinor(strings.TrimSpace(string(out)))
}

func parseMinor(version string) (int, error) {
	parts := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("unparseable version %q", version)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("unparseable minor in version %q: %w", version, err)
	}
	return minor, nil
}

// walkStableVersions queries baseURL/stable-1.<minor>.txt starting at
// lowMinor and incrementing until a minor 404s, which marks the first
// not-yet-released minor.
func walkStableVersions(ctx context.Context, baseURL string, lowMinor int) ([]string, error) {
	var versions []string
	for minor := lowMinor; ; minor++ {
		version, released, err := fetchStablePatch(ctx, baseURL, minor)
		if err != nil {
			return nil, err
		}
		if !released {
			break
		}
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no released Kubernetes minor found starting at 1.%d", lowMinor)
	}
	return versions, nil
}

func fetchStablePatch(ctx context.Context, baseURL string, minor int) (string, bool, error) {
	url := fmt.Sprintf("%s/stable-1.%d.txt", baseURL, minor)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", false, fmt.Errorf("building request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("unexpected status %d fetching %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("reading response body from %s: %w", url, err)
	}
	return strings.TrimSpace(string(body)), true, nil
}
