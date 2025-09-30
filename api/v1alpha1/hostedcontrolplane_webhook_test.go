package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func TestHostedControlPlaneWebhook_ValidateCreate(t *testing.T) {
	webhook := &hostedControlPlaneWebhook{}

	tests := []struct {
		name      string
		hcp       *HostedControlPlane
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid hosted control plane with autogrow enabled",
			hcp: &HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: HostedControlPlaneSpec{
					Version:  "v1.28.0",
					Replicas: ptr.To[int32](3),
					HostedControlPlaneInlineSpec: HostedControlPlaneInlineSpec{
						Gateway: GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: ETCDComponent{
							AutoGrow: true,
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "valid hosted control plane with volume size specified",
			hcp: &HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: HostedControlPlaneSpec{
					Version:  "1.28.0",
					Replicas: ptr.To[int32](1),
					HostedControlPlaneInlineSpec: HostedControlPlaneInlineSpec{
						Gateway: GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: ETCDComponent{
							AutoGrow:   false,
							VolumeSize: ptr.To(resource.MustParse("20Gi")),
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "invalid version format",
			hcp: &HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: HostedControlPlaneSpec{
					Version: "invalid-version",
					HostedControlPlaneInlineSpec: HostedControlPlaneInlineSpec{
						Gateway: GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: ETCDComponent{
							AutoGrow: true,
						},
					},
				},
			},
			expectErr: true,
			errMsg:    "version must be a valid semantic version",
		},
		{
			name: "autogrow enabled with volume size specified - should fail",
			hcp: &HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: HostedControlPlaneInlineSpec{
						Gateway: GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: ETCDComponent{
							AutoGrow:   true,
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
			hcp: &HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				Spec: HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: HostedControlPlaneInlineSpec{
						Gateway: GatewayReference{
							Name:      "test-gateway",
							Namespace: "default",
						},
						ETCD: ETCDComponent{
							AutoGrow: false,
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

	createValidHCP := func(version string, autoGrow bool, volumeSize string) *HostedControlPlane {
		hcp := &HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "default",
			},
			Spec: HostedControlPlaneSpec{
				Version: version,
				HostedControlPlaneInlineSpec: HostedControlPlaneInlineSpec{
					Gateway: GatewayReference{
						Name:      "test-gateway",
						Namespace: "default",
					},
					ETCD: ETCDComponent{
						AutoGrow: autoGrow,
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
		oldHCP    *HostedControlPlane
		newHCP    *HostedControlPlane
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
			oldHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "20Gi")
				return hcp
			}(),
			newHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "30Gi")
				return hcp
			}(),
			expectErr: false,
		},
		{
			name: "invalid volume size decrease",
			oldHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "30Gi")
				return hcp
			}(),
			newHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "20Gi")
				return hcp
			}(),
			expectErr: true,
			errMsg:    "volume size cannot be decreased",
		},
		{
			name: "valid transition from autogrow to fixed size",
			oldHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", true, "")
				hcp.Status = HostedControlPlaneStatus{
					ETCDVolumeSize: resource.MustParse("25Gi"),
				}
				return hcp
			}(),
			newHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "30Gi")
				return hcp
			}(),
			expectErr: false,
		},
		{
			name: "invalid transition from autogrow to smaller fixed size",
			oldHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", true, "")
				hcp.Status = HostedControlPlaneStatus{
					ETCDVolumeSize: resource.MustParse("25Gi"),
				}
				return hcp
			}(),
			newHCP: func() *HostedControlPlane {
				hcp := createValidHCP("v1.28.0", false, "20Gi")
				return hcp
			}(),
			expectErr: true,
			errMsg:    "volume size cannot be decreased",
		},
		{
			name: "invalid old object version",
			oldHCP: func() *HostedControlPlane {
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

	hcp := &HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "default",
		},
	}

	_, err := webhook.ValidateDelete(t.Context(), hcp)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestHostedControlPlaneWebhook_CastObjectToHostedControlPlane(t *testing.T) {
	webhook := &hostedControlPlaneWebhook{}

	tests := []struct {
		name      string
		obj       runtime.Object
		expectErr bool
	}{
		{
			name: "valid HostedControlPlane object",
			obj: &HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			expectErr: false,
		},
		{
			name:      "invalid object type",
			obj:       &corev1.Pod{}, // Wrong type
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := webhook.castObjectToHostedControlPlane(tt.obj)

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
			hcp := &HostedControlPlane{
				Spec: HostedControlPlaneSpec{
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
