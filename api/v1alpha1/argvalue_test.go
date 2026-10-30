package v1alpha1

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestArgValue_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  ArgValue
		expectErr bool
	}{
		{name: "single string", input: `"a"`, expected: ArgValue{"a"}},
		{name: "list keeps the order", input: `["b", "a"]`, expected: ArgValue{"b", "a"}},
		{name: "empty list", input: `[]`, expected: ArgValue{}},
		{name: "number is rejected", input: `1`, expectErr: true},
		{name: "object is rejected", input: `{}`, expectErr: true},
		{name: "list of non strings is rejected", input: `["a", 1]`, expectErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			var got ArgValue
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(Equal(tt.expected))
		})
	}
}

func TestArgValue_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    ArgValue
		expected string
	}{
		{name: "single value is a string", input: ArgValue{"a"}, expected: `"a"`},
		{name: "multiple values are a list", input: ArgValue{"b", "a"}, expected: `["b","a"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			got, err := json.Marshal(tt.input)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(string(got)).To(Equal(tt.expected))
		})
	}
}

func TestContainer_ArgsRoundTrip(t *testing.T) {
	g, _, _ := G(t)
	var container Container
	g.Expect(json.Unmarshal([]byte(`{"args":{"single":"a","multi":["b","c"]}}`), &container)).To(Succeed())
	g.Expect(container.Args).To(Equal(map[string]ArgValue{"single": {"a"}, "multi": {"b", "c"}}))
}
