package util

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
)

type ApplyOperationResult string

const (
	ApplyOperationResultCreated   ApplyOperationResult = "created"
	ApplyOperationResultUpdated   ApplyOperationResult = "updated"
	ApplyOperationResultUnchanged ApplyOperationResult = "unchanged"

	EventReasonResourceCreated = "ResourceCreated"
	EventReasonResourceUpdated = "ResourceUpdated"
)

// DetectResourceApplyOperation determines whether SSA created, updated, or left
// a resource unchanged based on generation and observedGeneration.
//
// Detection logic:
//   - observedGeneration == 0: Created - resource is new, controller hasn't reconciled it yet
//   - generation > observedGeneration: Updated - spec was changed
//   - generation == observedGeneration: Unchanged - no changes made
func DetectResourceApplyOperation(generation, observedGeneration int64) ApplyOperationResult {
	switch {
	case observedGeneration == 0:
		return ApplyOperationResultCreated
	case generation > observedGeneration:
		return ApplyOperationResultUpdated
	default:
		return ApplyOperationResultUnchanged
	}
}

func EmitResourceApplyEvent(
	ctx context.Context,
	kind, namespace, name string,
	generation, observedGeneration int64,
) ApplyOperationResult {
	operation := DetectResourceApplyOperation(generation, observedGeneration)

	if operation == ApplyOperationResultUnchanged {
		return operation
	}

	eventRecorder := recorder.FromContext(ctx)
	switch operation {
	case ApplyOperationResultCreated:
		eventRecorder.Eventf(corev1.EventTypeNormal, EventReasonResourceCreated,
			"%s %s/%s created", kind, namespace, name)
	case ApplyOperationResultUpdated:
		eventRecorder.Eventf(corev1.EventTypeNormal, EventReasonResourceUpdated,
			"%s %s/%s updated", kind, namespace, name)
	case ApplyOperationResultUnchanged:
		// This case is already handled above.
	}

	return operation
}
