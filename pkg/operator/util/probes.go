package util

import (
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

func CreateStartupProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
	path string,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	healthCheckTimeout := konstants.ControlPlaneComponentHealthCheckTimeout.Seconds()
	periodSeconds := int32(10)
	failureThreshold := int32(math.Ceil(healthCheckTimeout / float64(periodSeconds)))
	return CreateProbe(path, probePort, scheme).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(failureThreshold).
		WithPeriodSeconds(periodSeconds)
}

func CreateReadinessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
	path string,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	return CreateProbe(path, probePort, scheme).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(3).
		WithPeriodSeconds(1)
}

func CreateLivenessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
	path string,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	return CreateProbe(path, probePort, scheme).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(8).
		WithPeriodSeconds(10)
}

func CreateProbe(
	path string,
	probePort *corev1ac.ContainerPortApplyConfiguration,
	scheme corev1.URIScheme,
) *corev1ac.ProbeApplyConfiguration {
	return corev1ac.Probe().WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath(path).
		WithPort(intstr.FromString(*probePort.Name)).
		WithScheme(scheme),
	)
}
