package etcd_cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/etcd_cluster/s3_client"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

var etcdSnapshotData = "etcd snapshot data"

type etcdClientStub struct {
	// Configurable behavior
	StatusError     error
	SnapshotError   error
	AlarmError      error
	DisarmError     error
	StatusResponses map[string]*clientv3.StatusResponse
	ActiveAlarms    []*etcdserverpb.AlarmMember
}

func newEtcdClientStub() *etcdClientStub {
	return &etcdClientStub{
		StatusResponses: make(map[string]*clientv3.StatusResponse),
		ActiveAlarms:    make([]*etcdserverpb.AlarmMember, 0),
	}
}

func (s *etcdClientStub) GetStatuses(_ context.Context) (map[string]*clientv3.StatusResponse, error) {
	if s.StatusError != nil {
		return nil, s.StatusError
	}
	return s.StatusResponses, nil
}

func (s *etcdClientStub) CreateSnapshot(_ context.Context) (*clientv3.SnapshotResponse, error) {
	if s.SnapshotError != nil {
		return nil, s.SnapshotError
	}
	return &clientv3.SnapshotResponse{
		Header:   &etcdserverpb.ResponseHeader{ClusterId: 1},
		Snapshot: io.NopCloser(bytes.NewReader([]byte(etcdSnapshotData))),
	}, nil
}

func (s *etcdClientStub) ListAlarms(_ context.Context) (*clientv3.AlarmResponse, error) {
	if s.AlarmError != nil {
		return nil, s.AlarmError
	}
	return &clientv3.AlarmResponse{
		Header: &etcdserverpb.ResponseHeader{ClusterId: 1},
		Alarms: s.ActiveAlarms,
	}, nil
}

func (s *etcdClientStub) DisarmAlarm(_ context.Context, alarm *clientv3.AlarmMember) error {
	if s.DisarmError != nil {
		return s.DisarmError
	}

	s.ActiveAlarms = slices.Filter(s.ActiveAlarms, func(a *etcdserverpb.AlarmMember, _ int) bool {
		return !(a.MemberID == alarm.MemberID && a.Alarm == alarm.Alarm)
	})

	return nil
}

type s3ClientStub struct {
	UploadError      error
	UploadCallCount  int
	LastUploadedBody []byte
}

var _ s3_client.S3Client = &s3ClientStub{}

func newS3ClientStub() *s3ClientStub {
	return &s3ClientStub{}
}

func (s *s3ClientStub) Upload(_ context.Context, body io.ReadCloser) (err error) {
	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to close body reader: %w", closeErr))
		}
	}()
	s.UploadCallCount++
	if s.UploadError != nil {
		return s.UploadError
	}

	if body != nil {
		data, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		s.LastUploadedBody = data
	}

	return nil
}

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
			close(recorder.Events)

			g.Expect(result.Cmp(tt.expectedSize)).
				To(Equal(0), "Expected volume size %s, got %s", tt.expectedSize.String(), result.String())

			if tt.hostedControlPlane.Spec.ETCD.AutoGrow && result.Cmp(tt.hostedControlPlane.Status.ETCDVolumeSize) > 0 {
				events := slices.ChannelToSlice(recorder.Events)
				g.Expect(events).NotTo(BeEmpty(), "Expected event to be recorded for volume auto-resize")

				g.Expect(slices.SomeBy(events, func(event string) bool {
					return strings.Contains(event, etcdVolumeSizeReCalculatedEvent)
				})).To(BeTrue(), "Expected auto-resize event, got: %v", events)
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
					g.Expect(r).To(BeNil(), "Unexpected panic: %v", r)
				}
			}()

			result := reconciler.getETCDVolumeSize(tt.hostedControlPlane)

			g.Expect(result.Cmp(tt.expectedVolumeSize)).
				To(Equal(0), "%s: expected %s, got %s", tt.description, tt.expectedVolumeSize.String(), result.String())
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

			g.Expect(result.Cmp(expectedQuantity)).
				To(Equal(0), "%s: expected %s, got %s", tt.description, tt.expectedSize, result.String())

			close(recorder.Events)
			events := slices.ChannelToSlice(recorder.Events)

			hasGrowthEvent := slices.SomeBy(events, func(event string) bool {
				return strings.Contains(event, etcdVolumeSizeReCalculatedEvent)
			})
			if tt.expectedGrowth {
				g.Expect(hasGrowthEvent).To(BeTrue(), "%s: expected growth event to be recorded", tt.description)
			} else {
				g.Expect(hasGrowthEvent).To(BeFalse(), "%s: unexpected growth event recorded", tt.description)
			}
		})
	}
}

func TestEtcdClusterReconciler_reconcileETCDSpaceUsage(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	t.Run("should handle etcd status error gracefully", func(t *testing.T) {
		stub := newEtcdClientStub()
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

		g.Expect(err).To(HaveOccurred(), "Should return error when etcd status fails")
		g.Expect(err.Error()).To(ContainSubstring("connection refused"), "Error should include original etcd error")
	})

	t.Run("should update volume usage from etcd status", func(t *testing.T) {
		stub := newEtcdClientStub()
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
		stub := newEtcdClientStub()
		stub.AlarmError = errors.New("failed to list alarms")

		hcp := &v1alpha1.HostedControlPlane{}

		recorder := record.NewFakeRecorder(10)
		reconciler := &etcdClusterReconciler{
			recorder:   recorder,
			etcdClient: stub,
		}

		err := reconciler.etcdIsHealthy(ctx, hcp)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to list alarms"))
	})

	t.Run("should disarm NOSPACE alarms when autogrow is enabled", func(t *testing.T) {
		stub := newEtcdClientStub()
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
		g.Expect(slices.SomeBy(stub.ActiveAlarms, func(a *etcdserverpb.AlarmMember) bool {
			return a.Alarm == etcdserverpb.AlarmType_NOSPACE
		})).To(BeFalse(), "NOSPACE alarm should be disarmed")
	})

	t.Run("should return error for active NOSPACE alarms when autogrow is disabled", func(t *testing.T) {
		stub := newEtcdClientStub()
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

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).
			To(ContainSubstring(etcdserverpb.AlarmType_NOSPACE.String()), "Error should mention NOSPACE alarm")
		g.Expect(slices.SomeBy(stub.ActiveAlarms, func(a *etcdserverpb.AlarmMember) bool {
			return a.Alarm == etcdserverpb.AlarmType_NOSPACE
		})).To(BeTrue(), "NOSPACE alarm should remain active")
	})

	t.Run("should return error for active non-NOSPACE alarms", func(t *testing.T) {
		stub := newEtcdClientStub()
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

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring(etcdserverpb.AlarmType_CORRUPT.String()))
	})

	t.Run("should pass when no alarms present", func(t *testing.T) {
		stub := newEtcdClientStub()

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
		etcdClientStub := newEtcdClientStub()
		s3ClientStub := newS3ClientStub()

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

		g.Expect(err).NotTo(HaveOccurred(), "Backup reconciliation should succeed")
		g.Expect(s3ClientStub.UploadCallCount).To(Equal(1), "S3 upload should be called exactly once")
		g.Expect(string(s3ClientStub.LastUploadedBody)).To(Equal(etcdSnapshotData))
		g.Expect(hcp.Status.ETCDLastBackupTime).NotTo(BeZero(), "Last backup time should be updated")
		g.Expect(hcp.Status.ETCDNextBackupTime).NotTo(BeZero(), "Next backup time should be calculated")

		close(recorder.Events)
		events := slices.ChannelToSlice(recorder.Events)
		g.Expect(slices.SomeBy(events, func(event string) bool {
			return strings.Contains(event, "EtcdBackup") && strings.Contains(event, "Created etcd backup")
		})).To(BeTrue(), "Backup event should be recorded")
	})

	t.Run("should not create backup when not scheduled", func(t *testing.T) {
		etcdClientStub := newEtcdClientStub()
		s3ClientStub := newS3ClientStub()

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

		g.Expect(err).NotTo(HaveOccurred(), "Backup reconciliation should succeed")
		g.Expect(s3ClientStub.UploadCallCount).To(Equal(0), "S3 upload should not be called")
	})

	t.Run("should handle etcd snapshot creation failure", func(t *testing.T) {
		etcdClientStub := newEtcdClientStub()
		etcdClientStub.SnapshotError = errors.New("failed to create snapshot")
		s3ClientStub := newS3ClientStub()

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

		g.Expect(err).To(HaveOccurred(), "Should return error when snapshot creation fails")
		g.Expect(err.Error()).
			To(
				ContainSubstring("failed to create etcd snapshot"),
				"Error should mention snapshot creation failure",
			)
		g.Expect(s3ClientStub.UploadCallCount).To(Equal(0), "S3 upload should not be called when snapshot fails")
	})

	t.Run("should handle S3 upload failure", func(t *testing.T) {
		etcdClientStub := newEtcdClientStub()
		s3ClientStub := newS3ClientStub()
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

		g.Expect(err).To(HaveOccurred(), "Should return error when S3 upload fails")
		g.Expect(err.Error()).
			To(
				ContainSubstring("failed to upload etcd snapshot to S3"),
				"Error should mention S3 upload failure",
			)
		g.Expect(s3ClientStub.UploadCallCount).To(Equal(1), "S3 upload should be attempted once")
	})

	t.Run("should handle invalid cron schedule", func(t *testing.T) {
		etcdClientStub := newEtcdClientStub()
		s3ClientStub := newS3ClientStub()

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

		g.Expect(err).To(HaveOccurred(), "Should return error for invalid cron schedule")
		g.Expect(err.Error()).
			To(
				ContainSubstring("failed to parse etcd backup schedule"),
				"Error should mention schedule parsing failure",
			)
		g.Expect(s3ClientStub.UploadCallCount).To(Equal(0), "S3 upload should not be called when schedule is invalid")
	})

	t.Run("should create first backup when ETCDLastBackupTime is zero", func(t *testing.T) {
		etcdClientStub := newEtcdClientStub()
		s3ClientStub := newS3ClientStub()

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

		g.Expect(err).NotTo(HaveOccurred(), "First backup should succeed")
		g.Expect(s3ClientStub.UploadCallCount).To(Equal(1), "S3 upload should be called for first backup")
		g.Expect(hcp.Status.ETCDLastBackupTime).NotTo(BeZero(), "Last backup time should be set after first backup")
		g.Expect(hcp.Status.ETCDNextBackupTime).
			NotTo(BeZero(), "Next backup time should be calculated after first backup")
	})
}
