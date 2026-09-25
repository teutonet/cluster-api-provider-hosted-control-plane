package util

import (
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"k8s.io/utils/ptr"
)

const (
	maxKonnectivityMinor = 36        // renovate: datasource=docker depName=kas-network-proxy/proxy-server registryUrl=registry.k8s.io extractVersion=^v0\.(?<version>\d+)\.0$
	coreDNSTag           = "v1.12.0" // renovate: datasource=docker depName=coredns/coredns registryUrl=registry.k8s.io extractVersion=^(?<version>v\d+\.\d+\.\d+)$
	nginxTag             = "1.31.6"  // renovate: datasource=docker depName=nginx registryUrl=docker.io extractVersion=^(?<version>\d+\.\d+\.\d+)$
	// Tracked independently from go.etcd.io/etcd's module version: registry.k8s.io publishes
	// etcd images on its own cadence, not in lockstep with client library releases, and etcd's
	// client/server API is compatible across minor versions regardless.
	etcdTag = "3.7.1" // renovate: datasource=docker depName=etcd registryUrl=registry.k8s.io extractVersion=^(?<version>\d+\.\d+\.\d+)-0$
)

func buildImageString(registry, repository, tag string) string {
	if registry == "" {
		return fmt.Sprintf("%s:%s", repository, tag)
	}
	return fmt.Sprintf("%s/%s:%s", registry, repository, tag)
}

func resolveImageFromSpec(imageSpec *v1alpha1.ImageSpec, defaultRegistry, defaultRepository, defaultTag string) string {
	if imageSpec == nil {
		return buildImageString(defaultRegistry, defaultRepository, defaultTag)
	}

	return buildImageString(
		ptr.Deref(imageSpec.Registry, defaultRegistry),
		ptr.Deref(imageSpec.Repository, defaultRepository),
		ptr.Deref(imageSpec.Tag, defaultTag),
	)
}

func ResolveKubernetesComponentImage(imageSpec *v1alpha1.ImageSpec, component, version string) string {
	return resolveImageFromSpec(imageSpec, "registry.k8s.io", component, version)
}

func ResolveETCDImage(imageSpec *v1alpha1.ImageSpec) string {
	return resolveImageFromSpec(imageSpec, "registry.k8s.io", "etcd", etcdTag+"-0")
}

// DefaultETCDVersion returns the etcd server version deployed when no image tag is
// overridden, for callers that need to reason about the default server's behavior
// (e.g. picking client-side flags/arguments based on the server's version).
func DefaultETCDVersion() string {
	return etcdTag
}

func ResolveKonnectivityImage(imageSpec *v1alpha1.ImageSpec, component string, minorVersion uint64) string {
	return resolveImageFromSpec(
		imageSpec,
		"registry.k8s.io",
		fmt.Sprintf("kas-network-proxy/%s", component),
		fmt.Sprintf("v0.%d.0", min(minorVersion, uint64(maxKonnectivityMinor))),
	)
}

func ResolveKubeProxyImage(imageSpec *v1alpha1.ImageSpec, version string) string {
	return resolveImageFromSpec(imageSpec, "k8s.gcr.io", "kube-proxy", version)
}

func ResolveCoreDNSImage(imageSpec *v1alpha1.ImageSpec) string {
	return resolveImageFromSpec(imageSpec, "registry.k8s.io", "coredns/coredns", coreDNSTag)
}

func ResolveNginxImage(imageSpec *v1alpha1.ImageSpec) string {
	return resolveImageFromSpec(imageSpec, "docker.io", "nginx", nginxTag)
}
