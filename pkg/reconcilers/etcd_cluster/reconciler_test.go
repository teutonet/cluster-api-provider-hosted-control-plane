package etcd_cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

func TestEtcdClusterReconciler_getETCDVolumeSize(t *testing.T) {
	g := NewWithT(t)
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
						ETCD: &v1alpha1.ETCDComponent{
							AutoGrow: true,
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
						ETCD: &v1alpha1.ETCDComponent{
							AutoGrow: true,
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
						ETCD: &v1alpha1.ETCDComponent{
							AutoGrow:   false,
							VolumeSize: resource.MustParse("25Gi"),
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
			recorder := record.NewFakeRecorder(10)
			reconciler := &etcdClusterReconciler{
				recorder:                   recorder,
				etcdServerStorageBuffer:    tt.etcdServerStorageBuffer,
				etcdServerStorageIncrement: tt.etcdServerStorageIncrement,
			}

			result := reconciler.getETCDVolumeSize(tt.hostedControlPlane)

			g.Expect(result.Cmp(tt.expectedSize)).To(Equal(0))

			if tt.hostedControlPlane.Spec.ETCD.AutoGrow && result.Cmp(tt.hostedControlPlane.Status.ETCDVolumeSize) > 0 {
				g.Expect(recorder.Events).To(Receive(
					ContainSubstring(etcdVolumeSizeReCalculatedEvent),
				))
			}
		})
	}
}

func TestEtcdClusterReconciler_ErrorHandling_InvalidVolumeData(t *testing.T) {
	g := NewWithT(t)
	recorder := record.NewFakeRecorder(10)
	reconciler := &etcdClusterReconciler{
		recorder:                   recorder,
		etcdServerStorageBuffer:    resource.MustParse("2Gi"),
		etcdServerStorageIncrement: resource.MustParse("10Gi"),
	}

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
						ETCD: &v1alpha1.ETCDComponent{
							AutoGrow: true,
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
						ETCD: &v1alpha1.ETCDComponent{
							AutoGrow: true,
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
			defer func() {
				if r := recover(); r != nil && !tt.expectPanicRecovery {
					g.Expect(r).To(BeNil())
				}
			}()

			result := reconciler.getETCDVolumeSize(tt.hostedControlPlane)

			g.Expect(result.Cmp(tt.expectedVolumeSize)).
				To(Equal(0))
		})
	}
}

func TestEtcdClusterReconciler_StateTransitions_AutoGrowDecisionLogic(t *testing.T) {
	g := NewWithT(t)
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
			recorder := record.NewFakeRecorder(10)
			reconciler := &etcdClusterReconciler{
				recorder:                   recorder,
				etcdServerStorageBuffer:    resource.MustParse("3Gi"),
				etcdServerStorageIncrement: resource.MustParse("10Gi"),
			}

			hcp := &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						ETCD: &v1alpha1.ETCDComponent{
							AutoGrow: true,
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
				g.Expect(recorder.Events).To(Receive(
					ContainSubstring(etcdVolumeSizeReCalculatedEvent),
				))
			} else {
				g.Expect(recorder.Events).ToNot(Receive(
					ContainSubstring(etcdVolumeSizeReCalculatedEvent),
				))
			}
		})
	}
}

func TestEtcdClusterReconciler_reconcileETCDSpaceUsage(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	t.Run("should handle etcd status error gracefully", func(t *testing.T) {
		stub := test.NewEtcdClientStub()
		stub.StatusError = errors.New("connection refused")

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("15Gi"),
			},
		}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:                   recorder,
			etcdServerStorageBuffer:    resource.MustParse("2Gi"),
			etcdServerStorageIncrement: resource.MustParse("10Gi"),
			etcdClient:                 stub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	t.Run("should update volume usage from etcd status", func(t *testing.T) {
		stub := test.NewEtcdClientStub()
		highestMemberDBSize := int64(5368709120)
		stub.StatusResponses = map[string]*clientv3.StatusResponse{
			"etcd-0": {
				Header:  &etcdserverpb.ResponseHeader{ClusterId: 1},
				Version: "3.5.0",
				DbSize:  highestMemberDBSize,
			},
			"etcd-1": {
				Header:  &etcdserverpb.ResponseHeader{ClusterId: 1},
				Version: "3.5.0",
				DbSize:  highestMemberDBSize / 2,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDVolumeSize:  resource.MustParse("20Gi"),
				ETCDVolumeUsage: resource.MustParse("0"),
			},
		}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:                   recorder,
			etcdServerStorageBuffer:    resource.MustParse("2Gi"),
			etcdServerStorageIncrement: resource.MustParse("10Gi"),
			etcdClient:                 stub,
		}

		err := reconciler.reconcileETCDSpaceUsage(ctx, hcp)

		g.Expect(err).NotTo(HaveOccurred())
		expectedUsage := resource.NewQuantity(highestMemberDBSize, resource.BinarySI)
		expectedUsage.SetScaled(expectedUsage.ScaledValue(resource.Giga), resource.Giga)
		g.Expect(hcp.Status.ETCDVolumeUsage.String()).To(Equal(expectedUsage.String()))
	})
}

func TestEtcdClusterReconciler_etcdIsHealthy(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	t.Run("should handle etcd alarm errors", func(t *testing.T) {
		stub := test.NewEtcdClientStub()
		stub.AlarmError = errors.New("failed to list alarms")

		hcp := &v1alpha1.HostedControlPlane{}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: stub,
		}

		err := reconciler.etcdIsHealthy(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring("failed to list alarms")))
	})

	t.Run("should disarm NOSPACE alarms when autogrow is enabled", func(t *testing.T) {
		stub := test.NewEtcdClientStub()
		stub.ActiveAlarms = []*etcdserverpb.AlarmMember{
			{
				MemberID: 12345,
				Alarm:    etcdserverpb.AlarmType_NOSPACE,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
						AutoGrow: true,
					},
				},
			},
		}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: stub,
		}

		err := reconciler.etcdIsHealthy(ctx, hcp)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(stub.ActiveAlarms).ToNot(ContainElement(
			HaveField("Alarm", etcdserverpb.AlarmType_NOSPACE),
		))
	})

	t.Run("should return error for active NOSPACE alarms when autogrow is disabled", func(t *testing.T) {
		stub := test.NewEtcdClientStub()
		stub.ActiveAlarms = []*etcdserverpb.AlarmMember{
			{
				MemberID: 12345,
				Alarm:    etcdserverpb.AlarmType_NOSPACE,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
						AutoGrow: false,
					},
				},
			},
		}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: stub,
		}

		err := reconciler.etcdIsHealthy(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring(etcdserverpb.AlarmType_NOSPACE.String())))
		g.Expect(stub.ActiveAlarms).To(ContainElement(
			HaveField("Alarm", etcdserverpb.AlarmType_NOSPACE),
		))
	})

	t.Run("should return error for active non-NOSPACE alarms", func(t *testing.T) {
		stub := test.NewEtcdClientStub()
		stub.ActiveAlarms = []*etcdserverpb.AlarmMember{
			{
				MemberID: 12345,
				Alarm:    etcdserverpb.AlarmType_CORRUPT,
			},
		}

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
						AutoGrow: true,
					},
				},
			},
		}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: stub,
		}

		err := reconciler.etcdIsHealthy(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring(etcdserverpb.AlarmType_CORRUPT.String())))
	})

	t.Run("should pass when no alarms present", func(t *testing.T) {
		stub := test.NewEtcdClientStub()

		hcp := &v1alpha1.HostedControlPlane{}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: stub,
		}

		err := reconciler.etcdIsHealthy(ctx, hcp)

		g.Expect(err).NotTo(HaveOccurred())
	})
}

func TestEtcdClusterReconciler_reconcileETCDBackup(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	yesterday := metav1.Time{Time: time.Now().Add(-25 * time.Hour)}
	cronAt2AM := "0 2 * * *"
	t.Run("should create snapshot and upload to S3 when scheduled", func(t *testing.T) {
		etcdClientStub := test.NewEtcdClientStub()
		s3ClientStub := test.NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
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

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: etcdClientStub,
			s3Client:   s3ClientStub,
		}

		err := reconciler.reconcileETCDBackup(ctx, hcp)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(s3ClientStub.LastUploadedBody).To(Equal(test.EtcdSnapshotData))
		g.Expect(hcp.Status.ETCDLastBackupTime).NotTo(BeZero())
		g.Expect(hcp.Status.ETCDNextBackupTime).NotTo(BeZero())

		g.Expect(recorder.Events).To(Receive(
			And(
				ContainSubstring("EtcdBackup"),
				ContainSubstring("Created etcd backup"),
			),
		))
	})

	t.Run("should not create backup when not scheduled", func(t *testing.T) {
		etcdClientStub := test.NewEtcdClientStub()
		s3ClientStub := test.NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
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

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: etcdClientStub,
			s3Client:   s3ClientStub,
		}

		err := reconciler.reconcileETCDBackup(ctx, hcp)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should handle etcd snapshot creation failure", func(t *testing.T) {
		etcdClientStub := test.NewEtcdClientStub()
		etcdClientStub.SnapshotError = errors.New("failed to create snapshot")
		s3ClientStub := test.NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
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

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: etcdClientStub,
			s3Client:   s3ClientStub,
		}

		err := reconciler.reconcileETCDBackup(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring("failed to create etcd snapshot")))
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should handle S3 upload failure", func(t *testing.T) {
		etcdClientStub := test.NewEtcdClientStub()
		s3ClientStub := test.NewS3ClientStub()
		s3ClientStub.UploadError = errors.New("failed to upload to S3")

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
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

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: etcdClientStub,
			s3Client:   s3ClientStub,
		}

		err := reconciler.reconcileETCDBackup(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring("failed to upload etcd snapshot to S3")))
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should handle invalid cron schedule", func(t *testing.T) {
		etcdClientStub := test.NewEtcdClientStub()
		s3ClientStub := test.NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
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

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: etcdClientStub,
			s3Client:   s3ClientStub,
		}

		err := reconciler.reconcileETCDBackup(ctx, hcp)

		g.Expect(err).To(MatchError(ContainSubstring("failed to parse etcd backup schedule")))
		g.Expect(s3ClientStub.LastUploadedBody).To(BeEmpty())
	})

	t.Run("should create first backup when ETCDLastBackupTime is zero", func(t *testing.T) {
		etcdClientStub := test.NewEtcdClientStub()
		s3ClientStub := test.NewS3ClientStub()

		hcp := &v1alpha1.HostedControlPlane{
			Spec: v1alpha1.HostedControlPlaneSpec{
				HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
					ETCD: &v1alpha1.ETCDComponent{
						Backup: &v1alpha1.ETCDBackup{
							Schedule: cronAt2AM,
							Bucket:   "test-backup-bucket",
							Region:   "us-east-1",
							Secret: v1alpha1.ETCDBackupSecret{
								Name:               "etcd-backup-secret",
								Namespace:          "default",
								AccessKeyIDKey:     "access-key-id",
								SecretAccessKeyKey: "secret-access-key",
							},
						},
					},
				},
			},
			Status: v1alpha1.HostedControlPlaneStatus{
				ETCDLastBackupTime: metav1.Time{}, // Zero time - first backup
			},
		}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: etcdClientStub,
			s3Client:   s3ClientStub,
		}

		err := reconciler.reconcileETCDBackup(ctx, hcp)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(s3ClientStub.LastUploadedBody).To(Equal(test.EtcdSnapshotData))
		g.Expect(hcp.Status.ETCDLastBackupTime).NotTo(BeZero())
		g.Expect(hcp.Status.ETCDNextBackupTime).NotTo(BeZero())
	})
}
