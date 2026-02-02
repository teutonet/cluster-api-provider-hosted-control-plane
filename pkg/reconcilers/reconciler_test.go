package reconcilers

import (
	"testing"

	. "github.com/onsi/gomega"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/networkpolicy"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestArePodsReady(t *testing.T) {
	tests := []struct {
		name               string
		replicas           int32
		readyReplicas      int32
		availableReplicas  int32
		updatedReplicas    int32
		observedGeneration int64
		generation         int64
		expected           bool
	}{
		{
			name:               "all pods ready",
			replicas:           3,
			readyReplicas:      3,
			availableReplicas:  3,
			updatedReplicas:    3,
			observedGeneration: 1,
			generation:         1,
			expected:           true,
		},
		{
			name:               "not enough ready replicas",
			replicas:           3,
			readyReplicas:      2,
			availableReplicas:  3,
			updatedReplicas:    3,
			observedGeneration: 1,
			generation:         1,
			expected:           false,
		},
		{
			name:               "not enough available replicas",
			replicas:           3,
			readyReplicas:      3,
			availableReplicas:  2,
			updatedReplicas:    3,
			observedGeneration: 1,
			generation:         1,
			expected:           false,
		},
		{
			name:               "not enough updated replicas",
			replicas:           3,
			readyReplicas:      3,
			availableReplicas:  3,
			updatedReplicas:    2,
			observedGeneration: 1,
			generation:         1,
			expected:           false,
		},
		{
			name:               "observed generation behind",
			replicas:           3,
			readyReplicas:      3,
			availableReplicas:  3,
			updatedReplicas:    3,
			observedGeneration: 1,
			generation:         2,
			expected:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := arePodsReady(
				tt.replicas,
				tt.readyReplicas,
				tt.availableReplicas,
				tt.updatedReplicas,
				tt.observedGeneration,
				tt.generation,
			)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestIsDeploymentReady(t *testing.T) {
	tests := []struct {
		name       string
		deployment *appsv1.Deployment
		expected   bool
	}{
		{
			name:       "nil deployment",
			deployment: nil,
			expected:   false,
		},
		{
			name: "deployment ready",
			deployment: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: ptrInt32(3),
				},
				Status: appsv1.DeploymentStatus{
					ReadyReplicas:      3,
					AvailableReplicas:  3,
					UpdatedReplicas:    3,
					ObservedGeneration: 1,
				},
			},
			expected: true,
		},
		{
			name: "deployment not ready - insufficient replicas",
			deployment: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: ptrInt32(3),
				},
				Status: appsv1.DeploymentStatus{
					ReadyReplicas:      2,
					AvailableReplicas:  2,
					UpdatedReplicas:    2,
					ObservedGeneration: 1,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			if tt.deployment != nil {
				tt.deployment.Generation = 1
			}
			result := isDeploymentReady(tt.deployment)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestIsStatefulsetReady(t *testing.T) {
	tests := []struct {
		name        string
		statefulset *appsv1.StatefulSet
		expected    bool
	}{
		{
			name:        "nil statefulset",
			statefulset: nil,
			expected:    false,
		},
		{
			name: "statefulset ready",
			statefulset: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptrInt32(3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas:      3,
					AvailableReplicas:  3,
					UpdatedReplicas:    3,
					ObservedGeneration: 1,
					CurrentRevision:    "rev-1",
					UpdateRevision:     "rev-1",
				},
			},
			expected: true,
		},
		{
			name: "statefulset not ready - insufficient replicas",
			statefulset: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptrInt32(3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas:      2,
					AvailableReplicas:  2,
					UpdatedReplicas:    2,
					ObservedGeneration: 1,
					CurrentRevision:    "rev-1",
					UpdateRevision:     "rev-1",
				},
			},
			expected: false,
		},
		{
			name: "statefulset not ready - revision mismatch",
			statefulset: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptrInt32(3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas:      3,
					AvailableReplicas:  3,
					UpdatedReplicas:    3,
					ObservedGeneration: 1,
					CurrentRevision:    "rev-1",
					UpdateRevision:     "rev-2",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			if tt.statefulset != nil {
				tt.statefulset.Generation = 1
			}
			result := isStatefulsetReady(tt.statefulset)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func ptrInt32(i int32) *int32 {
	return &i
}

func TestReconcileDeployment_EmitsCreateEvent(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	fakeRecorder := recorder.NewInfiniteReturningFakeEventRecorder()
	ctx = recorder.IntoContext(ctx, recorder.New(fakeRecorder, nil))

	kubernetesClient := fake.NewClientset()

	podTemplateSpec := corev1ac.PodTemplateSpec().
		WithSpec(corev1ac.PodSpec().
			WithContainers(
				corev1ac.Container().
					WithName("test-container").
					WithImage("test-image"),
			),
		)

	ingressTargets := map[int32][]networkpolicy.IngressNetworkPolicyTarget{}
	egressTargets := map[int32][]networkpolicy.EgressNetworkPolicyTarget{}

	_, _, err := reconcileDeployment(
		ctx,
		kubernetesClient,
		nil,
		"test-namespace",
		"test-deployment",
		false,
		nil,
		map[string]string{"app": "test"},
		ingressTargets,
		egressTargets,
		1,
		podTemplateSpec,
	)

	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(fakeRecorder.Events).To(ContainElement(
		ContainSubstring(operatorutil.EventReasonResourceCreated),
	), "Expected ResourceCreated event when creating a new deployment")
}

func TestReconcileDeployment_EmitsUpdateEvent(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	fakeRecorder := recorder.NewInfiniteReturningFakeEventRecorder()
	ctx = recorder.IntoContext(ctx, recorder.New(fakeRecorder, nil))

	existingDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-deployment",
			Namespace:  "test-namespace",
			Generation: 2,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrInt32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container", Image: "test-image:v2"},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
		},
	}
	kubernetesClient := fake.NewClientset(existingDeployment)

	ingressTargets := map[int32][]networkpolicy.IngressNetworkPolicyTarget{}
	egressTargets := map[int32][]networkpolicy.EgressNetworkPolicyTarget{}

	podTemplateSpec := corev1ac.PodTemplateSpec().
		WithSpec(corev1ac.PodSpec().
			WithContainers(
				corev1ac.Container().
					WithName("test-container").
					WithImage("test-image:v2"),
			),
		)

	_, _, err := reconcileDeployment(
		ctx,
		kubernetesClient,
		nil,
		"test-namespace",
		"test-deployment",
		false,
		nil,
		map[string]string{"app": "test"},
		ingressTargets,
		egressTargets,
		1,
		podTemplateSpec,
	)

	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(fakeRecorder.Events).To(ContainElement(
		ContainSubstring(operatorutil.EventReasonResourceUpdated),
	), "Expected ResourceUpdated event when updating a deployment")
}

func TestReconcileDeployment_NoEventOnUnchanged(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	fakeRecorder := recorder.NewInfiniteReturningFakeEventRecorder()
	ctx = recorder.IntoContext(ctx, recorder.New(fakeRecorder, nil))

	existingDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-deployment",
			Namespace:  "test-namespace",
			Generation: 1,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrInt32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container", Image: "test-image"},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
		},
	}
	kubernetesClient := fake.NewClientset(existingDeployment)

	ingressTargets := map[int32][]networkpolicy.IngressNetworkPolicyTarget{}
	egressTargets := map[int32][]networkpolicy.EgressNetworkPolicyTarget{}

	podTemplateSpec := corev1ac.PodTemplateSpec().
		WithSpec(corev1ac.PodSpec().
			WithContainers(
				corev1ac.Container().
					WithName("test-container").
					WithImage("test-image"),
			),
		)

	_, _, err := reconcileDeployment(
		ctx,
		kubernetesClient,
		nil,
		"test-namespace",
		"test-deployment",
		false,
		nil,
		map[string]string{"app": "test"},
		ingressTargets,
		egressTargets,
		1,
		podTemplateSpec,
	)

	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(fakeRecorder.Events).To(Not(
		ContainElement(
			Or(
				ContainSubstring(operatorutil.EventReasonResourceCreated),
				ContainSubstring(operatorutil.EventReasonResourceUpdated),
			),
		),
	))
}
