package etc

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestGetOperatorConfig_ManagementClusterOIDC(t *testing.T) {
	tests := []struct {
		name     string
		value    *string
		expected bool
	}{
		{name: "enabled by default", expected: true},
		{name: "explicitly enabled", value: new("true"), expected: true},
		{name: "disabled", value: new("false"), expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			t.Setenv("CONTROLLER_NAMESPACE", "test-namespace")
			if tt.value != nil {
				t.Setenv("MANAGEMENT_CLUSTER_OIDC", *tt.value)
			}

			config, err := GetOperatorConfig()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(config.ManagementClusterOIDC).To(Equal(tt.expected))
		})
	}
}
