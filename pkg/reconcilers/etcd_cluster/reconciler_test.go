package etcd_cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/etcd_cluster/s3_client"
	"k8s.io/utils/ptr"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	semver "github.com/blang/semver/v4"
	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestEtcdClusterReconciler_getETCDVolumeSize(t *testing.T) {
	tests := []struct {
		name                       string
		hostedControlPlane         *v1alpha1.HostedControlPlane
		etcdServerStorageBuffer    resource.Quantity
		etcdServerStorageIncrement resource.Quantity
		expectedSize               resource.Quantity
	}{
		{
			name: "autogrow enabled with sufficient space",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
				Status: v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize:  resource.MustParse("20Gi"),
					ETCDVolumeUsage: resource.MustParse("15Gi"), // 5Gi free
				},
			},
			etcdServerStorageBuffer:    resource.MustParse("2Gi"), // Buffer requirement
			etcdServerStorageIncrement: resource.MustParse("10Gi"),
			expectedSize:               resource.MustParse("20Gi"), // No growth needed
		},
		{
			name: "autogrow enabled needs more space",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
				Status: v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize:  resource.MustParse("20Gi"),
					ETCDVolumeUsage: resource.MustParse("19Gi"), // 1Gi free, less than buffer
				},
			},
			etcdServerStorageBuffer:    resource.MustParse("2Gi"), // Buffer requirement
			etcdServerStorageIncrement: resource.MustParse("10Gi"),
			expectedSize:               resource.MustParse("30Gi"), // Should grow
		},
		{
			name: "autogrow disabled uses specified size",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow:   ptr.To(false),
							VolumeSize: ptr.To(resource.MustParse("25Gi")),
						},
					},
				},
				Status: v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize:  resource.MustParse("20Gi"), // Current size
					ETCDVolumeUsage: resource.MustParse("19Gi"),
				},
			},
			etcdServerStorageBuffer:    resource.MustParse("2Gi"),
			etcdServerStorageIncrement: resource.MustParse("10Gi"),
			expectedSize:               resource.MustParse("25Gi"), // Uses spec size
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			returningFakeRecorder, rec := recorder.NewInfiniteReturningFakeRecorder(tt.hostedControlPlane)
			reconciler := &etcdClusterReconciler{
				recorder:                   rec,
				etcdServerStorageBuffer:    tt.etcdServerStorageBuffer,
				etcdServerStorageIncrement: tt.etcdServerStorageIncrement,
			}

			result := reconciler.getETCDVolumeSize(tt.hostedControlPlane)

			g.Expect(result.Cmp(tt.expectedSize)).To(Equal(0))

			if tt.hostedControlPlane.Spec.ETCD.AutoGrow != nil && *tt.hostedControlPlane.Spec.ETCD.AutoGrow &&
				result.Cmp(tt.hostedControlPlane.Status.ETCDVolumeSize) > 0 {
				g.Expect(returningFakeRecorder.Events).To(ContainElement(
					ContainSubstring(etcdVolumeSizeReCalculatedEvent),
				))
			}
		})
	}
}

func TestEtcdClusterReconciler_ErrorHandling_InvalidVolumeData(t *testing.T) {
	tests := []struct {
		name                string
		hostedControlPlane  *v1alpha1.HostedControlPlane
		expectPanicRecovery bool
		expectedVolumeSize  resource.Quantity
		description         string
	}{
		{
			name: "negative volume usage - should handle gracefully",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
				Status: v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize:  resource.MustParse("20Gi"),
					ETCDVolumeUsage: resource.MustParse("-5Gi"), // Invalid negative value
				},
			},
			expectedVolumeSize: resource.MustParse("20Gi"), // Should not grow with invalid data
			description:        "Should handle negative volume usage gracefully",
		},
		{
			name: "zero volume size with autogrow - edge case handling",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
				Status: v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize:  resource.Quantity{}, // Zero value
					ETCDVolumeUsage: resource.MustParse("5Gi"),
				},
			},
			expectedVolumeSize: resource.MustParse("10Gi"), // Should default to minimum increment
			description:        "Should handle zero current volume size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			defer func() {
				if r := recover(); r != nil && !tt.expectPanicRecovery {
					g.Expect(r).To(BeNil())
				}
			}()

			reconciler := &etcdClusterReconciler{
				recorder:                   &recorder.InfiniteDiscardingFakeRecorder{},
				etcdServerStorageBuffer:    resource.MustParse("2Gi"),
				etcdServerStorageIncrement: resource.MustParse("10Gi"),
			}
			result := reconciler.getETCDVolumeSize(tt.hostedControlPlane)

			g.Expect(result.Cmp(tt.expectedVolumeSize)).
				To(Equal(0))
		})
	}
}

func TestEtcdClusterReconciler_StateTransitions_AutoGrowDecisionLogic(t *testing.T) {
	tests := []struct {
		name           string
		currentSize    string
		currentUsage   string
		expectedSize   string
		expectedGrowth bool
		description    string
	}{
		{
			name:           "just below threshold - should trigger growth",
			currentSize:    "20Gi",
			currentUsage:   "17.1Gi", // 20 - 17.1 = 2.9Gi free, less than 3Gi buffer
			expectedSize:   "30Gi",
			expectedGrowth: true,
			description:    "Should grow when free space is less than buffer requirement",
		},
		{
			name:           "at threshold - should not grow",
			currentSize:    "20Gi",
			currentUsage:   "17Gi", // 20-17 = 3Gi free, equal to buffer
			expectedSize:   "20Gi",
			expectedGrowth: false,
			description:    "Should not grow when free space equals buffer requirement",
		},
		{
			name:           "well below threshold - should trigger growth",
			currentSize:    "20Gi",
			currentUsage:   "19Gi", // 20-19 = 1Gi free, well below buffer
			expectedSize:   "30Gi",
			expectedGrowth: true,
			description:    "Should grow when free space is well below buffer",
		},
		{
			name:           "massive usage spike - should grow by increment only",
			currentSize:    "20Gi",
			currentUsage:   "19.5Gi", // Nearly full
			expectedSize:   "30Gi",   // Should grow by a single increment, not double
			expectedGrowth: true,
			description:    "Should grow by single increment regardless of usage spike size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			returningFakeRecorder, fakeRecorder := recorder.NewInfiniteReturningFakeRecorder()
			reconciler := &etcdClusterReconciler{
				recorder:                   fakeRecorder,
				etcdServerStorageBuffer:    resource.MustParse("3Gi"),
				etcdServerStorageIncrement: resource.MustParse("10Gi"),
			}

			hcp := &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: v1alpha1.ETCDComponent{
							AutoGrow: ptr.To(true),
						},
					},
				},
				Status: v1alpha1.HostedControlPlaneStatus{
					ETCDVolumeSize:  resource.MustParse(tt.currentSize),
					ETCDVolumeUsage: resource.MustParse(tt.currentUsage),
				},
			}

			result := reconciler.getETCDVolumeSize(hcp)
			expectedQuantity := resource.MustParse(tt.expectedSize)

			g.Expect(result.Cmp(expectedQuantity)).To(Equal(0))

			if tt.expectedGrowth {
				g.Expect(returningFakeRecorder.Events).To(ContainElement(
					ContainSubstring(etcdVolumeSizeReCalculatedEvent),
				))
			} else {
				g.Expect(returningFakeRecorder.Events).ToNot(ContainElement(
					ContainSubstring(etcdVolumeSizeReCalculatedEvent),
				))
			}
		})
	}
}

func TestEtcdClusterReconciler_reconcileETCDSpaceUsage(t *testing.T) {
	ctx := context.Background()

	t.Run("should update volume usage from filesystem stats", func(t *testing.T) {
		g := NewWithT(t)
		fsUsage := int64(5368709120) // 5 GiB

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("0"),
			},
		}

		volumeStub := NewEtcdVolumeStatsProviderStub()
		volumeStub.MaxUsage = fsUsage

		reconciler := &etcdClusterReconciler{
			recorder:            &recorder.InfiniteDiscardingFakeRecorder{},
			volumeStatsProvider: volumeStub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ETCDVolumeUsage).To(EqualResource(*resource.NewQuantity(fsUsage, resource.BinarySI)))
	})

	t.Run("should use filesystem usage when it exceeds previous", func(t *testing.T) {
		g := NewWithT(t)
		fsUsage := int64(5 * 1024 * 1024 * 1024)

		volumeStub := NewEtcdVolumeStatsProviderStub()
		volumeStub.MaxUsage = fsUsage

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("2Gi"),
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:            &recorder.InfiniteDiscardingFakeRecorder{},
			volumeStatsProvider: volumeStub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ETCDVolumeUsage).To(EqualResource(*resource.NewQuantity(fsUsage, resource.BinarySI)))
	})

	t.Run("should not shrink volume usage", func(t *testing.T) {
		g := NewWithT(t)
		fsUsage := int64(1 * 1024 * 1024 * 1024)  // 1 GiB
		previous := int64(5 * 1024 * 1024 * 1024) // 5 GiB

		volumeStub := NewEtcdVolumeStatsProviderStub()
		volumeStub.MaxUsage = fsUsage

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("5Gi"),
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:            &recorder.InfiniteDiscardingFakeRecorder{},
			volumeStatsProvider: volumeStub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ETCDVolumeUsage).To(EqualResource(*resource.NewQuantity(previous, resource.BinarySI)))
	})

	t.Run("should log warning and continue when volume stats fails", func(t *testing.T) {
		g := NewWithT(t)

		volumeStub := NewEtcdVolumeStatsProviderStub()
		volumeStub.Error = errors.New("connection refused")

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("15Gi"),
			},
		}

		returningFakeRecorder, rec := recorder.NewInfiniteReturningFakeRecorder()

		reconciler := &etcdClusterReconciler{
			recorder:            rec,
			volumeStatsProvider: volumeStub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(returningFakeRecorder.Events).To(ContainElement(ContainSubstring("connection refused")))
	})

	t.Run("should not shrink when stats fail", func(t *testing.T) {
		g := NewWithT(t)

		volumeStub := NewEtcdVolumeStatsProviderStub()
		volumeStub.Error = errors.New("connection refused")

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("15Gi"),
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:            &recorder.InfiniteDiscardingFakeRecorder{},
			volumeStatsProvider: volumeStub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		// Should retain previous value since new measurement failed (returns 0)
		g.Expect(hcp.Status.ETCDVolumeUsage).
			To(EqualResource(*resource.NewQuantity(int64(15*1024*1024*1024), resource.BinarySI)))
	})
}

func TestEtcdClusterReconciler_etcdIsHealthy(t *testing.T) {
	ctx := context.Background()

	t.Run("should handle etcd alarm errors", func(t *testing.T) {
		g := NewWithT(t)
		stub := NewEtcdClientStub()
		stub.AlarmError = errors.New("failed to list alarms")

		hcp := &v1alpha1.HostedControlPlane{}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
		}

		err := reconciler.etcdIsHealthy(ctx, stub, hcp)

		g.Expect(err).To(MatchError(ContainSubstring("failed to list alarms")))
	})

	t.Run("should disarm NOSPACE alarms when autogrow is enabled", func(t *testing.T) {
		g := NewWithT(t)
		stub := NewEtcdClientStub()
		stub.ActiveAlarms = []*etcdserverpb.AlarmMember{
			{
				MemberID: 12345,
				Alarm:    etcdserverpb.AlarmType_NOSPACE,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						AutoGrow: ptr.To(true),
					},
				},
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
		}

		err := reconciler.etcdIsHealthy(ctx, stub, hcp)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(stub.ActiveAlarms).ToNot(ContainElement(
			HaveField("Alarm", etcdserverpb.AlarmType_NOSPACE),
		))
	})

	t.Run("should return error for active NOSPACE alarms when autogrow is disabled", func(t *testing.T) {
		g := NewWithT(t)
		stub := NewEtcdClientStub()
		stub.ActiveAlarms = []*etcdserverpb.AlarmMember{
			{
				MemberID: 12345,
				Alarm:    etcdserverpb.AlarmType_NOSPACE,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						AutoGrow: ptr.To(false),
					},
				},
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
		}

		err := reconciler.etcdIsHealthy(ctx, stub, hcp)

		g.Expect(err).To(MatchError(ContainSubstring(etcdserverpb.AlarmType_NOSPACE.String())))
		g.Expect(stub.ActiveAlarms).To(ContainElement(
			HaveField("Alarm", etcdserverpb.AlarmType_NOSPACE),
		))
	})

	t.Run("should return error for active non-NOSPACE alarms", func(t *testing.T) {
		g := NewWithT(t)
		stub := NewEtcdClientStub()
		stub.ActiveAlarms = []*etcdserverpb.AlarmMember{
			{
				MemberID: 12345,
				Alarm:    etcdserverpb.AlarmType_CORRUPT,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						AutoGrow: ptr.To(true),
					},
				},
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
		}

		err := reconciler.etcdIsHealthy(ctx, stub, hcp)

		g.Expect(err).To(MatchError(ContainSubstring(etcdserverpb.AlarmType_CORRUPT.String())))
	})

	t.Run("should pass when no alarms present", func(t *testing.T) {
		g := NewWithT(t)
		stub := NewEtcdClientStub()

		hcp := &v1alpha1.HostedControlPlane{}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
		}

		err := reconciler.etcdIsHealthy(ctx, stub, hcp)

		g.Expect(err).NotTo(HaveOccurred())
	})
}

func TestEtcdClusterReconciler_reconcileETCDBackup(t *testing.T) {
	ctx := context.Background()

	yesterday := metav1.Time{Time: time.Now().Add(-25 * time.Hour)}
	cronAt2AM := "0 2 * * *"
	t.Run("should create snapshot and upload to S3 when scheduled", func(t *testing.T) {
		g := NewWithT(t)
		etcdClientStub := NewEtcdClientStub()
		s3ClientStub := NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: cronAt2AM,
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: yesterday, // Last backup was 25 hours ago
			},
		}

		returningFakeRecorder, fakeRecorder := recorder.NewInfiniteReturningFakeRecorder(hcp)
		reconciler := &etcdClusterReconciler{
			recorder:          fakeRecorder,
			etcdClientFactory: nil,
			s3ClientFactory: func(
				context.Context, *alias.ManagementClusterClient,
				*v1alpha1.HostedControlPlane, *capiv2.Cluster,
			) (s3_client.S3Client, error) {
				return s3ClientStub, nil
			},
		}

		err := reconciler.reconcileETCDBackup(ctx, etcdClientStub, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(s3ClientStub.LastUploadedBody).To(Equal(EtcdSnapshotData))
		g.Expect(hcp.Status.ETCDLastBackupTime).NotTo(BeZero())
		g.Expect(hcp.Status.ETCDNextBackupTime).NotTo(BeZero())

		g.Expect(returningFakeRecorder.Events).To(ContainElement(
			And(
				ContainSubstring("EtcdBackup"),
				ContainSubstring("Created etcd backup"),
			),
		))
	})

	t.Run("should not create backup when not scheduled", func(t *testing.T) {
		g := NewWithT(t)
		etcdClientStub := NewEtcdClientStub()
		s3ClientStub := NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: cronAt2AM,
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: metav1.Time{Time: time.Now().Add(-1 * time.Hour)}, // Recent backup
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
			s3ClientFactory: func(
				context.Context, *alias.ManagementClusterClient,
				*v1alpha1.HostedControlPlane, *capiv2.Cluster,
			) (s3_client.S3Client, error) {
				return s3ClientStub, nil
			},
		}

		err := reconciler.reconcileETCDBackup(ctx, etcdClientStub, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should handle etcd snapshot creation failure", func(t *testing.T) {
		g := NewWithT(t)
		etcdClientStub := NewEtcdClientStub()
		etcdClientStub.SnapshotError = errors.New("failed to create snapshot")
		s3ClientStub := NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: cronAt2AM,
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: yesterday, // Last backup was 25 hours ago
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
			s3ClientFactory: func(
				context.Context, *alias.ManagementClusterClient,
				*v1alpha1.HostedControlPlane, *capiv2.Cluster,
			) (s3_client.S3Client, error) {
				return s3ClientStub, nil
			},
		}

		err := reconciler.reconcileETCDBackup(ctx, etcdClientStub, hcp, nil)

		g.Expect(err).To(MatchError(ContainSubstring("failed to create etcd snapshot")))
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should handle S3 upload failure", func(t *testing.T) {
		g := NewWithT(t)
		etcdClientStub := NewEtcdClientStub()
		s3ClientStub := NewS3ClientStub()
		s3ClientStub.UploadError = errors.New("failed to upload to S3")

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: cronAt2AM,
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: yesterday, // Last backup was 25 hours ago
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
			s3ClientFactory: func(
				context.Context, *alias.ManagementClusterClient,
				*v1alpha1.HostedControlPlane, *capiv2.Cluster,
			) (s3_client.S3Client, error) {
				return s3ClientStub, nil
			},
		}

		err := reconciler.reconcileETCDBackup(ctx, etcdClientStub, hcp, nil)

		g.Expect(err).To(MatchError(ContainSubstring("failed to upload etcd snapshot to S3")))
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should handle invalid cron schedule", func(t *testing.T) {
		g := NewWithT(t)
		etcdClientStub := NewEtcdClientStub()
		s3ClientStub := NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: "invalid cron", // Invalid schedule
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: yesterday,
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
			s3ClientFactory: func(
				context.Context, *alias.ManagementClusterClient,
				*v1alpha1.HostedControlPlane, *capiv2.Cluster,
			) (s3_client.S3Client, error) {
				return s3ClientStub, nil
			},
		}

		err := reconciler.reconcileETCDBackup(ctx, etcdClientStub, hcp, nil)

		g.Expect(err).To(MatchError(ContainSubstring("failed to parse etcd backup schedule")))
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should create first backup when ETCDLastBackupTime is zero", func(t *testing.T) {
		g := NewWithT(t)
		etcdClientStub := NewEtcdClientStub()
		s3ClientStub := NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: cronAt2AM,
							Bucket:   "test-backup-bucket",
							Region:   "us-east-1",
							Secret: v1alpha1.ETCDBackupSecret{
								Name:               "etcd-backup-secret",
								Namespace:          ptr.To("default"),
								AccessKeyIDKey:     ptr.To("access-key-id"),
								SecretAccessKeyKey: ptr.To("secret-access-key"),
							},
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: metav1.Time{}, // Zero time - first backup
			},
		}

		reconciler := &etcdClusterReconciler{
			recorder:          &recorder.InfiniteDiscardingFakeRecorder{},
			etcdClientFactory: nil,
			s3ClientFactory: func(
				context.Context, *alias.ManagementClusterClient,
				*v1alpha1.HostedControlPlane, *capiv2.Cluster,
			) (s3_client.S3Client, error) {
				return s3ClientStub, nil
			},
		}

		err := reconciler.reconcileETCDBackup(ctx, etcdClientStub, hcp, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(s3ClientStub.LastUploadedBody).To(Equal(EtcdSnapshotData))
		g.Expect(hcp.Status.ETCDLastBackupTime).NotTo(BeZero())
		g.Expect(hcp.Status.ETCDNextBackupTime).NotTo(BeZero())
	})
}

func TestBuildEtcdArgs_SnapshotCount(t *testing.T) {
	tests := []struct {
		name                string
		etcdVersion         semver.Version
		expectSnapshotCount bool
	}{
		{
			name:                "version < 3.7 sets snapshot-count",
			etcdVersion:         semver.MustParse("3.6.0"),
			expectSnapshotCount: true,
		},
		{
			name:                "version >= 3.7 omits snapshot-count",
			etcdVersion:         semver.MustParse("3.7.0"),
			expectSnapshotCount: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			reconciler := &etcdClusterReconciler{}
			hcp := &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns"},
				Status:     v1alpha1.HostedControlPlaneStatus{ETCDVolumeSize: resource.MustParse("10Gi")},
			}
			cluster := &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-ns"},
			}
			serverPort := corev1ac.ContainerPort().WithContainerPort(2379)
			peerPort := corev1ac.ContainerPort().WithContainerPort(2380)
			metricsPort := corev1ac.ContainerPort().WithContainerPort(2381)
			dataMount := corev1ac.VolumeMount().WithMountPath("/var/lib/etcd")
			certMount := corev1ac.VolumeMount().WithMountPath("/etc/etcd")

			args := reconciler.buildEtcdArgs(
				context.Background(),
				hcp,
				cluster,
				tt.etcdVersion,
				dataMount,
				certMount,
				serverPort,
				peerPort,
				metricsPort,
			)

			if tt.expectSnapshotCount {
				g.Expect(args).To(ContainElement("--snapshot-count=10000"))
			} else {
				g.Expect(args).NotTo(ContainElement(ContainSubstring("snapshot-count")))
			}
		})
	}
}
