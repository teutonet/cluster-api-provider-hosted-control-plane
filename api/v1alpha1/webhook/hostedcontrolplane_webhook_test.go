package webhook

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHostedControlPlaneWebhook_ValidateCreate(t *testing.T) {
	webhook := &hostedControlPlaneWebhook{}

	tests := []struct {
		name      string
		hcp       *v1alpha1.HostedControlPlane
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid hosted control plane with autogrow enabled",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version:  "v1.28.0",
					Replicas: ptr.To[int32](3),
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "valid hosted control plane with volume size specified",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version:  "1.28.0",
					Replicas: ptr.To[int32](1),
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow:   ptr.To(false),
							VolumeSize: ptr.To(resource.MustParse("20Gi")),
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "invalid version format",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "invalid-version",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
			},
			expectErr: true,
			errMsg:    "version must be a valid semantic version",
		},
		{
			name: "autogrow enabled with volume size specified - should fail",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow:   ptr.To(true),
							VolumeSize: ptr.To(resource.MustParse("20Gi")),
						},
					},
				},
			},
			expectErr: true,
			errMsg:    "autoGrow cannot be true when volumeSize is set",
		},
		{
			name: "autogrow disabled without volume size - should fail",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(false),
						},
					},
				},
			},
			expectErr: true,
			errMsg:    "autoGrow cannot be false when volumeSize is not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := webhook.ValidateCreate(t.Context(), tt.hcp)

			if tt.expectErr {
				g.Expect(err).To(MatchError(ContainSubstring(tt.errMsg)))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestHostedControlPlaneWebhook_ValidateUpdate(t *testing.T) {
	webhook := &hostedControlPlaneWebhook{}

	createValidHCP := func(version string, autoGrow bool, volumeSize string) *v1alpha1.HostedControlPlane {
		hcp := &v1alpha1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "default",
			},
			Spec: v1alpha1.HostedControlPlaneSpec{
				Version: version,
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					Gateway: v1alpha1.GatewayReference{
						Name:      "test-gateway",
						Namespace: "default",
					},
					ETCD: v1alpha1.ETCDComponent{
						AutoGrow: ptr.To(autoGrow),
					},
				},
			},
		}

		if volumeSize != "" {
			hcp.Spec.ETCD.VolumeSize = ptr.To(resource.MustParse(volumeSize))
		}

		return hcp
	}

	tests := []struct {
		name      string
		oldHCP    *v1alpha1.HostedControlPlane
		newHCP    *v1alpha1.HostedControlPlane
		expectErr bool
		errMsg    string
	}{
		{
			name:      "valid version upgrade",
			oldHCP:    createValidHCP("v1.28.0", true, ""),
			newHCP:    createValidHCP("v1.28.1", true, ""),
			expectErr: false,
		},
		{
			name:      "invalid version downgrade",
			oldHCP:    createValidHCP("v1.28.1", true, ""),
			newHCP:    createValidHCP("v1.28.0", true, ""),
			expectErr: true,
			errMsg:    "version cannot be decreased",
		},
		{
			name: "valid volume size increase",
			oldHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "20Gi")
				return hcp
			}(),
			newHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "30Gi")
				return hcp
			}(),
			expectErr: false,
		},
		{
			name: "invalid volume size decrease",
			oldHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "30Gi")
				return hcp
			}(),
			newHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "20Gi")
				return hcp
			}(),
			expectErr: true,
			errMsg:    "volume size cannot be decreased",
		},
		{
			name: "valid transition from autogrow to fixed size",
			oldHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", true, "")
				hcp.Status = v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize: resource.MustParse("25Gi"),
				}
				return hcp
			}(),
			newHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "30Gi")
				return hcp
			}(),
			expectErr: false,
		},
		{
			name: "invalid transition from autogrow to smaller fixed size",
			oldHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", true, "")
				hcp.Status = v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize: resource.MustParse("25Gi"),
				}
				return hcp
			}(),
			newHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "20Gi")
				return hcp
			}(),
			expectErr: true,
			errMsg:    "volume size cannot be decreased",
		},
		{
			name: "invalid old object version",
			oldHCP: func() *v1alpha1.HostedControlPlane {
				hcp := createValidHCP("invalid-version", true, "")
				return hcp
			}(),
			newHCP:    createValidHCP("v1.28.0", true, ""),
			expectErr: true,
			errMsg:    "version must be a valid semantic version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := webhook.ValidateUpdate(t.Context(), tt.oldHCP, tt.newHCP)

			if tt.expectErr {
				g.Expect(err).To(MatchError(ContainSubstring(tt.errMsg)))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestHostedControlPlaneWebhook_ValidateDelete(t *testing.T) {
	webhook := &hostedControlPlaneWebhook{}
	g := NewWithT(t)

	hcp := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "default",
		},
	}

	g.Expect(webhook.ValidateDelete(t.Context(), hcp)).Error().NotTo(HaveOccurred())
}

func TestHostedControlPlaneWebhook_ValidateCreate_CustomKubeconfigs(t *testing.T) {
	scheme := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(capiv2.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	cluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	longClusterName := "long-cluster-" + strings.Repeat("a", 240)
	longCluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      longClusterName,
			Namespace: "default",
		},
	}

	clusterOwnerRef := metav1.OwnerReference{
		APIVersion: capiv2.GroupVersion.String(),
		Kind:       "Cluster",
		Name:       cluster.Name,
	}

	baseHCP := func(
		kubeconfigs map[string]v1alpha1.KubeconfigEndpointType,
		ownerRefs ...metav1.OwnerReference,
	) *v1alpha1.HostedControlPlane {
		return &v1alpha1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-hcp",
				Namespace:       "default",
				OwnerReferences: ownerRefs,
			},
			Spec: v1alpha1.HostedControlPlaneSpec{
				Version: "v1.28.0",
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					Gateway: v1alpha1.GatewayReference{
						Name:      "test-gateway",
						Namespace: "default",
					},
					ETCD: v1alpha1.ETCDComponent{
						AutoGrow: ptr.To(true),
					},
					CustomKubeconfigs: kubeconfigs,
				},
			},
		}
	}

	tests := []struct {
		name           string
		hcp            *v1alpha1.HostedControlPlane
		expectErr      bool
		errMsg         string
		expectWarnings bool
	}{
		{
			name: "builtin username admin is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"admin": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "cannot be the same as a default kubeconfig username",
		},
		{
			name: "builtin username kube-controller-manager is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"kube-controller-manager": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "cannot be the same as a default kubeconfig username",
		},
		{
			name: "builtin username kube-scheduler is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"kube-scheduler": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "cannot be the same as a default kubeconfig username",
		},
		{
			name: "builtin username konnectivity-client is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"konnectivity-client": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "cannot be the same as a default kubeconfig username",
		},
		{
			name: "builtin username control-plane-controller is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"control-plane-controller": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "cannot be the same as a default kubeconfig username",
		},
		{
			name: "invalid DNS subdomain uppercase is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"MyUser": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "must be a valid DNS subdomain",
		},
		{
			name: "invalid DNS subdomain starts with dash is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"-invalid": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "must be a valid DNS subdomain",
		},
		{
			name: "invalid DNS subdomain special chars is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"my_user": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "must be a valid DNS subdomain",
		},
		{
			name: "valid simple name is accepted",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"my-user": v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: false,
		},
		{
			name: "cloud provider names are valid",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"cloud-controller-manager":               v1alpha1.KubeconfigEndpointTypeInternal,
				"container-storage-interface-controller": v1alpha1.KubeconfigEndpointTypeInternal,
				"csi-controller":                         v1alpha1.KubeconfigEndpointTypeInternal,
			}, clusterOwnerRef),
			expectErr: false,
		},
		{
			name: "valid name with dots is accepted",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"my.custom.user": v1alpha1.KubeconfigEndpointTypeInternal,
			}, clusterOwnerRef),
			expectErr: false,
		},
		{
			name: "multiple valid names are accepted",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"user-one": v1alpha1.KubeconfigEndpointTypeExternal,
				"user-two": v1alpha1.KubeconfigEndpointTypeInternal,
			}, clusterOwnerRef),
			expectErr: false,
		},
		{
			name: "username too long for DNS subdomain is rejected",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				strings.Repeat("a", 254): v1alpha1.KubeconfigEndpointTypeExternal,
			}, clusterOwnerRef),
			expectErr: true,
			errMsg:    "must be a valid DNS subdomain",
		},
		{
			name: "short username with long cluster name causes invalid secret name",
			hcp: func() *v1alpha1.HostedControlPlane {
				hcp := baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
					"my-user": v1alpha1.KubeconfigEndpointTypeExternal,
				}, metav1.OwnerReference{
					APIVersion: capiv2.GroupVersion.String(),
					Kind:       "Cluster",
					Name:       longClusterName,
				})
				return hcp
			}(),
			expectErr: true,
			errMsg:    "would result in an invalid secret name",
		},
		{
			name: "no owner cluster returns warning",
			hcp: baseHCP(map[string]v1alpha1.KubeconfigEndpointType{
				"my-user": v1alpha1.KubeconfigEndpointTypeExternal,
			}),
			expectErr:      false,
			expectWarnings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, longCluster).
				Build()
			webhook := NewHostedControlPlaneWebhook(fakeClient)

			warnings, err := webhook.ValidateCreate(t.Context(), tt.hcp)

			if tt.expectErr {
				g.Expect(err).To(MatchError(ContainSubstring(tt.errMsg)))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			if tt.expectWarnings {
				g.Expect(warnings).NotTo(BeEmpty())
			}
		})
	}
}

func TestHostedControlPlaneWebhook_ParseVersion(t *testing.T) {
	webhook := &hostedControlPlaneWebhook{}

	tests := []struct {
		name      string
		version   string
		expectErr bool
	}{
		{
			name:      "valid semantic version with v prefix",
			version:   "v1.28.0",
			expectErr: false,
		},
		{
			name:      "valid semantic version without v prefix",
			version:   "1.28.0",
			expectErr: false,
		},
		{
			name:      "valid semantic version with patch",
			version:   "v1.28.5",
			expectErr: false,
		},
		{
			name:      "invalid version format",
			version:   "invalid-version",
			expectErr: true,
		},
		{
			name:      "empty version",
			version:   "",
			expectErr: true,
		},
		{
			name:      "incomplete version",
			version:   "v1.28",
			expectErr: false, // semver.ParseTolerant is tolerant
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: tt.version,
				},
			}

			result, err := webhook.parseVersion(hcp)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(result).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).NotTo(BeNil())
			}
		})
	}
}
