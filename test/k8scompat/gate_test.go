package k8scompat

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestIsEnabled_TogglesOnEnvVar(t *testing.T) {
	g, _, _ := G(t)

	t.Setenv(enabledEnvVar, "")
	g.Expect(isEnabled()).To(BeFalse())

	t.Setenv(enabledEnvVar, "1")
	g.Expect(isEnabled()).To(BeTrue())
}
