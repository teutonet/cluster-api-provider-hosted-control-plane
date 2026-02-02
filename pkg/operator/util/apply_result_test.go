package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
)

func TestDetectResourceApplyOperation(t *testing.T) {
	tests := []struct {
		name               string
		generation         int64
		observedGeneration int64
		expected           ApplyOperationResult
	}{
		{
			name:               "created - observedGeneration is 0",
			generation:         1,
			observedGeneration: 0,
			expected:           ApplyOperationResultCreated,
		},
		{
			name:               "updated - generation > observedGeneration",
			generation:         3,
			observedGeneration: 2,
			expected:           ApplyOperationResultUpdated,
		},
		{
			name:               "unchanged - generation == observedGeneration",
			generation:         2,
			observedGeneration: 2,
			expected:           ApplyOperationResultUnchanged,
		},
		{
			name:               "edge case - very high generation, low observed",
			generation:         100,
			observedGeneration: 1,
			expected:           ApplyOperationResultUpdated,
		},
		{
			name:               "edge case - generation 1 observed 0",
			generation:         1,
			observedGeneration: 0,
			expected:           ApplyOperationResultCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := DetectResourceApplyOperation(tt.generation, tt.observedGeneration)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestEmitResourceApplyEvent(t *testing.T) {
	tests := []struct {
		name               string
		generation         int64
		observedGeneration int64
		expectedOperation  ApplyOperationResult
		expectEvent        bool
		expectedReason     string
	}{
		{
			name:               "emits ResourceCreated for new resource",
			generation:         1,
			observedGeneration: 0,
			expectedOperation:  ApplyOperationResultCreated,
			expectEvent:        true,
			expectedReason:     EventReasonResourceCreated,
		},
		{
			name:               "emits ResourceUpdated for updated resource",
			generation:         3,
			observedGeneration: 2,
			expectedOperation:  ApplyOperationResultUpdated,
			expectEvent:        true,
			expectedReason:     EventReasonResourceUpdated,
		},
		{
			name:               "no event for unchanged resource",
			generation:         2,
			observedGeneration: 2,
			expectedOperation:  ApplyOperationResultUnchanged,
			expectEvent:        false,
			expectedReason:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			returningFakeRecorder, eventRecorder := recorder.NewInfiniteReturningFakeRecorder()

			ctx = recorder.IntoContext(ctx, eventRecorder)

			result := EmitResourceApplyEvent(ctx, "Deployment", "test-ns", "test-deploy",
				tt.generation, tt.observedGeneration)

			g.Expect(result).To(Equal(tt.expectedOperation))

			if tt.expectEvent {
				g.Expect(returningFakeRecorder.Events).To(ContainElement(
					ContainSubstring(tt.expectedReason),
				))
			} else {
				g.Expect(returningFakeRecorder.Events).To(BeEmpty())
			}
		})
	}
}
