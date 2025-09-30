package recorder

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

type Recorder interface {
	Eventf(eventType, reason, messageFmt string, args ...interface{})
}

type recorder struct {
	eventRecorder record.EventRecorder
	object        runtime.Object
}

func New(eventRecorder record.EventRecorder, object runtime.Object) Recorder {
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
		eventRecorder: record.NewFakeRecorder(100),
		object:        &runtime.Unknown{},
	}
}

func IntoContext(ctx context.Context, recorder Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func (r *recorder) Eventf(eventType, reason, messageFmt string, args ...interface{}) {
	r.eventRecorder.Eventf(r.object, eventType, reason, messageFmt, args...)
}
