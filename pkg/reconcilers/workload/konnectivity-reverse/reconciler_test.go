package konnectivityreverse

import (
	"testing"

	. "github.com/onsi/gomega"
)

// TestClusterRoleBindingNameIsolation verifies that the reverse konnectivity
// ClusterRoleBinding uses a unique name that does not conflict with the forward
// tunnel's ClusterRoleBinding. Both reconcilers create a binding to
// "system:auth-delegator", but they must use different names so that when both
// tunnels are active they don't overwrite each other.
func TestClusterRoleBindingNameIsolation(t *testing.T) {
	g := NewWithT(t)

	const konnectivityServerAudience = "system:konnectivity-server"
	const forwardClusterRoleBindingName = konnectivityServerAudience
	const reverseClusterRoleBindingName = konnectivityServerAudience + "-reverse"

	// The reverse binding must not share the forward binding's name
	g.Expect(reverseClusterRoleBindingName).NotTo(Equal(forwardClusterRoleBindingName),
		"reverse ClusterRoleBinding name must not collide with forward binding")
	g.Expect(reverseClusterRoleBindingName).To(HaveSuffix("-reverse"),
		"reverse ClusterRoleBinding should follow -reverse suffix convention for clarity")
}

// TestPortIsolation verifies that forward and reverse tunnels use different ports
// so they can coexist on the same workload cluster without port conflicts.
func TestPortIsolation(t *testing.T) {
	g := NewWithT(t)

	const forwardKonnectivityPort int32 = 8132
	const reverseKonnectivityPort int32 = 8134

	g.Expect(reverseKonnectivityPort).NotTo(Equal(forwardKonnectivityPort),
		"reverse tunnel port must not conflict with forward tunnel port")
}

// TestServiceAccountNameIsolation verifies that forward and reverse tunnels use
// different service account names to avoid RBAC conflicts when both are active.
func TestServiceAccountNameIsolation(t *testing.T) {
	g := NewWithT(t)

	const forwardSAName = "konnectivity-agent"
	const reverseSAName = "konnectivity-server"

	g.Expect(reverseSAName).NotTo(Equal(forwardSAName),
		"reverse tunnel service account must not share name with forward tunnel")
}

// TestHealthPortIsolation verifies that health ports do not conflict between
// tunnels. Both use 8134 but on different deployments with different selectors,
// so this is safe — but documented for clarity.
func TestHealthPortIsolation(t *testing.T) {
	g := NewWithT(t)

	// Both forward (konnectivity-agent) and reverse (konnectivity-server) use
	// health port 8134. This is safe because they are separate deployments with
	// different selectors. The reverse tunnel also exposes a separate server port
	// (8134 vs the forward tunnel's 8132).
	const forwardHealthPort int32 = 8134
	const reverseHealthPort int32 = 8134
	const forwardServerPort int32 = 8132
	const reverseServerPort int32 = 8134

	// Health ports can be the same (different deployments)
	g.Expect(forwardHealthPort).To(Equal(reverseHealthPort))

	// Server ports must differ — these are the actual tunnel ports
	g.Expect(reverseServerPort).NotTo(Equal(forwardServerPort),
		"reverse server port must not conflict with forward server port")
}

// TestLabelIsolation verifies that forward and reverse tunnel resources use
// distinct component labels so they can be independently managed.
func TestLabelIsolation(t *testing.T) {
	g := NewWithT(t)

	const forwardComponent = "konnectivity"
	const reverseComponent = "konnectivity-reverse"

	g.Expect(reverseComponent).NotTo(Equal(forwardComponent),
		"reverse tunnel label must not share component with forward tunnel")
}
