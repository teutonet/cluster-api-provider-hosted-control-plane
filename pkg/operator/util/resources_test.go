package util

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
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
			g.Expect(*result.Limits).To(BeEmpty())
		}
	} else {
		g.Expect(result.Limits).NotTo(BeNil())
		g.Expect(*result.Limits).To(Equal(expectedLimits))
	}

	if expectedRequests == nil {
		if result.Requests != nil {
			g.Expect(*result.Requests).To(BeEmpty())
		}
	} else {
		g.Expect(result.Requests).NotTo(BeNil())
		g.Expect(*result.Requests).To(Equal(expectedRequests))
	}
}

func getBasicResourceTestCases() []resourceTestCase {
	return []resourceTestCase{
		{
			name:  "empty resource requirements",
			input: corev1.ResourceRequirements{},
			validate: func(g Gomega, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				g.Expect(result).NotTo(BeNil())
				validateResourceLimitsAndRequests(g, result, nil, nil)
				g.Expect(result.Claims).To(BeEmpty())
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
				g.Expect(result).NotTo(BeNil())
				validateResourceLimitsAndRequests(g, result, corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				}, nil)
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
				g.Expect(result).NotTo(BeNil())
				validateResourceLimitsAndRequests(g, result, nil, corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				})
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
				g.Expect(result).NotTo(BeNil())

				g.Expect(*result.Limits).To(Equal(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				}))

				g.Expect(*result.Requests).To(Equal(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				}))
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
				g.Expect(result).NotTo(BeNil())

				g.Expect(result.Claims).To(ConsistOf(
					MatchFields(IgnoreExtras,
						Fields{
							"Name":    PointTo(Equal("gpu-claim-1")),
							"Request": PointTo(Equal("nvidia.com/gpu")),
						},
					),
					MatchFields(IgnoreExtras,
						Fields{
							"Name":    PointTo(Equal("storage-claim")),
							"Request": PointTo(Equal("example.com/storage")),
						},
					),
				))
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
				g.Expect(result).NotTo(BeNil())

				g.Expect(*result.Limits).To(Equal(corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("1000m"),
					corev1.ResourceMemory:           resource.MustParse("1Gi"),
					corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
				}))

				g.Expect(*result.Requests).To(Equal(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				}))

				g.Expect(result.Claims).To(ConsistOf(
					MatchFields(IgnoreExtras,
						Fields{
							"Name":    PointTo(Equal("special-device")),
							"Request": PointTo(Equal("vendor.com/special-device")),
						},
					),
				))
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
				g.Expect(result).NotTo(BeNil())

				g.Expect(*result.Limits).To(Equal(corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("5"),
					"nvidia.com/gpu":              resource.MustParse("1"),
				}))

				g.Expect(*result.Requests).To(Equal(corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("2"),
				}))
			},
		},
	}
}

func TestResourceRequirementsToResourcesApplyConfigurationReturnType(t *testing.T) {
	g := NewWithT(t)
	input := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		},
	}

	result := ResourceRequirementsToResourcesApplyConfiguration(input)

	g.Expect(result).NotTo(BeNil())

	_ = result
}
