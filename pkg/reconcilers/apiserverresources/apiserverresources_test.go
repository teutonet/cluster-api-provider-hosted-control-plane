package apiserverresources

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes/fake"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/pkg/controller/certificates/rootcacertpublisher"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func TestApiServerResourcesReconciler_extractAdditionalVolumesAndMounts(t *testing.T) {
	g, _, _ := G(t)
	reconciler := &apiServerResourcesReconciler{}

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
						Mounts: map[string]v1alpha1.Mount{
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

	tests := []struct {
		name                string
		mounts              map[string]v1alpha1.Mount
		expectedVolumeCount int
		expectedMountCount  int
		description         string
	}{
		{
			name:                "empty mounts - should handle gracefully",
			mounts:              map[string]v1alpha1.Mount{},
			expectedVolumeCount: 0,
			expectedMountCount:  0,
			description:         "Empty mount configuration should not create volumes",
		},
		{
			name: "mixed mount types - should handle all types",
			mounts: map[string]v1alpha1.Mount{
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
			mounts: map[string]v1alpha1.Mount{
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
			g, _, _ := G(t)
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

func TestReconcileAuthenticationConfig_StableOIDCProviderOrder(t *testing.T) {
	g, _, _ := G(t)

	ctx := context.Background()
	namespace := "test-ns"

	fakeClient := fake.NewClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      rootcacertpublisher.RootCACertConfigMapName,
		},
		Data: map[string]string{
			konstants.CACertName: "fake-root-ca-cert",
		},
	})

	r := &apiServerResourcesReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			ManagementClusterClient: &alias.ManagementClusterClient{Interface: fakeClient},
		},
		authenticationConfigFileName: "config.yaml",
	}

	hcp := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "test-hcp", UID: "test-uid"},
		Spec: v1alpha1.HostedControlPlaneSpec{
			Version: "1.32.0",
			HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
				OIDCProviders: map[string]v1alpha1.OIDCProvider{
					"https://provider-a.example.com": {
						CertificateAuthority: "ca-a",
						ClaimMappings: v1alpha1.OIDCClaimMappings{
							Username: "claims.sub",
							Groups:   "claims.groups",
						},
						Audiences: []string{"aud-a"},
					},
					"https://provider-b.example.com": {
						CertificateAuthority: "ca-b",
						ClaimMappings:        v1alpha1.OIDCClaimMappings{Username: "claims.sub"},
						Audiences:            []string{"aud-b1", "aud-b2"},
					},
					"https://provider-c.example.com": {
						CertificateAuthority: "ca-c",
						ClaimMappings:        v1alpha1.OIDCClaimMappings{Username: `"prefix:" + claims.sub`},
						Audiences:            []string{"aud-c"},
						ClaimValidationRules: []v1alpha1.OIDCClaimValidationRule{
							{Expression: "claims.iss == 'https://provider-c.example.com'", Message: "issuer mismatch"},
						},
					},
					"https://provider-d.example.com": {
						CertificateAuthority: "ca-d",
						ClaimMappings:        v1alpha1.OIDCClaimMappings{Username: "claims.email"},
						Audiences:            []string{"aud-d"},
					},
					"https://provider-e.example.com": {
						CertificateAuthority: "ca-e",
						ClaimMappings:        v1alpha1.OIDCClaimMappings{Username: "claims.sub"},
						Audiences:            []string{"aud-e"},
					},
				},
			},
		},
	}

	cluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "test-cluster"},
	}

	configMapName := names.GetAuthenticationConfigMapName(cluster)

	reconcileAndGetYAML := func(g Gomega) string {
		g.Expect(r.reconcileAuthenticationConfig(ctx, hcp, cluster)).To(Succeed())
		cm, err := fakeClient.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		return cm.Data[r.authenticationConfigFileName]
	}

	firstYAML := reconcileAndGetYAML(g)

	for range 50 {
		g.Expect(reconcileAndGetYAML(g)).To(Equal(firstYAML))
	}
}
