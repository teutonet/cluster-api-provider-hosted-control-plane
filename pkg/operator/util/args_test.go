package util

import (
	"reflect"
	"strings"
	"testing"

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

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ArgsToSlice() = %v, want %v", result, tt.expected)
			}

			for i := 1; i < len(result); i++ {
				if result[i-1] > result[i] {
					t.Errorf("Result is not sorted: %s > %s", result[i-1], result[i])
				}
			}
		})
	}
}

func TestArgsToSlice_Ordering(t *testing.T) {
	input1 := []map[string]string{
		{"z": "26", "a": "1", "m": "13"},
	}
	input2 := []map[string]string{
		{"a": "1", "m": "13", "z": "26"},
	}

	result1 := ArgsToSlice(input1...)
	result2 := ArgsToSlice(input2...)

	if !reflect.DeepEqual(result1, result2) {
		t.Errorf("ArgsToSlice() should produce consistent sorted output regardless of input order")
		t.Errorf("Input1 result: %v", result1)
		t.Errorf("Input2 result: %v", result2)
	}

	expected := []string{"--a=1", "--m=13", "--z=26"}
	if !reflect.DeepEqual(result1, expected) {
		t.Errorf("Expected sorted output %v, got %v", expected, result1)
	}
}

func TestArgsToSliceWithObservability(t *testing.T) {
	tests := []struct {
		name              string
		userArgs          map[string]string
		controllerArgs    map[string]string
		expectedArgs      []string
		expectedOverrides []OverriddenArg
		expectEvent       bool
		expectedEventType string
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
			expectEvent:       true,
			expectedEventType: corev1.EventTypeWarning,
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
			expectEvent:       true,
			expectedEventType: corev1.EventTypeWarning,
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

			// Check the resulting args
			if !reflect.DeepEqual(result, tt.expectedArgs) {
				t.Errorf("ArgsToSliceWithObservability() = %v, want %v", result, tt.expectedArgs)
			}

			// Check events
			if tt.expectEvent {
				select {
				case event := <-eventRecorder.Events:
					if !strings.Contains(event, tt.expectedEventType) {
						t.Errorf("Expected event type %s, but event was: %s", tt.expectedEventType, event)
					}
					if !strings.Contains(event, "ArgumentOverridden") {
						t.Errorf("Expected event to contain 'ArgumentsOverridden', but event was: %s", event)
					}
				default:
					t.Errorf("Expected an event to be emitted, but none was found")
				}
			} else {
				select {
				case event := <-eventRecorder.Events:
					t.Errorf("Expected no event, but got: %s", event)
				default:
					// Expected no event - this is correct
				}
			}

			if len(tt.expectedOverrides) > 0 {
				foundOverride := false
				for _, expectedOverride := range tt.expectedOverrides {
					if userValue, exists := tt.userArgs[expectedOverride.Key]; exists {
						if controllerValue, controllerExists := tt.controllerArgs[expectedOverride.Key]; controllerExists {
							if userValue != controllerValue && userValue == expectedOverride.UserValue &&
								controllerValue == expectedOverride.ControllerValue {
								foundOverride = true
								break
							}
						}
					}
				}
				if !foundOverride {
					t.Errorf("Expected to find at least one override matching the test case expectations")
				}
			}
		})
	}
}

func TestArgsToSliceWithObservabilityNilInputs(t *testing.T) {
	ctx := log.IntoContext(t.Context(), log.Log.WithName("test"))

	ctx = recorder.IntoContext(ctx, recorder.New(nil, nil))

	result := ArgsToSliceWithObservability(
		ctx,
		nil,
		map[string]string{"test": "value"},
	)

	expected := []string{"--test=value"}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("ArgsToSliceWithObservability() with nil inputs = %v, want %v", result, expected)
	}
}

func TestOverriddenArgStruct(t *testing.T) {
	arg := OverriddenArg{
		Key:             "test-key",
		UserValue:       "user-value",
		ControllerValue: "controller-value",
	}

	if arg.Key != "test-key" {
		t.Errorf("Expected Key to be 'test-key', got %s", arg.Key)
	}
	if arg.UserValue != "user-value" {
		t.Errorf("Expected UserValue to be 'user-value', got %s", arg.UserValue)
	}
	if arg.ControllerValue != "controller-value" {
		t.Errorf("Expected ControllerValue to be 'controller-value', got %s", arg.ControllerValue)
	}
}

func TestArgsToSliceWithObservabilityBackwardCompatibility(t *testing.T) {
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

	if !reflect.DeepEqual(originalResult, newResult) {
		t.Errorf("Backward compatibility test failed. ArgsToSlice() = %v, ArgsToSliceWithObservability() = %v",
			originalResult, newResult)
	}
}
