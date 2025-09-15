package util

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

// Helper function to validate basic probe configuration.
func validateBasicProbe(
	t *testing.T,
	probe *corev1ac.ProbeApplyConfiguration,
	path string,
	portName string,
	scheme corev1.URIScheme,
) {
	t.Helper()

	if probe == nil {
		t.Fatal("Expected non-nil probe")
	}
	if probe.HTTPGet == nil {
		t.Fatal("Expected HTTPGet to be set")
	}
	if probe.HTTPGet.Path == nil || *probe.HTTPGet.Path != path {
		t.Errorf("Expected path %q, got %v", path, probe.HTTPGet.Path)
	}
	if probe.HTTPGet.Port == nil || probe.HTTPGet.Port.StrVal != portName {
		t.Errorf("Expected port name %q, got %v", portName, probe.HTTPGet.Port)
	}
	if probe.HTTPGet.Scheme == nil || *probe.HTTPGet.Scheme != scheme {
		t.Errorf("Expected scheme %v, got %v", scheme, probe.HTTPGet.Scheme)
	}
}

func TestCreateProbe(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		probePort *corev1ac.ContainerPortApplyConfiguration
		scheme    corev1.URIScheme
	}{
		{
			name:      "basic HTTP probe",
			path:      "/healthz",
			probePort: corev1ac.ContainerPort().WithName("http").WithContainerPort(8080),
			scheme:    corev1.URISchemeHTTP,
		},
		{
			name:      "HTTPS probe",
			path:      "/ready",
			probePort: corev1ac.ContainerPort().WithName("https").WithContainerPort(8443),
			scheme:    corev1.URISchemeHTTPS,
		},
		{
			name:      "root path probe",
			path:      "/",
			probePort: corev1ac.ContainerPort().WithName("web").WithContainerPort(80),
			scheme:    corev1.URISchemeHTTP,
		},
		{
			name:      "complex path probe",
			path:      "/api/v1/health",
			probePort: corev1ac.ContainerPort().WithName("api").WithContainerPort(9090),
			scheme:    corev1.URISchemeHTTP,
		},
		{
			name:      "empty path probe",
			path:      "",
			probePort: corev1ac.ContainerPort().WithName("default").WithContainerPort(8080),
			scheme:    corev1.URISchemeHTTP,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := CreateProbe(tt.path, tt.probePort, tt.scheme)
			validateBasicProbe(t, probe, tt.path, *tt.probePort.Name, tt.scheme)
		})
	}
}

// Helper function to validate probe timing configuration.
func validateProbeTiming(
	t *testing.T,
	probe *corev1ac.ProbeApplyConfiguration,
	initialDelay, timeout, period, failureThreshold int32,
) {
	t.Helper()

	if probe.InitialDelaySeconds == nil || *probe.InitialDelaySeconds != initialDelay {
		t.Errorf("Expected InitialDelaySeconds %d, got %v", initialDelay, probe.InitialDelaySeconds)
	}
	if probe.TimeoutSeconds == nil || *probe.TimeoutSeconds != timeout {
		t.Errorf("Expected TimeoutSeconds %d, got %v", timeout, probe.TimeoutSeconds)
	}
	if probe.PeriodSeconds == nil || *probe.PeriodSeconds != period {
		t.Errorf("Expected PeriodSeconds %d, got %v", period, probe.PeriodSeconds)
	}
	if probe.FailureThreshold == nil || *probe.FailureThreshold != failureThreshold {
		t.Errorf("Expected FailureThreshold %d, got %v", failureThreshold, probe.FailureThreshold)
	}
}

func TestCreateStartupProbe(t *testing.T) {
	probePort := corev1ac.ContainerPort().WithName("http").WithContainerPort(8080)
	path := "/healthz"
	scheme := corev1.URISchemeHTTP

	probe := CreateStartupProbe(probePort, path, scheme)

	validateBasicProbe(t, probe, path, *probePort.Name, scheme)
	// Check startup probe specific settings - failure threshold is calculated, so just verify it's set
	if probe.FailureThreshold == nil {
		t.Errorf("Expected FailureThreshold to be set")
	}
	validateProbeTiming(t, probe, 0, 15, 10, *probe.FailureThreshold)
}

func TestCreateReadinessProbe(t *testing.T) {
	probePort := corev1ac.ContainerPort().WithName("http").WithContainerPort(8080)
	path := "/ready"
	scheme := corev1.URISchemeHTTP

	probe := CreateReadinessProbe(probePort, path, scheme)

	validateBasicProbe(t, probe, path, *probePort.Name, scheme)
	validateProbeTiming(t, probe, 0, 15, 1, 3)
}

func TestCreateLivenessProbe(t *testing.T) {
	probePort := corev1ac.ContainerPort().WithName("http").WithContainerPort(8080)
	path := "/healthz"
	scheme := corev1.URISchemeHTTP

	probe := CreateLivenessProbe(probePort, path, scheme)

	validateBasicProbe(t, probe, path, *probePort.Name, scheme)
	validateProbeTiming(t, probe, 0, 15, 10, 8)
}

func TestCreateStartupProbeHTTPS(t *testing.T) {
	probePort := corev1ac.ContainerPort().WithName("https").WithContainerPort(8443)
	path := "/startup"
	scheme := corev1.URISchemeHTTPS

	probe := CreateStartupProbe(probePort, path, scheme)

	validateBasicProbe(t, probe, path, *probePort.Name, scheme)
	if probe.FailureThreshold == nil {
		t.Errorf("Expected FailureThreshold to be set")
	}
}

func TestCreateReadinessProbeHTTPS(t *testing.T) {
	probePort := corev1ac.ContainerPort().WithName("https").WithContainerPort(8443)
	path := "/readiness"
	scheme := corev1.URISchemeHTTPS

	probe := CreateReadinessProbe(probePort, path, scheme)

	validateBasicProbe(t, probe, path, *probePort.Name, scheme)
	validateProbeTiming(t, probe, 0, 15, 1, 3)
}

func TestCreateLivenessProbeHTTPS(t *testing.T) {
	probePort := corev1ac.ContainerPort().WithName("https").WithContainerPort(8443)
	path := "/livez"
	scheme := corev1.URISchemeHTTPS

	probe := CreateLivenessProbe(probePort, path, scheme)

	validateBasicProbe(t, probe, path, *probePort.Name, scheme)
	validateProbeTiming(t, probe, 0, 15, 10, 8)
}

func TestProbePortIntegration(t *testing.T) {
	// Test that the port configuration is correctly integrated into the probe
	probePort := corev1ac.ContainerPort().WithName("metrics").WithContainerPort(9090)
	path := "/metrics"
	scheme := corev1.URISchemeHTTP

	probe := CreateProbe(path, probePort, scheme)

	if probe == nil || probe.HTTPGet == nil {
		t.Fatal("Expected valid probe with HTTPGet")
	}

	expectedPort := intstr.FromString("metrics")
	if probe.HTTPGet.Port == nil || *probe.HTTPGet.Port != expectedPort {
		t.Errorf("Expected port %v, got %v", expectedPort, probe.HTTPGet.Port)
	}
}

func TestProbeTypeDifferences(t *testing.T) {
	// Test that different probe types have different configurations
	probePort := corev1ac.ContainerPort().WithName("http").WithContainerPort(8080)
	path := "/health"
	scheme := corev1.URISchemeHTTP

	startup := CreateStartupProbe(probePort, path, scheme)
	readiness := CreateReadinessProbe(probePort, path, scheme)
	liveness := CreateLivenessProbe(probePort, path, scheme)

	// All should have different failure thresholds
	if *startup.FailureThreshold == *readiness.FailureThreshold {
		t.Errorf("Startup and readiness probes should have different failure thresholds")
	}
	if *readiness.FailureThreshold == *liveness.FailureThreshold {
		t.Errorf("Readiness and liveness probes should have different failure thresholds")
	}
	if *startup.FailureThreshold == *liveness.FailureThreshold {
		t.Errorf("Startup and liveness probes should have different failure thresholds")
	}

	// All should have different period seconds
	if *startup.PeriodSeconds == *readiness.PeriodSeconds {
		t.Errorf("Startup and readiness probes should have different period seconds")
	}
	if *readiness.PeriodSeconds == *liveness.PeriodSeconds {
		t.Errorf("Readiness and liveness probes should have different period seconds")
	}

	// All should have the same timeout and initial delay (based on implementation)
	if *startup.TimeoutSeconds != *readiness.TimeoutSeconds || *readiness.TimeoutSeconds != *liveness.TimeoutSeconds {
		t.Errorf("All probes should have the same timeout seconds")
	}
	if *startup.InitialDelaySeconds != *readiness.InitialDelaySeconds ||
		*readiness.InitialDelaySeconds != *liveness.InitialDelaySeconds {
		t.Errorf("All probes should have the same initial delay seconds")
	}
}
