package util

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestResourceRequirementsToResourcesApplyConfiguration(t *testing.T) {
	tests := append(getBasicResourceTestCases(), getAdvancedResourceTestCases()...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResourceRequirementsToResourcesApplyConfiguration(tt.input)
			tt.validate(t, result)
		})
	}
}

type resourceTestCase struct {
	name     string
	input    corev1.ResourceRequirements
	validate func(*testing.T, *corev1ac.ResourceRequirementsApplyConfiguration)
}

// Helper function to validate resource limits and requests.
func validateResourceLimitsAndRequests(
	t *testing.T,
	result *corev1ac.ResourceRequirementsApplyConfiguration,
	expectedLimits, expectedRequests corev1.ResourceList,
) {
	t.Helper()

	if expectedLimits == nil {
		if result.Limits != nil && len(*result.Limits) != 0 {
			t.Errorf("Expected empty Limits, got %v", result.Limits)
		}
	} else {
		if result.Limits == nil {
			t.Fatal("Expected non-nil Limits")
		}
		if !reflect.DeepEqual(*result.Limits, expectedLimits) {
			t.Errorf("Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)
		}
	}

	if expectedRequests == nil {
		if result.Requests != nil && len(*result.Requests) != 0 {
			t.Errorf("Expected empty Requests, got %v", result.Requests)
		}
	} else {
		if result.Requests == nil {
			t.Fatal("Expected non-nil Requests")
		}
		if !reflect.DeepEqual(*result.Requests, expectedRequests) {
			t.Errorf("Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)
		}
	}
}

func getBasicResourceTestCases() []resourceTestCase {
	return []resourceTestCase{
		{
			name:  "empty resource requirements",
			input: corev1.ResourceRequirements{},
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}
				validateResourceLimitsAndRequests(t, result, nil, nil)
				if len(result.Claims) > 0 {
					t.Errorf("Expected empty Claims, got %v", result.Claims)
				}
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
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}
				expectedLimits := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				}
				validateResourceLimitsAndRequests(t, result, expectedLimits, nil)
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
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}
				expectedRequests := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				}
				validateResourceLimitsAndRequests(t, result, nil, expectedRequests)
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
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}

				expectedLimits := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				}
				if !reflect.DeepEqual(*result.Limits, expectedLimits) {
					t.Errorf("Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)
				}

				expectedRequests := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				}
				if !reflect.DeepEqual(*result.Requests, expectedRequests) {
					t.Errorf("Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)
				}
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
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}

				if result.Claims == nil {
					t.Fatal("Expected non-nil Claims")
				}

				if len(result.Claims) != 2 {
					t.Fatalf("Expected 2 claims, got %d", len(result.Claims))
				}

				// Check first claim
				if result.Claims[0].Name == nil || *result.Claims[0].Name != "gpu-claim-1" {
					t.Errorf("First claim name mismatch: got %v, want %s", result.Claims[0].Name, "gpu-claim-1")
				}
				if result.Claims[0].Request == nil || *result.Claims[0].Request != "nvidia.com/gpu" {
					t.Errorf(
						"First claim request mismatch: got %v, want %s",
						result.Claims[0].Request,
						"nvidia.com/gpu",
					)
				}

				// Check second claim
				if result.Claims[1].Name == nil || *result.Claims[1].Name != "storage-claim" {
					t.Errorf("Second claim name mismatch: got %v, want %s", result.Claims[1].Name, "storage-claim")
				}
				if result.Claims[1].Request == nil || *result.Claims[1].Request != "example.com/storage" {
					t.Errorf(
						"Second claim request mismatch: got %v, want %s",
						result.Claims[1].Request,
						"example.com/storage",
					)
				}
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
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}

				// Check limits
				expectedLimits := corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("1000m"),
					corev1.ResourceMemory:           resource.MustParse("1Gi"),
					corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
				}
				if !reflect.DeepEqual(*result.Limits, expectedLimits) {
					t.Errorf("Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)
				}

				// Check requests
				expectedRequests := corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				}
				if !reflect.DeepEqual(*result.Requests, expectedRequests) {
					t.Errorf("Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)
				}

				// Check claims
				if len(result.Claims) != 1 {
					t.Fatalf("Expected 1 claim, got %d", len(result.Claims))
				}
				if result.Claims[0].Name == nil || *result.Claims[0].Name != "special-device" {
					t.Errorf("Claim name mismatch: got %v, want %s", result.Claims[0].Name, "special-device")
				}
				if result.Claims[0].Request == nil || *result.Claims[0].Request != "vendor.com/special-device" {
					t.Errorf(
						"Claim request mismatch: got %v, want %s",
						result.Claims[0].Request,
						"vendor.com/special-device",
					)
				}
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
			validate: func(t *testing.T, result *corev1ac.ResourceRequirementsApplyConfiguration) {
				t.Helper()
				if result == nil {
					t.Fatal("Expected non-nil result")
				}

				expectedLimits := corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("5"),
					"nvidia.com/gpu":              resource.MustParse("1"),
				}
				if !reflect.DeepEqual(*result.Limits, expectedLimits) {
					t.Errorf("Limits mismatch: got %v, want %v", *result.Limits, expectedLimits)
				}

				expectedRequests := corev1.ResourceList{
					"example.com/custom-resource": resource.MustParse("2"),
				}
				if !reflect.DeepEqual(*result.Requests, expectedRequests) {
					t.Errorf("Requests mismatch: got %v, want %v", *result.Requests, expectedRequests)
				}
			},
		},
	}
}

func TestResourceRequirementsToResourcesApplyConfigurationReturnType(t *testing.T) {
	// Test that the function returns the correct type
	input := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		},
	}

	result := ResourceRequirementsToResourcesApplyConfiguration(input)

	// Verify the return type is correct
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// The result should be assignable to the correct type
	_ = result
}
