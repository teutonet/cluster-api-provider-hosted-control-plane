package util

import (
	"testing"

	. "github.com/onsi/gomega"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
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
		name           string
		userArgs       map[string]string
		controllerArgs map[string]string
		expected       []string
	}{
		{
			name:           "empty args",
			userArgs:       map[string]string{},
			controllerArgs: map[string]string{},
			expected:       []string{},
		},
		{
			name:           "single empty map",
			userArgs:       map[string]string{},
			controllerArgs: map[string]string{},
			expected:       []string{},
		},
		{
			name:           "single map with one key-value",
			userArgs:       map[string]string{},
			controllerArgs: map[string]string{"key1": "value1"},
			expected:       []string{"--key1=value1"},
		},
		{
			name:           "single map with multiple key-values",
			userArgs:       map[string]string{},
			controllerArgs: map[string]string{"key1": "value1", "key2": "value2"},
			expected:       []string{"--key1=value1", "--key2=value2"},
		},
		{
			name:           "multiple maps",
			userArgs:       map[string]string{"key1": "value1"},
			controllerArgs: map[string]string{"key2": "value2"},
			expected:       []string{"--key1=value1", "--key2=value2"},
		},
		{
			name:           "multiple maps with overlapping keys",
			userArgs:       map[string]string{"key1": "value1"},
			controllerArgs: map[string]string{"key1": "value2"},
			expected:       []string{"--key1=value2"},
		},
		{
			name:           "maps with empty values",
			userArgs:       map[string]string{},
			controllerArgs: map[string]string{"key1": "", "key2": "value2"},
			expected:       []string{"--key1=", "--key2=value2"},
		},
		{
			name:     "maps with special characters",
			userArgs: map[string]string{},
			controllerArgs: map[string]string{
				"key-with-dash": "value_with_underscore",
				"key.with.dot":  "value/with/slash",
			},
			expected: []string{"--key-with-dash=value_with_underscore", "--key.with.dot=value/with/slash"},
		},
		{
			name:           "complex mixed case",
			userArgs:       map[string]string{"a": "1", "z": "26"},
			controllerArgs: map[string]string{"m": "13", "b": "2"},
			expected:       []string{"--a=1", "--b=2", "--m=13", "--z=26"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			result := argsToSlice(argValues(tt.userArgs), tt.controllerArgs, nil)

			g.Expect(result).To(Equal(tt.expected))

			for i := 1; i < len(result); i++ {
				g.Expect(result[i-1] <= result[i]).To(BeTrue())
			}
		})
	}
}

func TestArgsToSlice_Ordering(t *testing.T) {
	g, _, _ := G(t)
	input1 := map[string]string{
		"z": "26", "a": "1", "m": "13",
	}
	input2 := map[string]string{
		"a": "1", "m": "13", "z": "26",
	}

	result1 := argsToSlice(argValues(input1), map[string]string{}, nil)
	result2 := argsToSlice(argValues(input2), map[string]string{}, nil)

	g.Expect(result1).
		To(Equal(result2))

	expected := []string{"--a=1", "--m=13", "--z=26"}
	g.Expect(result1).To(Equal(expected))
}

func TestArgsToSlice_UserSliceAndSingleValues(t *testing.T) {
	g, ctx, _ := G(t)

	result := ArgsToSlice(
		ctx,
		map[string]v1alpha1.ArgValue{
			"single": {"a"},
			"multi":  {"z", "b", "c"},
			"none":   {},
		},
		map[string]string{"controller": "x"},
	)

	g.Expect(result).To(Equal([]string{"--controller=x", "--multi=z", "--multi=b", "--multi=c", "--single=a"}))
}

func TestArgsToSlice_RepeatedValues(t *testing.T) {
	tests := []struct {
		name           string
		userArgs       map[string]string
		controllerArgs map[string]string
		opt            ArgOption
		expected       []string
	}{
		{
			name:           "repeats a flag once per value, in order",
			controllerArgs: map[string]string{"a": "1"},
			opt: ArgOption{Repeated: map[string][]string{
				"issuer": {"https://new.example.com", "https://old.example.com"},
			}},
			expected: []string{
				"--a=1",
				"--issuer=https://new.example.com",
				"--issuer=https://old.example.com",
			},
		},
		{
			name:           "a repeated key replaces the user and controller value",
			userArgs:       map[string]string{"issuer": "https://user.example.com"},
			controllerArgs: map[string]string{"issuer": "https://default.example.com"},
			opt:            ArgOption{Repeated: map[string][]string{"issuer": {"https://new.example.com"}}},
			expected:       []string{"--issuer=https://new.example.com"},
		},
		{
			name:           "uses the delimiter and prefix",
			controllerArgs: map[string]string{},
			opt: ArgOption{
				Prefix:    new("-"),
				Delimiter: new(" "),
				Repeated:  map[string][]string{"t": {"x", "y"}},
			},
			expected: []string{"-t", "x", "-t", "y"},
		},
		{
			name:           "an empty slice drops the key",
			controllerArgs: map[string]string{"issuer": "https://default.example.com"},
			opt:            ArgOption{Repeated: map[string][]string{"issuer": {}}},
			expected:       []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, ctx, _ := G(t)
			g.Expect(ArgsToSlice(ctx, argValues(tt.userArgs), tt.controllerArgs, tt.opt)).To(Equal(tt.expected))
		})
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
			g, _, _ := G(t)

			returningFakeRecorder, eventRecorder := recorder.NewInfiniteReturningFakeRecorder()

			ctx = recorder.IntoContext(ctx, eventRecorder)

			result := ArgsToSlice(
				ctx,
				argValues(tt.userArgs),
				tt.controllerArgs,
			)

			g.Expect(result).To(Equal(tt.expectedArgs))

			if tt.expectEvent {
				g.Expect(returningFakeRecorder.Events).To(ContainElement(
					ContainSubstring(argumentOverriddenEvent),
				))
			} else {
				g.Expect(returningFakeRecorder.Events).To(BeEmpty())
			}
		})
	}
}

func TestArgsToSliceWithObservabilityNilInputs(t *testing.T) {
	g, _, _ := G(t)
	ctx := log.IntoContext(t.Context(), log.Log.WithName("test"))

	ctx = recorder.IntoContext(ctx, &recorder.InfiniteDiscardingFakeRecorder{})

	result := ArgsToSlice(
		ctx,
		nil,
		map[string]string{"test": "value"},
	)

	expected := []string{"--test=value"}
	g.Expect(result).
		To(Equal(expected))
}

func TestOverriddenArgStruct(t *testing.T) {
	g, _, _ := G(t)
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
	g, _, _ := G(t)
	ctx := log.IntoContext(t.Context(), log.Log.WithName("test"))
	ctx = recorder.IntoContext(ctx, &recorder.InfiniteDiscardingFakeRecorder{})

	userArgs := map[string]string{
		"user-arg1": "value1",
		"user-arg2": "value2",
	}
	controllerArgs := map[string]string{
		"controller-arg1": "value3",
		"controller-arg2": "value4",
	}

	// Test with argsToSlice (original function)
	originalResult := argsToSlice(argValues(userArgs), controllerArgs, nil)

	// Test with ArgsToSlice (new function)
	newResult := ArgsToSlice(
		ctx,
		argValues(userArgs),
		controllerArgs,
	)

	g.Expect(newResult).
		To(Equal(originalResult))
}

func argValues(args map[string]string) map[string]v1alpha1.ArgValue {
	if args == nil {
		return nil
	}
	return slices.MapValues(args, func(value string, _ string) v1alpha1.ArgValue {
		return v1alpha1.ArgValue{value}
	})
}
