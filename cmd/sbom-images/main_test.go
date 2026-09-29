package main

import (
	"math"
	"testing"

	. "github.com/onsi/gomega"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestBundledImages(t *testing.T) {
	g, _, _ := G(t)

	// Expected values are derived from the same Resolve*Image functions bundledImages()
	// calls, rather than hardcoded, so this doesn't break on every renovate version bump —
	// it only breaks if the name/flag composition itself regresses.
	g.Expect(bundledImages()).To(ConsistOf(
		bundledImage{Name: "nginx", Image: operatorutil.ResolveNginxImage(nil), FetchAttestation: true},
		bundledImage{Name: "coredns", Image: operatorutil.ResolveCoreDNSImage(nil)},
		bundledImage{
			Name:  "konnectivity-server",
			Image: operatorutil.ResolveKonnectivityImage(nil, "proxy-server", math.MaxUint64),
		},
		bundledImage{
			Name:  "konnectivity-agent",
			Image: operatorutil.ResolveKonnectivityImage(nil, "proxy-agent", math.MaxUint64),
		},
		bundledImage{Name: "etcd", Image: operatorutil.ResolveETCDImage(nil)},
	))
}
