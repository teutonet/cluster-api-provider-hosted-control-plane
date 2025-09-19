package util

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// OverriddenArg represents an argument that was overridden by the controller.
type OverriddenArg struct {
	Key             string
	UserValue       string
	ControllerValue string
}

func TestArgsToSlice(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name     string
		args     []map[string]string
		expected []string
	}{
		{
			name:     "empty args",
			args:     []map[string]string{},
			expected: []string{},
		},
		{
			name:     "single empty map",
			args:     []map[string]string{{}},
			expected: []string{},
		},
		{
			name: "single map with one key-value",
			args: []map[string]string{
				{"key1": "value1"},
			},
			expected: []string{"--key1=value1"},
		},
		{
			name: "single map with multiple key-values",
			args: []map[string]string{
				{"key1": "value1", "key2": "value2"},
			},
			expected: []string{"--key1=value1", "--key2=value2"},
		},
		{
			name: "multiple maps",
			args: []map[string]string{
				{"key1": "value1"},
				{"key2": "value2"},
			},
			expected: []string{"--key1=value1", "--key2=value2"},
		},
		{
			name: "multiple maps with overlapping keys",
			args: []map[string]string{
				{"key1": "value1"},
				{"key1": "value2"},
			},
			expected: []string{"--key1=value2"},
		},
		{
			name: "maps with empty values",
			args: []map[string]string{
				{"key1": "", "key2": "value2"},
			},
			expected: []string{"--key1=", "--key2=value2"},
		},
		{
			name: "maps with special characters",
			args: []map[string]string{
				{"key-with-dash": "value_with_underscore", "key.with.dot": "value/with/slash"},
			},
			expected: []string{"--key-with-dash=value_with_underscore", "--key.with.dot=value/with/slash"},
		},
		{
			name: "complex mixed case",
			args: []map[string]string{
				{"a": "1", "z": "26"},
				{"m": "13"},
				{"b": "2"},
			},
			expected: []string{"--a=1", "--b=2", "--m=13", "--z=26"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ArgsToSlice(tt.args...)

			g.Expect(result).To(Equal(tt.expected))

			for i := 1; i < len(result); i++ {
				g.Expect(result[i-1] <= result[i]).To(BeTrue())
			}
		})
	}
}

func TestArgsToSlice_Ordering(t *testing.T) {
	g := NewWithT(t)
	input1 := []map[string]string{
		{"z": "26", "a": "1", "m": "13"},
	}
	input2 := []map[string]string{
		{"a": "1", "m": "13", "z": "26"},
	}

	result1 := ArgsToSlice(input1...)
	result2 := ArgsToSlice(input2...)

	g.Expect(result1).
		To(Equal(result2))

	expected := []string{"--a=1", "--m=13", "--z=26"}
	g.Expect(result1).To(Equal(expected))
}

func TestArgsToSliceWithObservability(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name              string
		userArgs          map[string]string
		controllerArgs    map[string]string
		expectedArgs      []string
		expectedOverrides []OverriddenArg
		expectEvent       bool
	}{
		{
			name:     "no user args, only controller args",
			userArgs: map[string]string{},
			controllerArgs: map[string]string{
				"secure-port":  "6443",
				"etcd-servers": "https://etcd:2379",
			},
			expectedArgs: []string{
				"--etcd-servers=https://etcd:2379",
				"--secure-port=6443",
			},
			expectedOverrides: []OverriddenArg{},
			expectEvent:       false,
		},
		{
			name: "no controller args, only user args",
			userArgs: map[string]string{
				"v":            "5",
				"bind-address": "127.0.0.1",
			},
			controllerArgs: map[string]string{},
			expectedArgs: []string{
				"--bind-address=127.0.0.1",
				"--v=5",
			},
			expectedOverrides: []OverriddenArg{},
			expectEvent:       false,
		},
		{
			name: "no conflicts - different keys",
			userArgs: map[string]string{
				"v":                "3",
				"concurrent-syncs": "5",
			},
			controllerArgs: map[string]string{
				"kubeconfig":   "/etc/kubeconfig",
				"bind-address": "0.0.0.0",
			},
			expectedArgs: []string{
				"--bind-address=0.0.0.0",
				"--concurrent-syncs=5",
				"--kubeconfig=/etc/kubeconfig",
				"--v=3",
			},
			expectedOverrides: []OverriddenArg{},
			expectEvent:       false,
		},
		{
			name: "single override - same key different values",
			userArgs: map[string]string{
				"secure-port": "8080",
				"v":           "4",
			},
			controllerArgs: map[string]string{
				"secure-port":  "6443",
				"etcd-servers": "https://etcd:2379",
			},
			expectedArgs: []string{
				"--etcd-servers=https://etcd:2379",
				"--secure-port=6443",
				"--v=4",
			},
			expectedOverrides: []OverriddenArg{
				{
					Key:             "secure-port",
					UserValue:       "8080",
					ControllerValue: "6443",
				},
			},
			expectEvent: true,
		},
		{
			name: "multiple overrides",
			userArgs: map[string]string{
				"secure-port":   "8080",
				"etcd-servers":  "https://bad-etcd:2379",
				"tls-cert-file": "/tmp/bad.crt",
				"v":             "4",
			},
			controllerArgs: map[string]string{
				"secure-port":   "6443",
				"etcd-servers":  "https://etcd:2379",
				"tls-cert-file": "/etc/certs/server.crt",
				"bind-address":  "0.0.0.0",
			},
			expectedArgs: []string{
				"--bind-address=0.0.0.0",
				"--etcd-servers=https://etcd:2379",
				"--secure-port=6443",
				"--tls-cert-file=/etc/certs/server.crt",
				"--v=4",
			},
			expectedOverrides: []OverriddenArg{
				{
					Key:             "secure-port",
					UserValue:       "8080",
					ControllerValue: "6443",
				},
				{
					Key:             "etcd-servers",
					UserValue:       "https://bad-etcd:2379",
					ControllerValue: "https://etcd:2379",
				},
				{
					Key:             "tls-cert-file",
					UserValue:       "/tmp/bad.crt",
					ControllerValue: "/etc/certs/server.crt",
				},
			},
			expectEvent: true,
		},
		{
			name: "same values - no override",
			userArgs: map[string]string{
				"bind-address": "0.0.0.0",
				"v":            "2",
			},
			controllerArgs: map[string]string{
				"bind-address": "0.0.0.0",
				"kubeconfig":   "/etc/kubeconfig",
			},
			expectedArgs: []string{
				"--bind-address=0.0.0.0",
				"--kubeconfig=/etc/kubeconfig",
				"--v=2",
			},
			expectedOverrides: []OverriddenArg{},
			expectEvent:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := log.IntoContext(t.Context(), log.Log.WithName("test"))

			eventRecorder := record.NewFakeRecorder(10)
			obj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-config",
					Namespace: "default",
				},
			}

			ctx = recorder.IntoContext(ctx, recorder.New(eventRecorder, obj))

			result := ArgsToSliceWithObservability(
				ctx,
				tt.userArgs,
				tt.controllerArgs,
			)

			g.Expect(result).To(Equal(tt.expectedArgs))

			if tt.expectEvent {
				g.Expect(eventRecorder.Events).To(Receive(
					ContainSubstring(argumentOverriddenEvent),
				))
			} else {
				g.Expect(eventRecorder.Events).ToNot(Receive(
					ContainSubstring(argumentOverriddenEvent),
				))
			}
		})
	}
}

func TestArgsToSliceWithObservabilityNilInputs(t *testing.T) {
	g := NewWithT(t)
	ctx := log.IntoContext(t.Context(), log.Log.WithName("test"))

	ctx = recorder.IntoContext(ctx, recorder.New(nil, nil))

	result := ArgsToSliceWithObservability(
		ctx,
		nil,
		map[string]string{"test": "value"},
	)

	expected := []string{"--test=value"}
	g.Expect(result).
		To(Equal(expected))
}

func TestOverriddenArgStruct(t *testing.T) {
	g := NewWithT(t)
	arg := OverriddenArg{
		Key:             "test-key",
		UserValue:       "user-value",
		ControllerValue: "controller-value",
	}

	g.Expect(arg.Key).To(Equal("test-key"))
	g.Expect(arg.UserValue).To(Equal("user-value"))
	g.Expect(arg.ControllerValue).
		To(Equal("controller-value"))
}

func TestArgsToSliceWithObservabilityBackwardCompatibility(t *testing.T) {
	g := NewWithT(t)
	ctx := log.IntoContext(t.Context(), log.Log.WithName("test"))
	ctx = recorder.IntoContext(ctx, recorder.New(nil, nil))

	userArgs := map[string]string{
		"user-arg1": "value1",
		"user-arg2": "value2",
	}
	controllerArgs := map[string]string{
		"controller-arg1": "value3",
		"controller-arg2": "value4",
	}

	// Test with ArgsToSlice (original function)
	originalResult := ArgsToSlice(userArgs, controllerArgs)

	// Test with ArgsToSliceWithObservability (new function)
	newResult := ArgsToSliceWithObservability(
		ctx,
		userArgs,
		controllerArgs,
	)

	g.Expect(newResult).
		To(Equal(originalResult))
}
