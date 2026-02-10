package recorder

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
)

type Recorder interface {
	Warnf(related runtime.Object, reason string, action string, note string, args ...interface{})
	Normalf(related runtime.Object, reason string, action string, note string, args ...interface{})
}

type recorder struct {
	eventRecorder events.EventRecorder
	object        runtime.Object
}

func New(eventRecorder events.EventRecorder, object runtime.Object) Recorder {
	return &recorder{
		eventRecorder: eventRecorder,
		object:        object,
	}
}

type recorderKey struct{}

var _ Recorder = &recorder{}

func FromContext(ctx context.Context) Recorder {
	if ctx != nil {
		if r, ok := ctx.Value(recorderKey{}).(Recorder); ok {
			return r
		}
	}
	return &recorder{
		eventRecorder: &InfiniteDiscardingFakeRecorder{},
		object:        &runtime.Unknown{},
	}
}

func IntoContext(ctx context.Context, recorder Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func (r *recorder) eventf(related runtime.Object, eventType, reason, action string, note string, args ...interface{}) {
	r.eventRecorder.Eventf(related, r.object, eventType, reason, action, note, args...)
}

func (r *recorder) Warnf(related runtime.Object, reason string, action string, note string, args ...interface{}) {
	r.eventf(related, corev1.EventTypeWarning, reason, action, note, args...)
}

func (r *recorder) Normalf(related runtime.Object, reason string, action string, note string, args ...interface{}) {
	r.eventf(related, corev1.EventTypeNormal, reason, action, note, args...)
}
