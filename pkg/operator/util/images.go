package util

import (
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
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

	registry := defaultRegistry
	if imageSpec.Registry != "" {
		registry = imageSpec.Registry
	}

	repository := defaultRepository
	if imageSpec.Repository != "" {
		repository = imageSpec.Repository
	}

	tag := defaultTag
	if imageSpec.Tag != "" {
		tag = imageSpec.Tag
	}

	return buildImageString(registry, repository, tag)
}

func ResolveKubernetesComponentImage(imageSpec *v1alpha1.ImageSpec, component, version string) string {
	return resolveImageFromSpec(imageSpec, "registry.k8s.io", component, version)
}

func ResolveETCDImage(imageSpec *v1alpha1.ImageSpec, version string) string {
	return resolveImageFromSpec(imageSpec, "registry.k8s.io", "etcd", version+"-0")
}

func ResolveKonnectivityImage(imageSpec *v1alpha1.ImageSpec, component string, minorVersion uint64) string {
	return resolveImageFromSpec(
		imageSpec,
		"registry.k8s.io",
		fmt.Sprintf("kas-network-proxy/%s", component),
		fmt.Sprintf("v0.%d.0", minorVersion),
	)
}

func ResolveKubeProxyImage(imageSpec *v1alpha1.ImageSpec, version string) string {
	return resolveImageFromSpec(imageSpec, "k8s.gcr.io", "kube-proxy", version)
}

func ResolveCoreDNSImage(imageSpec *v1alpha1.ImageSpec) string {
	return resolveImageFromSpec(imageSpec, "registry.k8s.io", "coredns/coredns", "v1.12.0")
}

func ResolveNginxImage(imageSpec *v1alpha1.ImageSpec) string {
	return resolveImageFromSpec(imageSpec, "docker.io", "nginx", "1.29.1")
}
