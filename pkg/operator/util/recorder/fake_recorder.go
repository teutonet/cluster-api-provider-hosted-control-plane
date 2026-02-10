package recorder

import (
	"fmt"

	slices "github.com/samber/lo"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
)

type InfiniteDiscardingFakeRecorder struct{}

var (
	_ events.EventRecorder = &InfiniteDiscardingFakeRecorder{}
	_ Recorder             = &InfiniteDiscardingFakeRecorder{}
)

func (*InfiniteDiscardingFakeRecorder) Eventf(_, _ runtime.Object, _, _, _, _ string, _ ...interface{}) {
	// Discard the event
}

func (*InfiniteDiscardingFakeRecorder) Warnf(_ runtime.Object, _, _, _ string, _ ...interface{}) {
	// Discard the event
}

func (*InfiniteDiscardingFakeRecorder) Normalf(_ runtime.Object, _, _, _ string, _ ...interface{}) {
	// Discard the event
}

type InfiniteReturningFakeRecorder struct {
	Events []string
}

var _ events.EventRecorder = &InfiniteReturningFakeRecorder{}

func objectString(object runtime.Object) string {
	if object == nil {
		return " involvedObject<nil>"
	}
	return fmt.Sprintf(" involvedObject{kind=%s,apiVersion=%s}",
		object.GetObjectKind().GroupVersionKind().Kind,
		object.GetObjectKind().GroupVersionKind().GroupVersion(),
	)
}

func (r *InfiniteReturningFakeRecorder) writeEvent(
	object, related runtime.Object,
	eventtype, reason, action, note string,
	args ...interface{},
) {
	r.Events = append(r.Events, fmt.Sprintf(eventtype+" "+reason+" "+action+" "+note, args...)+
		objectString(object)+objectString(related))
}

func (r *InfiniteReturningFakeRecorder) Event(
	object, related runtime.Object,
	eventtype, reason, action, note string,
) {
	r.writeEvent(object, related, eventtype, reason, action, "%s", note)
}

func (r *InfiniteReturningFakeRecorder) Eventf(
	object runtime.Object,
	related runtime.Object,
	eventtype, reason, action, note string,
	args ...interface{},
) {
	r.writeEvent(object, related, eventtype, reason, action, note, args...)
}

func NewInfiniteReturningFakeRecorder(object ...runtime.Object) (*InfiniteReturningFakeRecorder, Recorder) {
	infiniteReturningFakeRecorder := NewInfiniteReturningFakeEventRecorder()
	return infiniteReturningFakeRecorder, &recorder{
		eventRecorder: infiniteReturningFakeRecorder,
		object:        slices.FirstOr(object, nil),
	}
}

func NewInfiniteReturningFakeEventRecorder() *InfiniteReturningFakeRecorder {
	return &InfiniteReturningFakeRecorder{}
}
