package util

import (
	"fmt"

	"github.com/blang/semver/v4"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
)

func GetMinorVersion(hostedControlPlane *v1alpha1.HostedControlPlane) (uint64, error) {
	if version, err := semver.ParseTolerant(hostedControlPlane.Spec.Version); err != nil {
		return 0, fmt.Errorf("failed to parse version %s: %w", hostedControlPlane.Spec.Version, err)
	} else {
		return version.Minor, nil
	}
}
