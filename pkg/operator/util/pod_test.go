package util

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestValidateMounts(t *testing.T) {
	g := NewWithT(t)

	tests := getValidateMountsTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validateMountTest(g, tt)
		})
	}
}

type validateMountTestCase struct {
	name        string
	podSpec     *corev1ac.PodSpecApplyConfiguration
	expectError bool
	errorMsg    string
}

func getValidateMountsTestCases() []validateMountTestCase {
	return []validateMountTestCase{
		{
			name: "valid pod spec with no volumes or mounts",
			podSpec: corev1ac.PodSpec().WithContainers(
				corev1ac.Container().WithName("test-container"),
			),
			expectError: false,
		},
		{
			name: "valid pod spec with matching volume and mount",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("config-vol").WithConfigMap(
						corev1ac.ConfigMapVolumeSource().WithName("config-map"),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("test-container").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("config-vol").WithMountPath("/config"),
						),
				),
			expectError: false,
		},
		{
			name: "valid pod spec with multiple matching volumes and mounts",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("config-vol").WithConfigMap(
						corev1ac.ConfigMapVolumeSource().WithName("config-map"),
					),
					corev1ac.Volume().WithName("secret-vol").WithSecret(
						corev1ac.SecretVolumeSource().WithSecretName("secret"),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("container1").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("config-vol").WithMountPath("/config"),
						),
					corev1ac.Container().
						WithName("container2").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("secret-vol").WithMountPath("/secret"),
							corev1ac.VolumeMount().WithName("config-vol").WithMountPath("/shared-config"),
						),
				),
			expectError: false,
		},
		{
			name: "invalid pod spec with non-existent volume mount",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("existing-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("test-container").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("non-existent-vol").WithMountPath("/data"),
						),
				),
			expectError: true,
			errorMsg:    "non-existent-vol",
		},
		{
			name: "invalid pod spec with multiple non-existent volume mounts",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("good-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("container1").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("missing-vol1").WithMountPath("/data1"),
						),
					corev1ac.Container().
						WithName("container2").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("good-vol").WithMountPath("/good"),
							corev1ac.VolumeMount().WithName("missing-vol2").WithMountPath("/data2"),
						),
				),
			expectError: true,
			errorMsg:    "missing-vol1,missing-vol2",
		},
		{
			name: "valid pod spec with no containers",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("unused-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
				),
			expectError: false,
		},
		{
			name: "valid pod spec with container having no volume mounts",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("unused-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
				).
				WithContainers(
					corev1ac.Container().WithName("test-container"),
				),
			expectError: false,
		},
		{
			name: "mixed valid and invalid mounts",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("good-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("test-container").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("good-vol").WithMountPath("/good"),
							corev1ac.VolumeMount().WithName("bad-vol").WithMountPath("/bad"),
						),
				),
			expectError: true,
			errorMsg:    "bad-vol",
		},
		{
			name: "invalid mount with empty path",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("test-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("test-container").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("test-vol").WithMountPath(""),
						),
				),
			expectError: true,
			errorMsg:    "test-vol",
		},
		{
			name: "valid pod spec with different volume types",
			podSpec: corev1ac.PodSpec().
				WithVolumes(
					corev1ac.Volume().WithName("config-vol").WithConfigMap(
						corev1ac.ConfigMapVolumeSource().WithName("config"),
					),
					corev1ac.Volume().WithName("secret-vol").WithSecret(
						corev1ac.SecretVolumeSource().WithSecretName("secret"),
					),
					corev1ac.Volume().WithName("empty-vol").WithEmptyDir(
						corev1ac.EmptyDirVolumeSource(),
					),
					corev1ac.Volume().WithName("host-vol").WithHostPath(
						corev1ac.HostPathVolumeSource().WithPath("/host/path"),
					),
				).
				WithContainers(
					corev1ac.Container().
						WithName("test-container").
						WithVolumeMounts(
							corev1ac.VolumeMount().WithName("config-vol").WithMountPath("/config"),
							corev1ac.VolumeMount().WithName("secret-vol").WithMountPath("/secret"),
							corev1ac.VolumeMount().WithName("empty-vol").WithMountPath("/tmp"),
							corev1ac.VolumeMount().WithName("host-vol").WithMountPath("/host"),
						),
				),
			expectError: false,
		},
	}
}

func validateMountTest(g Gomega, tt validateMountTestCase) {
	err := ValidateMounts(tt.podSpec)

	if tt.expectError {
		validateExpectedError(g, err, tt.errorMsg)
	} else {
		g.Expect(err).NotTo(HaveOccurred(), "ValidateMounts() unexpected error: %v", err)
	}
}

func validateExpectedError(g Gomega, err error, expectedMsg string) {
	g.Expect(err).To(HaveOccurred(), "ValidateMounts() expected error but got nil")

	// Check that the error is the expected type
	g.Expect(errors.Is(err, ErrInvalidMount)).
		To(BeTrue(), "ValidateMounts() error type mismatch: expected ErrInvalidMount, got %T", err)

	// Check that the error message contains the expected volume names
	if expectedMsg != "" {
		g.Expect(err.Error()).To(
			ContainSubstring(expectedMsg),
			"ValidateMounts() error message %q does not contain expected text %q",
			err.Error(),
			expectedMsg,
		)
	}

	// Verify the error message mentions volume mounts
	g.Expect(err.Error()).
		To(ContainSubstring("VolumeMounts"), "ValidateMounts() error message should mention VolumeMounts: %s", err.Error())
}

func TestValidateMountsErrorMessage(t *testing.T) {
	g := NewWithT(t)
	// Test specific error message format
	podSpec := corev1ac.PodSpec().
		WithContainers(
			corev1ac.Container().
				WithName("test-container").
				WithVolumeMounts(
					corev1ac.VolumeMount().WithName("missing1").WithMountPath("/data1"),
					corev1ac.VolumeMount().WithName("missing2").WithMountPath("/data2"),
				),
		)

	err := ValidateMounts(podSpec)
	g.Expect(err).To(HaveOccurred(), "Expected error but got nil")

	errorMsg := err.Error()

	// Should contain both missing volume names
	g.Expect(errorMsg).To(ContainSubstring("missing1"), "Error message should contain 'missing1': %s", errorMsg)
	g.Expect(errorMsg).To(ContainSubstring("missing2"), "Error message should contain 'missing2': %s", errorMsg)

	// Should contain "VolumeMounts"
	g.Expect(errorMsg).To(ContainSubstring("VolumeMounts"), "Error message should contain 'VolumeMounts': %s", errorMsg)

	// Should mention non-existent volume
	g.Expect(errorMsg).
		To(ContainSubstring("non-existent"), "Error message should mention non-existent volume: %s", errorMsg)
}

func TestValidateMountsNilPodSpec(t *testing.T) {
	g := NewWithT(t)
	// Test edge case with nil pod spec - this will panic, which is acceptable
	// since it indicates a programming error
	defer func() {
		if r := recover(); r == nil {
			g.Expect(r).NotTo(BeNil(), "ValidateMounts() with nil PodSpec should panic")
		}
	}()

	_ = ValidateMounts(nil)
}
