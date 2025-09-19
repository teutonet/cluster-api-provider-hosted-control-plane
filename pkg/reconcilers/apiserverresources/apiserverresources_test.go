package apiserverresources

import (
	"testing"

	. "github.com/onsi/gomega"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestApiServerResourcesReconciler_extractAdditionalVolumesAndMounts(t *testing.T) {
	reconciler := &apiServerResourcesReconciler{}
	g := NewWithT(t)

	configMapMountName := "custom-config"
	configMapMountPath := "/etc/custom"
	configMapName := "custom-config-map"
	secretMountName := "custom-secret"
	secretMountPath := "/etc/secret"
	secretName := "custom-secret"
	hostedControlPlane := &v1alpha1.HostedControlPlane{
		Spec: v1alpha1.HostedControlPlaneSpec{
			HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
				Deployment: v1alpha1.HostedControlPlaneDeployment{
					APIServer: v1alpha1.APIServerPod{
						Mounts: map[string]v1alpha1.HostedControlPlaneMount{
							configMapMountName: {
								Path: configMapMountPath,
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
							secretMountName: {
								Path: secretMountPath,
								Secret: &corev1.SecretVolumeSource{
									SecretName: secretName,
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

	g.Expect(volumes).To(HaveLen(2))

	g.Expect(volumeMounts).To(HaveLen(2))

	volumeMap := slices.SliceToMap(volumes,
		func(vol *corev1ac.VolumeApplyConfiguration) (string, *corev1ac.VolumeApplyConfiguration) {
			return *vol.Name, vol
		},
	)

	mountMap := slices.SliceToMap(volumeMounts,
		func(mount *corev1ac.VolumeMountApplyConfiguration) (string, *corev1ac.VolumeMountApplyConfiguration) {
			return *mount.Name, mount
		},
	)

	configVolume, exists := volumeMap[configMapMountName]
	g.Expect(exists).To(BeTrue())

	g.Expect(configVolume.ConfigMap).NotTo(BeNil())
	g.Expect(*configVolume.ConfigMap.Name).To(Equal(configMapName))

	configMount, exists := mountMap[configMapMountName]
	g.Expect(exists).To(BeTrue())

	g.Expect(*configMount.MountPath).To(Equal(configMapMountPath))

	secretVolume, exists := volumeMap[secretMountName]
	g.Expect(exists).To(BeTrue())

	g.Expect(secretVolume.Secret).NotTo(BeNil())
	g.Expect(*secretVolume.Secret.SecretName).To(Equal(secretName))

	secretMount, exists := mountMap[secretMountName]
	g.Expect(exists).To(BeTrue())

	g.Expect(secretMount).NotTo(BeNil())
	g.Expect(*secretMount.MountPath).To(Equal(secretMountPath))
}

func TestApiServerResourcesReconciler_ResourceLifecycle_MountConfiguration(t *testing.T) {
	reconciler := &apiServerResourcesReconciler{}
	g := NewWithT(t)

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

			g.Expect(volumes).To(HaveLen(tt.expectedVolumeCount))

			g.Expect(mounts).To(HaveLen(tt.expectedMountCount))

			g.Expect(volumes).To(HaveLen(len(mounts)))

			volumeNames := make(map[string]bool)
			for _, vol := range volumes {
				volumeNames[*vol.Name] = true
			}

			for _, mount := range mounts {
				g.Expect(volumeNames).To(HaveKey(*mount.Name))
			}
		})
	}
}
