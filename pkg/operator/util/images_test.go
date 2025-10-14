package util

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"k8s.io/utils/ptr"
)

func TestBuildImageString(t *testing.T) {
	tests := []struct {
		name       string
		registry   string
		repository string
		tag        string
		expected   string
	}{
		{
			name:       "with registry",
			registry:   "registry.k8s.io",
			repository: "kube-apiserver",
			tag:        "v1.28.0",
			expected:   "registry.k8s.io/kube-apiserver:v1.28.0",
		},
		{
			name:       "without registry",
			registry:   "",
			repository: "kube-apiserver",
			tag:        "v1.28.0",
			expected:   "kube-apiserver:v1.28.0",
		},
		{
			name:       "with custom registry",
			registry:   "my-registry.com:5000",
			repository: "custom/image",
			tag:        "latest",
			expected:   "my-registry.com:5000/custom/image:latest",
		},
		{
			name:       "with nested repository",
			registry:   "gcr.io",
			repository: "project/app/component",
			tag:        "v2.1.0",
			expected:   "gcr.io/project/app/component:v2.1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(buildImageString(tt.registry, tt.repository, tt.tag)).To(Equal(tt.expected))
		})
	}
}

func TestResolveImageFromSpec(t *testing.T) {
	tests := []struct {
		name              string
		imageSpec         *v1alpha1.ImageSpec
		defaultRegistry   string
		defaultRepository string
		defaultTag        string
		expected          string
	}{
		{
			name:              "nil spec uses all defaults",
			imageSpec:         nil,
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-apiserver",
			defaultTag:        "v1.28.0",
			expected:          "registry.k8s.io/kube-apiserver:v1.28.0",
		},
		{
			name:              "empty spec uses all defaults",
			imageSpec:         &v1alpha1.ImageSpec{},
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-scheduler",
			defaultTag:        "v1.28.0",
			expected:          "registry.k8s.io/kube-scheduler:v1.28.0",
		},
		{
			name: "override registry only",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
			},
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-apiserver",
			defaultTag:        "v1.28.0",
			expected:          "my-registry.com/kube-apiserver:v1.28.0",
		},
		{
			name: "override repository only",
			imageSpec: &v1alpha1.ImageSpec{
				Repository: ptr.To("custom-apiserver"),
			},
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-apiserver",
			defaultTag:        "v1.28.0",
			expected:          "registry.k8s.io/custom-apiserver:v1.28.0",
		},
		{
			name: "override tag only",
			imageSpec: &v1alpha1.ImageSpec{
				Tag: ptr.To("v1.29.0"),
			},
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-apiserver",
			defaultTag:        "v1.28.0",
			expected:          "registry.k8s.io/kube-apiserver:v1.29.0",
		},
		{
			name: "override registry and tag",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
				Tag:      ptr.To("v1.29.0"),
			},
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-apiserver",
			defaultTag:        "v1.28.0",
			expected:          "my-registry.com/kube-apiserver:v1.29.0",
		},
		{
			name: "override all fields",
			imageSpec: &v1alpha1.ImageSpec{
				Registry:   ptr.To("private-registry.com"),
				Repository: ptr.To("custom/kube-apiserver"),
				Tag:        ptr.To("custom-v1.28.0"),
			},
			defaultRegistry:   "registry.k8s.io",
			defaultRepository: "kube-apiserver",
			defaultTag:        "v1.28.0",
			expected:          "private-registry.com/custom/kube-apiserver:custom-v1.28.0",
		},
		{
			name: "empty registry in spec and default",
			imageSpec: &v1alpha1.ImageSpec{
				Repository: ptr.To("my-app"),
				Tag:        ptr.To("v1.0.0"),
			},
			defaultRegistry:   "",
			defaultRepository: "default-app",
			defaultTag:        "latest",
			expected:          "my-app:v1.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(resolveImageFromSpec(tt.imageSpec, tt.defaultRegistry, tt.defaultRepository, tt.defaultTag)).
				To(Equal(tt.expected))
		})
	}
}

func TestResolveKubernetesComponentImage(t *testing.T) {
	tests := []struct {
		name      string
		imageSpec *v1alpha1.ImageSpec
		component string
		version   string
		expected  string
	}{
		{
			name:      "default API server image",
			imageSpec: nil,
			component: "kube-apiserver",
			version:   "v1.28.0",
			expected:  "registry.k8s.io/kube-apiserver:v1.28.0",
		},
		{
			name: "custom registry for API server",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
			},
			component: "kube-apiserver",
			version:   "v1.28.0",
			expected:  "my-registry.com/kube-apiserver:v1.28.0",
		},
		{
			name: "custom tag for scheduler",
			imageSpec: &v1alpha1.ImageSpec{
				Tag: ptr.To("v1.29.0"),
			},
			component: "kube-scheduler",
			version:   "v1.28.0",
			expected:  "registry.k8s.io/kube-scheduler:v1.29.0",
		},
		{
			name: "fully custom image",
			imageSpec: &v1alpha1.ImageSpec{
				Registry:   ptr.To("private.registry.io"),
				Repository: ptr.To("custom-kube-controller-manager"),
				Tag:        ptr.To("custom-v1.28.0"),
			},
			component: "kube-controller-manager",
			version:   "v1.28.0",
			expected:  "private.registry.io/custom-kube-controller-manager:custom-v1.28.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ResolveKubernetesComponentImage(tt.imageSpec, tt.component, tt.version)).To(Equal(tt.expected))
		})
	}
}

func TestResolveETCDImage(t *testing.T) {
	tests := []struct {
		name      string
		imageSpec *v1alpha1.ImageSpec
		version   string
		expected  string
	}{
		{
			name:      "default ETCD image",
			imageSpec: nil,
			version:   "3.5.9",
			expected:  "registry.k8s.io/etcd:3.5.9-0",
		},
		{
			name: "custom registry for ETCD",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
			},
			version:  "3.5.9",
			expected: "my-registry.com/etcd:3.5.9-0",
		},
		{
			name: "custom repository and tag",
			imageSpec: &v1alpha1.ImageSpec{
				Repository: ptr.To("custom-etcd"),
				Tag:        ptr.To("3.5.10-custom"),
			},
			version:  "3.5.9",
			expected: "registry.k8s.io/custom-etcd:3.5.10-custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ResolveETCDImage(tt.imageSpec, tt.version)).To(Equal(tt.expected))
		})
	}
}

func TestResolveKonnectivityImage(t *testing.T) {
	tests := []struct {
		name         string
		imageSpec    *v1alpha1.ImageSpec
		component    string
		minorVersion uint64
		expected     string
	}{
		{
			name:         "default proxy-server image",
			imageSpec:    nil,
			component:    "proxy-server",
			minorVersion: 28,
			expected:     "registry.k8s.io/kas-network-proxy/proxy-server:v0.28.0",
		},
		{
			name:         "default proxy-agent image",
			imageSpec:    nil,
			component:    "proxy-agent",
			minorVersion: 28,
			expected:     "registry.k8s.io/kas-network-proxy/proxy-agent:v0.28.0",
		},
		{
			name: "custom registry for konnectivity",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
			},
			component:    "proxy-server",
			minorVersion: 28,
			expected:     "my-registry.com/kas-network-proxy/proxy-server:v0.28.0",
		},
		{
			name: "custom tag for konnectivity",
			imageSpec: &v1alpha1.ImageSpec{
				Tag: ptr.To("v0.29.0-custom"),
			},
			component:    "proxy-agent",
			minorVersion: 28,
			expected:     "registry.k8s.io/kas-network-proxy/proxy-agent:v0.29.0-custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ResolveKonnectivityImage(tt.imageSpec, tt.component, tt.minorVersion)).To(Equal(tt.expected))
		})
	}
}

func TestResolveKubeProxyImage(t *testing.T) {
	tests := []struct {
		name      string
		imageSpec *v1alpha1.ImageSpec
		version   string
		expected  string
	}{
		{
			name:      "default kube-proxy image",
			imageSpec: nil,
			version:   "v1.28.0",
			expected:  "k8s.gcr.io/kube-proxy:v1.28.0",
		},
		{
			name: "custom registry for kube-proxy",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("registry.k8s.io"),
			},
			version:  "v1.28.0",
			expected: "registry.k8s.io/kube-proxy:v1.28.0",
		},
		{
			name: "custom repository",
			imageSpec: &v1alpha1.ImageSpec{
				Repository: ptr.To("custom-kube-proxy"),
			},
			version:  "v1.28.0",
			expected: "k8s.gcr.io/custom-kube-proxy:v1.28.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ResolveKubeProxyImage(tt.imageSpec, tt.version)).To(Equal(tt.expected))
		})
	}
}

//nolint:dupl // Similar to TestResolveAuditWebhookImage
func TestResolveCoreDNSImage(t *testing.T) {
	tests := []struct {
		name      string
		imageSpec *v1alpha1.ImageSpec
		expected  string
	}{
		{
			name:      "default CoreDNS image",
			imageSpec: nil,
			expected:  "registry.k8s.io/coredns/coredns:v1.12.0",
		},
		{
			name: "custom registry for CoreDNS",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
			},
			expected: "my-registry.com/coredns/coredns:v1.12.0",
		},
		{
			name: "custom tag for CoreDNS",
			imageSpec: &v1alpha1.ImageSpec{
				Tag: ptr.To("v1.13.0"),
			},
			expected: "registry.k8s.io/coredns/coredns:v1.13.0",
		},
		{
			name: "fully custom CoreDNS",
			imageSpec: &v1alpha1.ImageSpec{
				Registry:   ptr.To("private.registry.io"),
				Repository: ptr.To("custom/coredns"),
				Tag:        ptr.To("v1.14.0-custom"),
			},
			expected: "private.registry.io/custom/coredns:v1.14.0-custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ResolveCoreDNSImage(tt.imageSpec)).To(Equal(tt.expected))
		})
	}
}

//nolint:dupl // Similar to TestResolveCoreDNSImage
func TestResolveAuditWebhookImage(t *testing.T) {
	tests := []struct {
		name      string
		imageSpec *v1alpha1.ImageSpec
		expected  string
	}{
		{
			name:      "default audit webhook image",
			imageSpec: nil,
			expected:  "docker.io/nginx:1.29.1",
		},
		{
			name: "custom registry for audit webhook",
			imageSpec: &v1alpha1.ImageSpec{
				Registry: ptr.To("my-registry.com"),
			},
			expected: "my-registry.com/nginx:1.29.1",
		},
		{
			name: "custom tag for audit webhook",
			imageSpec: &v1alpha1.ImageSpec{
				Tag: ptr.To("v1.34.0"),
			},
			expected: "docker.io/nginx:v1.34.0",
		},
		{
			name: "fully custom audit webhook",
			imageSpec: &v1alpha1.ImageSpec{
				Registry:   ptr.To("private.registry.io"),
				Repository: ptr.To("custom/proxy"),
				Tag:        ptr.To("v2.0.0-custom"),
			},
			expected: "private.registry.io/custom/proxy:v2.0.0-custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ResolveNginxImage(tt.imageSpec)).To(Equal(tt.expected))
		})
	}
}
