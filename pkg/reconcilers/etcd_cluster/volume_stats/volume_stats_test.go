package volume_stats

import (
	"testing"

	. "github.com/onsi/gomega"
	kubeletstatsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

func TestIsEtcdDataVolume(t *testing.T) {
	tests := []struct {
		name     string
		vol      *kubeletstatsv1alpha1.VolumeStats
		expected bool
	}{
		{
			name: "matches PVC with etcd-data prefix",
			vol: &kubeletstatsv1alpha1.VolumeStats{
				Name: "etcd-data",
				PVCRef: &kubeletstatsv1alpha1.PVCReference{
					Name:      "etcd-data-etcd-0",
					Namespace: "default",
				},
			},
			expected: true,
		},
		{
			name: "matches volume name etcd-data without PVC ref",
			vol: &kubeletstatsv1alpha1.VolumeStats{
				Name: "etcd-data",
			},
			expected: true,
		},
		{
			name: "does not match unrelated PVC",
			vol: &kubeletstatsv1alpha1.VolumeStats{
				Name: "some-other-volume",
				PVCRef: &kubeletstatsv1alpha1.PVCReference{
					Name:      "some-other-pvc",
					Namespace: "default",
				},
			},
			expected: false,
		},
		{
			name: "does not match unrelated volume name without PVC ref",
			vol: &kubeletstatsv1alpha1.VolumeStats{
				Name: "etcd-certificates",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isEtcdDataVolume(tt.vol)).To(Equal(tt.expected))
		})
	}
}
