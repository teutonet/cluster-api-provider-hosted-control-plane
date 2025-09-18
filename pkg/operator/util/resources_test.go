package util

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestResourceRequirementsToResourcesApplyConfiguration(t *testing.T) {
	g := NewWithT(t)
	tests := append(getBasicResourceTestCases(), getAdvancedResourceTestCases()...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResourceRequirementsToResourcesApplyConfiguration(tt.input)
			tt.validate(g, result)
		})
	}
}

type resourceTestCase struct {
	name     string
	input    corev1.ResourceRequirements
	validate func(Gomega, *corev1ac.ResourceRequirementsApplyConfiguration)
}

// Helper function to validate resource limits and requests.
func validateResourceLimitsAndRequests(
	g Gomega,
	result *corev1ac.ResourceRequirementsApplyConfiguration,
	expectedLimits, expectedRequests corev1.ResourceList,
) {
	if expectedLimits == nil {
		if result.Limits != nil {
			g.Expect(*result.Limits).To(BeEmpty(), "Expected empty Limits, got %v", result.Limits)
		}
	} else {
		g.Expect(result.Limits).NotTo(BeNil(), "Expected non-nil Limits")
		g.Expect(*result.Limits).To(Equal(expectedLimits), "Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)
	}

	if expectedRequests == nil {
		if result.Requests != nil {
			g.Expect(*result.Requests).To(BeEmpty(), "Expected empty Requests, got %v", result.Requests)
		}
	} else {
		g.Expect(result.Requests).NotTo(BeNil(), "Expected non-nil Requests")
		g.Expect(*result.Requests).To(
			Equal(expectedRequests),
			"Requests mismatch: got %v, want %v", *result.Requests, expectedRequests,
		)
	}
}

func getBasicResourceTestCases() []resourceTestCase {
	return []resourceTestCase{
		{
			name:  "empty resource requirements",
			input: corev1.ResourceRequirements{},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")
				validateResourceLimitsAndRequests(g, result, nil, nil)
				g.Expect(result.Claims).To(BeEmpty(), "Expected empty Claims, got %v", result.Claims)
			},
		},
		{
			name: "resource requirements with limits only",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")
				expectedLimits := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				}
				validateResourceLimitsAndRequests(g, result, expectedLimits, nil)
			},
		},
		{
			name: "resource requirements with requests only",
			input: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")
				expectedRequests := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				}
				validateResourceLimitsAndRequests(g, result, nil, expectedRequests)
			},
		},
		{
			name: "resource requirements with both limits and requests",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")

				expectedLimits := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				}
				g.Expect(*result.Limits).
					To(Equal(expectedLimits), "Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)

				expectedRequests := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				}
				g.Expect(*result.Requests).
					To(Equal(expectedRequests), "Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)
			},
		},
	}
}

func getAdvancedResourceTestCases() []resourceTestCase {
	return []resourceTestCase{
		{
			name: "resource requirements with claims",
			input: corev1.ResourceRequirements{
				Claims: []corev1.ResourceClaim{
					{
						Name:    "gpu-claim-1",
						Request: "nvidia.com/gpu",
					},
					{
						Name:    "storage-claim",
						Request: "example.com/storage",
					},
				},
			},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")
				g.Expect(result.Claims).NotTo(BeNil(), "Expected non-nil Claims")
				g.Expect(result.Claims).To(HaveLen(2), "Expected 2 claims, got %d", len(result.Claims))

				// Check first claim
				g.Expect(result.Claims[0].Name).NotTo(BeNil())
				g.Expect(*result.Claims[0].Name).
					To(Equal("gpu-claim-1"), "First claim name mismatch: got %v, want %s", result.Claims[0].Name, "gpu-claim-1")
				g.Expect(result.Claims[0].Request).NotTo(BeNil())
				g.Expect(*result.Claims[0].Request).To(
					Equal("nvidia.com/gpu"),
					"First claim request mismatch: got %v, want %s",
					result.Claims[0].Request,
					"nvidia.com/gpu",
				)

				// Check second claim
				g.Expect(result.Claims[1].Name).NotTo(BeNil())
				g.Expect(*result.Claims[1].Name).
					To(Equal("storage-claim"), "Second claim name mismatch: got %v, want %s", result.Claims[1].Name, "storage-claim")
				g.Expect(result.Claims[1].Request).NotTo(BeNil())
				g.Expect(*result.Claims[1].Request).To(
					Equal("example.com/storage"),
					"Second claim request mismatch: got %v, want %s",
					result.Claims[1].Request,
					"example.com/storage",
				)
			},
		},
		{
			name: "comprehensive resource requirements",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("1000m"),
					corev1.ResourceMemory:           resource.MustParse("1Gi"),
					corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				Claims: []corev1.ResourceClaim{
					{
						Name:    "special-device",
						Request: "vendor.com/special-device",
					},
				},
			},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")

				// Check limits
				expectedLimits := corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("1000m"),
					corev1.ResourceMemory:           resource.MustParse("1Gi"),
					corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
				}
				g.Expect(*result.Limits).
					To(Equal(expectedLimits), "Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)

				// Check requests
				expectedRequests := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				}
				g.Expect(*result.Requests).
					To(Equal(expectedRequests), "Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)

				// Check claims
				g.Expect(result.Claims).To(HaveLen(1), "Expected 1 claim, got %d", len(result.Claims))
				g.Expect(result.Claims[0].Name).NotTo(BeNil())
				g.Expect(*result.Claims[0].Name).
					To(Equal("special-device"), "Claim name mismatch: got %v, want %s", result.Claims[0].Name, "special-device")
				g.Expect(result.Claims[0].Request).NotTo(BeNil())
				g.Expect(*result.Claims[0].Request).To(
					Equal("vendor.com/special-device"),
					"Claim request mismatch: got %v, want %s",
					result.Claims[0].Request,
					"vendor.com/special-device",
				)
			},
		},
		{
			name: "custom resource types",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("5"),
					"nvidia.com/gpu":              resource.MustParse("1"),
				},
				Requests: corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("2"),
				},
			},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil(), "Expected non-nil result")

				expectedLimits := corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("5"),
					"nvidia.com/gpu":              resource.MustParse("1"),
				}
				g.Expect(*result.Limits).
					To(Equal(expectedLimits), "Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)

				expectedRequests := corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("2"),
				}
				g.Expect(*result.Requests).
					To(Equal(expectedRequests), "Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)
			},
		},
	}
}

func TestResourceRequirementsToResourcesApplyConfigurationReturnType(t *testing.T) {
	g := NewWithT(t)
	// Test that the function returns the correct type
	input := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		},
	}

	result := ResourceRequirementsToResourcesApplyConfiguration(input)

	// Verify the return type is correct
	g.Expect(result).NotTo(BeNil(), "Expected non-nil result")

	// The result should be assignable to the correct type
	_ = result
}
