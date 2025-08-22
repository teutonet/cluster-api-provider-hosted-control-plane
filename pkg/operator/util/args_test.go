package util

import (
	"reflect"
	"testing"
)

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
