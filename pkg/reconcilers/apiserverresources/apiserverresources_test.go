package apiserverresources

import (
	"testing"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestApiServerResourcesReconciler_extractAdditionalVolumesAndMounts(t *testing.T) {
	reconciler := &apiServerResourcesReconciler{}

	hostedControlPlane := &v1alpha1.HostedControlPlane{
		Spec: v1alpha1.HostedControlPlaneSpec{
			HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
				Deployment: v1alpha1.HostedControlPlaneDeployment{
					APIServer: v1alpha1.APIServerPod{
						Mounts: map[string]v1alpha1.HostedControlPlaneMount{
							"custom-config": {
								Path: "/etc/custom",
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "custom-config-map",
									},
								},
							},
							"custom-secret": {
								Path: "/etc/secret",
								Secret: &corev1.SecretVolumeSource{
									SecretName: "custom-secret",
								},
							},
						},
					},
				},
			},
		},
	}

	volumes, volumeMounts := reconciler.extractAdditionalVolumesAndMounts(
		hostedControlPlane.Spec.Deployment.APIServer.Mounts,
	)

	if len(volumes) != 2 {
		t.Errorf("expected 2 additional volumes, got %d", len(volumes))
	}

	if len(volumeMounts) != 2 {
		t.Errorf("expected 2 additional volume mounts, got %d", len(volumeMounts))
	}

	// Create maps for easier verification
	volumeMap := make(map[string]*corev1ac.VolumeApplyConfiguration)
	for _, vol := range volumes {
		volumeMap[*vol.Name] = vol
	}

	mountMap := make(map[string]*corev1ac.VolumeMountApplyConfiguration)
	for _, mount := range volumeMounts {
		mountMap[*mount.Name] = mount
	}

	// Verify custom-config volume and mount
	if configVolume, exists := volumeMap["custom-config"]; !exists {
		t.Error("expected custom-config volume not found")
	} else {
		if configVolume.ConfigMap == nil {
			t.Error("expected custom-config volume to be a ConfigMap volume")
		} else if *configVolume.ConfigMap.Name != "custom-config-map" {
			t.Errorf("expected ConfigMap name 'custom-config-map', got %s", *configVolume.ConfigMap.Name)
		}
	}

	if configMount, exists := mountMap["custom-config"]; !exists {
		t.Error("expected custom-config volume mount not found")
	} else if *configMount.MountPath != "/etc/custom" {
		t.Errorf("expected mount path '/etc/custom', got %s", *configMount.MountPath)
	}

	// Verify custom-secret volume and mount
	if secretVolume, exists := volumeMap["custom-secret"]; !exists {
		t.Error("expected custom-secret volume not found")
	} else {
		if secretVolume.Secret == nil {
			t.Error("expected custom-secret volume to be a Secret volume")
		} else if *secretVolume.Secret.SecretName != "custom-secret" {
			t.Errorf("expected secret name 'custom-secret', got %s", *secretVolume.Secret.SecretName)
		}
	}

	if secretMount, exists := mountMap["custom-secret"]; !exists {
		t.Error("expected custom-secret volume mount not found")
	} else if *secretMount.MountPath != "/etc/secret" {
		t.Errorf("expected mount path '/etc/secret', got %s", *secretMount.MountPath)
	}
}

func TestApiServerResourcesReconciler_ResourceLifecycle_MountConfiguration(t *testing.T) {
	reconciler := &apiServerResourcesReconciler{}

	tests := []struct {
		name                string
		mounts              map[string]v1alpha1.HostedControlPlaneMount
		expectedVolumeCount int
		expectedMountCount  int
		description         string
	}{
		{
			name:                "empty mounts - should handle gracefully",
			mounts:              map[string]v1alpha1.HostedControlPlaneMount{},
			expectedVolumeCount: 0,
			expectedMountCount:  0,
			description:         "Empty mount configuration should not create volumes",
		},
		{
			name: "mixed mount types - should handle all types",
			mounts: map[string]v1alpha1.HostedControlPlaneMount{
				"config-mount": {
					Path: "/etc/config",
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "my-config",
						},
					},
				},
				"secret-mount": {
					Path: "/etc/secret",
					Secret: &corev1.SecretVolumeSource{
						SecretName: "my-secret",
					},
				},
			},
			expectedVolumeCount: 2,
			expectedMountCount:  2,
			description:         "Should handle ConfigMap and Secret mount types correctly",
		},
		{
			name: "mount with invalid path - should still extract resources",
			mounts: map[string]v1alpha1.HostedControlPlaneMount{
				"weird-mount": {
					Path: "", // Empty path
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "config",
						},
					},
				},
			},
			expectedVolumeCount: 1,
			expectedMountCount:  1,
			description:         "Should handle mounts with invalid paths without failing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volumes, mounts := reconciler.extractAdditionalVolumesAndMounts(tt.mounts)

			if len(volumes) != tt.expectedVolumeCount {
				t.Errorf("%s: expected %d volumes, got %d",
					tt.description, tt.expectedVolumeCount, len(volumes))
			}

			if len(mounts) != tt.expectedMountCount {
				t.Errorf("%s: expected %d mounts, got %d",
					tt.description, tt.expectedMountCount, len(mounts))
			}

			// Verify volume and mount correlation
			if len(volumes) != len(mounts) {
				t.Errorf("%s: volume count (%d) should match mount count (%d)",
					tt.description, len(volumes), len(mounts))
			}

			// Verify each volume has corresponding mount
			volumeNames := make(map[string]bool)
			for _, vol := range volumes {
				volumeNames[*vol.Name] = true
			}

			for _, mount := range mounts {
				if !volumeNames[*mount.Name] {
					t.Errorf("%s: mount references non-existent volume: %s",
						tt.description, *mount.Name)
				}
			}
		})
	}
}
