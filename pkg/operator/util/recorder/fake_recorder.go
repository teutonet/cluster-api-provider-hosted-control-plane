package recorder

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

func NewInfiniteDiscardingFakeEventRecorder() record.EventRecorder {
	fakeRecorder := record.NewFakeRecorder(10)
	go func() {
		for {
			<-fakeRecorder.Events
		}
	}()
	return fakeRecorder
}

type InfiniteReturningFakeRecorder struct {
	Events []string
}

func objectString(object runtime.Object) string {
	if object == nil {
		return " involvedObject<nil>"
	}
	return fmt.Sprintf(" involvedObject{kind=%s,apiVersion=%s}",
		object.GetObjectKind().GroupVersionKind().Kind,
		object.GetObjectKind().GroupVersionKind().GroupVersion(),
	)
}

func annotationsString(annotations map[string]string) string {
	if len(annotations) == 0 {
		return ""
	}

	return " " + fmt.Sprint(annotations)
}

func (r *InfiniteReturningFakeRecorder) writeEvent(
	object runtime.Object,
	annotations map[string]string,
	eventtype, reason, messageFmt string,
	args ...interface{},
) {
	r.Events = append(r.Events, fmt.Sprintf(eventtype+" "+reason+" "+messageFmt, args...)+
		objectString(object)+annotationsString(annotations))
}

func (r *InfiniteReturningFakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	r.writeEvent(object, nil, eventtype, reason, "%s", message)
}

func (r *InfiniteReturningFakeRecorder) Eventf(
	object runtime.Object,
	eventtype, reason, messageFmt string,
	args ...interface{},
) {
	r.writeEvent(object, nil, eventtype, reason, messageFmt, args...)
}

func (r *InfiniteReturningFakeRecorder) AnnotatedEventf(
	object runtime.Object,
	annotations map[string]string,
	eventtype, reason, messageFmt string,
	args ...interface{},
) {
	r.writeEvent(object, annotations, eventtype, reason, messageFmt, args...)
}

var _ record.EventRecorder = &InfiniteReturningFakeRecorder{}

func NewInfiniteReturningFakeRecorder() (*InfiniteReturningFakeRecorder, Recorder) {
	infiniteReturningFakeRecorder := NewInfiniteReturningFakeEventRecorder()
	return infiniteReturningFakeRecorder, &recorder{
		eventRecorder: infiniteReturningFakeRecorder,
		object:        nil,
	}
}

func NewInfiniteReturningFakeEventRecorder() *InfiniteReturningFakeRecorder {
	return &InfiniteReturningFakeRecorder{}
}
