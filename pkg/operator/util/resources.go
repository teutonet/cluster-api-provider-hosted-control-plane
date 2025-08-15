package util

import (
	slices "github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func ResourceRequirementsToResourcesApplyConfiguration(
	resources corev1.ResourceRequirements,
) *corev1ac.ResourceRequirementsApplyConfiguration {
	return corev1ac.ResourceRequirements().
		WithLimits(resources.Limits).
		WithRequests(resources.Requests).
		WithClaims(
			slices.Map(resources.Claims, func(claim corev1.ResourceClaim, _ int) *corev1ac.ResourceClaimApplyConfiguration {
				return corev1ac.ResourceClaim().
					WithName(claim.Name).
					WithRequest(claim.Request)
			})...,
		)
}
