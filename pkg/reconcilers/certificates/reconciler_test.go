package certificates

import (
	"testing"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagermetav1 "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/importcycle"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestCertificateReconciler_isIssuerReady(t *testing.T) {
	reconciler := &certificateReconciler{}

	tests := []struct {
		name     string
		issuer   *certmanagerv1.Issuer
		expected bool
	}{
		{
			name: "ready issuer",
			issuer: &certmanagerv1.Issuer{
				Status: certmanagerv1.IssuerStatus{
					Conditions: []certmanagerv1.IssuerCondition{
						{
							Type:   certmanagerv1.IssuerConditionReady,
							Status: certmanagermetav1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "not ready issuer",
			issuer: &certmanagerv1.Issuer{
				Status: certmanagerv1.IssuerStatus{
					Conditions: []certmanagerv1.IssuerCondition{
						{
							Type:   certmanagerv1.IssuerConditionReady,
							Status: certmanagermetav1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "issuer without ready condition",
			issuer: &certmanagerv1.Issuer{
				Status: certmanagerv1.IssuerStatus{
					Conditions: []certmanagerv1.IssuerCondition{
						{
							Type:   "SomeOtherCondition",
							Status: certmanagermetav1.ConditionTrue,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "issuer with no conditions",
			issuer: &certmanagerv1.Issuer{
				Status: certmanagerv1.IssuerStatus{
					Conditions: []certmanagerv1.IssuerCondition{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			result := reconciler.isIssuerReady(tt.issuer)

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestCertificateReconciler_isCertificateReady(t *testing.T) {
	reconciler := &certificateReconciler{}

	tests := []struct {
		name        string
		certificate *certmanagerv1.Certificate
		expected    bool
	}{
		{
			name: "ready certificate",
			certificate: &certmanagerv1.Certificate{
				Status: certmanagerv1.CertificateStatus{
					Conditions: []certmanagerv1.CertificateCondition{
						{
							Type:   certmanagerv1.CertificateConditionReady,
							Status: certmanagermetav1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "not ready certificate",
			certificate: &certmanagerv1.Certificate{
				Status: certmanagerv1.CertificateStatus{
					Conditions: []certmanagerv1.CertificateCondition{
						{
							Type:   certmanagerv1.CertificateConditionReady,
							Status: certmanagermetav1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "certificate without ready condition",
			certificate: &certmanagerv1.Certificate{
				Status: certmanagerv1.CertificateStatus{
					Conditions: []certmanagerv1.CertificateCondition{
						{
							Type:   "Issuing",
							Status: certmanagermetav1.ConditionTrue,
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			result := reconciler.isCertificateReady(tt.certificate)

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestCertificateReconciler_ErrorHandling_EdgeCases(t *testing.T) {
	g, _, _ := G(t)
	reconciler := &certificateReconciler{}

	emptyIssuer := &certmanagerv1.Issuer{
		Status: certmanagerv1.IssuerStatus{
			Conditions: nil,
		},
	}

	g.Expect(reconciler.isIssuerReady(emptyIssuer)).To(BeFalse())

	emptyConditionsIssuer := &certmanagerv1.Issuer{
		Status: certmanagerv1.IssuerStatus{
			Conditions: []certmanagerv1.IssuerCondition{},
		},
	}

	g.Expect(reconciler.isIssuerReady(emptyConditionsIssuer)).To(BeFalse())

	inconsistentCertificate := &certmanagerv1.Certificate{
		Status: certmanagerv1.CertificateStatus{
			Conditions: []certmanagerv1.CertificateCondition{
				{
					Type:   certmanagerv1.CertificateConditionReady,
					Status: certmanagermetav1.ConditionUnknown, // Unknown status
					Reason: "Pending",
				},
			},
		},
	}

	g.Expect(reconciler.isCertificateReady(inconsistentCertificate)).To(BeFalse())
}

func TestCertificateReconciler_createCertificateSpecs_ServiceAccountKeepsPrivateKey(t *testing.T) {
	g, _, _ := G(t)
	r := &certificateReconciler{certificateDuration: time.Hour, caCertificateDuration: time.Hour}

	specs := r.createCertificateSpecs(bundleTestHCP, bundleTestCluster)

	serviceAccount := specs[names.GetServiceAccountCertificateName(bundleTestCluster)]
	g.Expect(serviceAccount.spec.PrivateKey).NotTo(BeNil())
	g.Expect(*serviceAccount.spec.PrivateKey.RotationPolicy).To(Equal(certmanagerv1.RotationPolicyNever))
}

func TestCertificateReconciler_createCertificateSpecs_MetricsClientOnlyWhenEnabled(t *testing.T) {
	g, _, _ := G(t)
	name := names.GetMetricsClientCertificateName(bundleTestCluster)

	disabled := &certificateReconciler{certificateDuration: time.Hour, caCertificateDuration: time.Hour}
	g.Expect(disabled.createCertificateSpecs(bundleTestHCP, bundleTestCluster)).NotTo(HaveKey(name))

	enabled := &certificateReconciler{
		certificateDuration:             time.Hour,
		caCertificateDuration:           time.Hour,
		metricsClientCertificateEnabled: true,
	}
	specs := enabled.createCertificateSpecs(bundleTestHCP, bundleTestCluster)
	g.Expect(specs).To(HaveKey(name))
	g.Expect(*specs[name].spec.CommonName).To(Equal(importcycle.MetricsClientUsername))
	g.Expect(*specs[name].spec.SecretName).To(Equal(names.GetMetricsClientCertificateSecretName(bundleTestCluster)))
	g.Expect(specs[name].spec.Usages).To(ContainElement(certmanagerv1.UsageClientAuth))
	g.Expect(specs[name].customLabels).To(HaveKeyWithValue(names.CertificateKindLabel, "client"))
}
