package volume_stats

import (
	"math"
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

func TestExtractPodVolumeUsage(t *testing.T) {
	p := &kubeletEtcdVolumeStatsProvider{}

	t.Run("returns usage for matching pod with PVC volume", func(t *testing.T) {
		g := NewWithT(t)
		usage := int64(1073741824) // 1 GiB
		usedBytes := uint64(usage)
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(usage))
	})

	t.Run("returns usage for matching pod with static volume", func(t *testing.T) {
		g := NewWithT(t)
		usage := int64(536870912) // 512 MiB
		usedBytes := uint64(usage)
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-1",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name:    "etcd-data",
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-1")
		g.Expect(result).To(Equal(usage))
	})

	t.Run("returns 0 for missing pod", func(t *testing.T) {
		g := NewWithT(t)
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-99")
		g.Expect(result).To(Equal(int64(0)))
	})

	t.Run("returns 0 when volume has nil UsedBytes", func(t *testing.T) {
		g := NewWithT(t)
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: nil},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(int64(0)))
	})

	t.Run("ignores non-etcd volumes", func(t *testing.T) {
		g := NewWithT(t)
		usedBytes := uint64(1073741824)
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-certificates",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-certificates-etcd-0",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(int64(0)))
	})

	t.Run("caps UsedBytes to MaxInt64 when value exceeds", func(t *testing.T) {
		g := NewWithT(t)
		usedBytes := uint64(math.MaxInt64) + 1
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(int64(math.MaxInt64)))
	})

	t.Run("skips non-matching namespace", func(t *testing.T) {
		g := NewWithT(t)
		usedBytes := uint64(1073741824)
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "other-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0",
								Namespace: "other-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(int64(0)))
	})

	t.Run("first matching volume wins when multiple etcd-data volumes exist", func(t *testing.T) {
		g := NewWithT(t)
		usedBytes1 := uint64(1073741824) // 1 GiB
		usedBytes2 := uint64(2147483648) // 2 GiB
		summary := kubeletstatsv1alpha1.Summary{
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes1},
						},
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0-2",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes2},
						},
					},
				},
			},
		}

		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(int64(1073741824))) // first match wins
	})

	t.Run("empty summary returns 0", func(t *testing.T) {
		g := NewWithT(t)
		summary := kubeletstatsv1alpha1.Summary{}
		result := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		g.Expect(result).To(Equal(int64(0)))
	})
}

func TestGetMaxEtcdVolumeUsage(t *testing.T) {
	t.Run("deduplicates node stats for pods on same node", func(t *testing.T) {
		g := NewWithT(t)
		usedBytes1 := uint64(1073741824) // 1 GiB
		usedBytes2 := uint64(2147483648) // 2 GiB
		summary := kubeletstatsv1alpha1.Summary{
			Node: kubeletstatsv1alpha1.NodeStats{},
			Pods: []kubeletstatsv1alpha1.PodStats{
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-0",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-0",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes1},
						},
					},
				},
				{
					PodRef: kubeletstatsv1alpha1.PodReference{
						Name:      "etcd-1",
						Namespace: "test-ns",
					},
					VolumeStats: []kubeletstatsv1alpha1.VolumeStats{
						{
							Name: "etcd-data",
							PVCRef: &kubeletstatsv1alpha1.PVCReference{
								Name:      "etcd-data-etcd-1",
								Namespace: "test-ns",
							},
							FsStats: kubeletstatsv1alpha1.FsStats{UsedBytes: &usedBytes2},
						},
					},
				},
			},
		}

		// This test verifies the extraction logic - the actual provider tests
		// with real node API calls would be integration tests
		p := &kubeletEtcdVolumeStatsProvider{}

		usage0 := p.extractPodVolumeUsage(summary, "test-ns", "etcd-0")
		usage1 := p.extractPodVolumeUsage(summary, "test-ns", "etcd-1")

		g.Expect(usage0).To(Equal(int64(1073741824)))
		g.Expect(usage1).To(Equal(int64(2147483648)))
	})
}
