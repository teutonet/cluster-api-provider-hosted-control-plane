package util

import (
	"testing"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
)

func TestGetMinorVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		expected    uint64
		expectError bool
	}{
		{
			name:        "basic semantic version",
			version:     "1.25.3",
			expected:    25,
			expectError: false,
		},
		{
			name:        "version with v prefix",
			version:     "v1.24.2",
			expected:    24,
			expectError: false,
		},
		{
			name:        "version with patch zero",
			version:     "1.26.0",
			expected:    26,
			expectError: false,
		},
		{
			name:        "high minor version",
			version:     "1.99.15",
			expected:    99,
			expectError: false,
		},
		{
			name:        "major version zero",
			version:     "0.5.1",
			expected:    5,
			expectError: false,
		},
		{
			name:        "version with pre-release",
			version:     "1.27.1-alpha.1",
			expected:    27,
			expectError: false,
		},
		{
			name:        "version with build metadata",
			version:     "1.28.0+build.123",
			expected:    28,
			expectError: false,
		},
		{
			name:        "version with pre-release and build metadata",
			version:     "1.29.1-beta.2+build.456",
			expected:    29,
			expectError: false,
		},
		{
			name:        "tolerant parsing - missing patch",
			version:     "1.30",
			expected:    30,
			expectError: false,
		},
		{
			name:        "tolerant parsing - single number",
			version:     "2",
			expected:    0,
			expectError: false,
		},
		{
			name:        "invalid version - empty string",
			version:     "",
			expected:    0,
			expectError: true,
		},
		{
			name:        "invalid version - non-numeric",
			version:     "invalid.version.string",
			expected:    0,
			expectError: true,
		},
		{
			name:        "invalid version - special characters",
			version:     "1.25@3",
			expected:    0,
			expectError: true,
		},
		{
			name:        "invalid version - negative numbers",
			version:     "1.-25.3",
			expected:    0,
			expectError: true,
		},
		{
			name:        "edge case - very large numbers",
			version:     "999.999.999",
			expected:    999,
			expectError: false,
		},
		{
			name:        "kubernetes version 1.23",
			version:     "1.23.8",
			expected:    23,
			expectError: false,
		},
		{
			name:        "kubernetes version 1.31",
			version:     "v1.31.0-rc.1",
			expected:    31,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &v1alpha1.HostedControlPlane{
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: tt.version,
				},
			}

			result, err := GetMinorVersion(hcp)

			if tt.expectError {
				if err == nil {
					t.Errorf("GetMinorVersion() expected error but got nil")
				}
				// Don't check the result value when we expect an error
				return
			}

			if err != nil {
				t.Errorf("GetMinorVersion() unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("GetMinorVersion() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetMinorVersionNilHostedControlPlane(t *testing.T) {
	// Test edge case with nil input - this will panic, which is acceptable behavior
	// since it indicates a programming error
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("GetMinorVersion() with nil HostedControlPlane should panic")
		}
	}()

	_, _ = GetMinorVersion(nil)
}
