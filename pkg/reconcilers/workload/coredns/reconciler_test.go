package coredns

import (
	"net"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	corev1 "k8s.io/api/core/v1"
)

func newTestReconciler() *coreDNSReconciler {
	return NewCoreDNSReconciler(nil, nil, "cluster.local", net.ParseIP("10.96.0.10")).(*coreDNSReconciler)
}

func TestCorefile_Hardening(t *testing.T) {
	g, _, _ := G(t)

	corefile := newTestReconciler().buildCorefile().ToString()

	g.Expect(corefile).To(MatchRegexp(`(?s)forward \. /etc/resolv\.conf \{.*health_check 5s.*\}`))
	g.Expect(corefile).To(MatchRegexp(`(?s)forward \. /etc/resolv\.conf \{.*max_fails 3.*\}`))
	g.Expect(corefile).To(MatchRegexp(`(?s)cache 30 \{.*serve_stale.*\}`))
	g.Expect(corefile).NotTo(ContainSubstring("reload"))
}

func TestCoreDNSContainer_FallsBackToLogsOnError(t *testing.T) {
	g, ctx, _ := G(t)
	r := newTestReconciler()

	container := r.buildContainer(ctx, &v1alpha1.HostedControlPlane{})

	g.Expect(container.TerminationMessagePolicy).To(HaveValue(Equal(corev1.TerminationMessageFallbackToLogsOnError)))
}
