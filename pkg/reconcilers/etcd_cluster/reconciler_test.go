package etcd_cluster

import (
	"strings"
	"testing"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/record"
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

			if result.Cmp(tt.expectedSize) != 0 {
				t.Errorf("expected volume size %s, got %s", tt.expectedSize.String(), result.String())
			}

			// Check if event was recorded for autogrow case
			close(recorder.Events)
			var events []string
			for event := range recorder.Events {
				events = append(events, event)
			}

			if tt.hostedControlPlane.Spec.ETCD.AutoGrow && result.Cmp(tt.hostedControlPlane.Status.ETCDVolumeSize) > 0 {
				if len(events) == 0 {
					t.Error("expected event to be recorded for volume auto-resize")
				}

				if len(events) > 0 && !strings.Contains(events[0], etcdVolumeResizeEvent) {
					t.Errorf("expected auto-resize event, got: %s", events[0])
				}
			}
		})
	}
}

func TestEtcdClusterReconciler_ErrorHandling_InvalidVolumeData(t *testing.T) {
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
					t.Errorf("unexpected panic: %v", r)
				}
			}()

			result := reconciler.getETCDVolumeSize(tt.hostedControlPlane)

			if result.Cmp(tt.expectedVolumeSize) != 0 {
				t.Errorf("%s: expected %s, got %s", tt.description, tt.expectedVolumeSize.String(), result.String())
			}
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
			currentUsage:   "17Gi", // 20 - 17 = 3Gi free, equal to buffer
			expectedSize:   "20Gi",
			expectedGrowth: false,
			description:    "Should not grow when free space equals buffer requirement",
		},
		{
			name:           "well below threshold - should trigger growth",
			currentSize:    "20Gi",
			currentUsage:   "19Gi", // 20 - 19 = 1Gi free, well below buffer
			expectedSize:   "30Gi",
			expectedGrowth: true,
			description:    "Should grow when free space is well below buffer",
		},
		{
			name:           "massive usage spike - should grow by increment only",
			currentSize:    "20Gi",
			currentUsage:   "19.5Gi", // Nearly full
			expectedSize:   "30Gi",   // Should grow by single increment, not double
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

			if result.Cmp(expectedQuantity) != 0 {
				t.Errorf("%s: expected %s, got %s", tt.description, tt.expectedSize, result.String())
			}

			// Verify event recording for growth scenarios
			close(recorder.Events)
			var events []string
			for event := range recorder.Events {
				events = append(events, event)
			}

			hasGrowthEvent := len(events) > 0 && strings.Contains(events[0], "Auto-resized")
			if tt.expectedGrowth && !hasGrowthEvent {
				t.Errorf("%s: expected growth event to be recorded", tt.description)
			}
			if !tt.expectedGrowth && hasGrowthEvent {
				t.Errorf("%s: unexpected growth event recorded", tt.description)
			}
		})
	}
}
