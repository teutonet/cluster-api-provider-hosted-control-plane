package certificates

import (
	"testing"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagermetav1 "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	. "github.com/onsi/gomega"
)

func TestCertificateReconciler_isIssuerReady(t *testing.T) {
	reconciler := &certificateReconciler{}
	g := NewWithT(t)

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
			result := reconciler.isIssuerReady(tt.issuer)

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestCertificateReconciler_isCertificateReady(t *testing.T) {
	reconciler := &certificateReconciler{}
	g := NewWithT(t)

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
			result := reconciler.isCertificateReady(tt.certificate)

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestCertificateReconciler_ErrorHandling_EdgeCases(t *testing.T) {
	reconciler := &certificateReconciler{}
	g := NewWithT(t)

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
