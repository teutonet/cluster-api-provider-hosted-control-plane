package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
)

// bundledImage describes an upstream image this operator deploys, resolved with the same
// defaulting logic (pkg/operator/util) used by the controller at runtime, so the SBOM task
// never drifts from what actually gets shipped.
type bundledImage struct {
	Name             string `json:"name"`
	Image            string `json:"image"`
	FetchAttestation bool   `json:"fetchAttestation"`
}

func bundledImages() []bundledImage {
	return []bundledImage{
		{Name: "nginx", Image: operatorutil.ResolveNginxImage(nil), FetchAttestation: true},
		{Name: "coredns", Image: operatorutil.ResolveCoreDNSImage(nil)},
		{
			Name:  "konnectivity-server",
			Image: operatorutil.ResolveKonnectivityImage(nil, "proxy-server", math.MaxUint64),
		},
		{
			Name:  "konnectivity-agent",
			Image: operatorutil.ResolveKonnectivityImage(nil, "proxy-agent", math.MaxUint64),
		},
		{Name: "etcd", Image: operatorutil.ResolveETCDImage(nil)},
	}
}

func main() {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bundledImages()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
