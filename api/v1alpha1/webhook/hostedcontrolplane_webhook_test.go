package webhook

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/importcycle"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
					Replicas: new(int32(3)),
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: new(true),
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
					Replicas: new(int32(1)),
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow:   new(false),
							VolumeSize: new(resource.MustParse("20Gi")),
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
							AutoGrow: new(true),
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
							AutoGrow:   new(true),
							VolumeSize: new(resource.MustParse("20Gi")),
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
							AutoGrow: new(false),
						},
					},
				},
			},
			expectErr: true,
			errMsg:    "autoGrow cannot be false when volumeSize is not set",
		},
		{
			name: "internal service account issuer URL as OIDC provider is rejected",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "default"},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{Name: "test-gateway", Namespace: "default"},
						ETCD:    v1alpha1.ETCDComponent{AutoGrow: new(true)},
						OIDCProviders: map[string]v1alpha1.OIDCProvider{
							importcycle.LocalClusterOIDCEndpoint: {
								Audiences: []string{"my-app"},
								ClaimMappings: v1alpha1.OIDCClaimMappings{
									Username: "claims.sub",
								},
							},
						},
					},
				},
			},
			expectErr: true,
			errMsg:    "issuer URL cannot be the management Kubernetes service account issuer",
		},
		{
			name: "external OIDC provider URL is accepted",
			hcp: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "default"},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{Name: "test-gateway", Namespace: "default"},
						ETCD:    v1alpha1.ETCDComponent{AutoGrow: new(true)},
						OIDCProviders: map[string]v1alpha1.OIDCProvider{
							"https://oidc.example.com": {
								Audiences: []string{"my-app"},
								ClaimMappings: v1alpha1.OIDCClaimMappings{
									Username: "claims.sub",
								},
							},
						},
					},
				},
			},
			expectErr: false,
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
						AutoGrow: &autoGrow,
					},
				},
			},
		}

		if volumeSize != "" {
			hcp.Spec.ETCD.VolumeSize = new(resource.MustParse(volumeSize))
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
