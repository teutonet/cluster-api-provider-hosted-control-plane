package hostedcontrolplane

import (
	"context"
	"fmt"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagerv1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/certmanager/v1"
	certmanagermetav1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/meta/v1"
	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
)

type CertificateReconciler struct {
	certManagerClient     cmclient.Interface
	caCertificateDuration time.Duration
	certificateDuration   time.Duration
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=issuers,verbs=create;update;patch
//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=create;update;patch

func (cr *CertificateReconciler) ReconcileCACertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCACertificates",
		func(ctx context.Context, span trace.Span) error {
			createCertificateSpec := func(
				issuer *certmanagerv1ac.IssuerApplyConfiguration,
				secretName string,
				name string,
			) *certmanagerv1ac.CertificateSpecApplyConfiguration {
				return certmanagerv1ac.CertificateSpec().
					WithSecretName(secretName).
					WithIssuerRef(certmanagermetav1ac.IssuerReference().
						WithKind(*issuer.Kind).
						WithName(*issuer.Name),
					).
					WithCommonName(name).
					WithDNSNames(name).
					WithUsages(
						certmanagerv1.UsageDigitalSignature,
						certmanagerv1.UsageKeyEncipherment,
						certmanagerv1.UsageCertSign,
					).
					WithIsCA(true).
					WithDuration(metav1.Duration{Duration: cr.caCertificateDuration}).
					WithRenewBeforePercentage(certificateRenewBefore)
			}

			rootIssuer := certmanagerv1ac.Issuer(names.GetRootIssuerName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithSelfSigned(certmanagerv1ac.SelfSignedIssuer()),
				)

			issuerClient := cr.certManagerClient.CertmanagerV1().Issuers(hostedControlPlane.Namespace)
			_, err := issuerClient.Apply(ctx, rootIssuer, applyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch self-signed issuer: %w", err)
			}

			if err := cr.reconcileCertificate(ctx, hostedControlPlane,
				names.GetCACertificateName(hostedControlPlane.Name),
				createCertificateSpec(
					rootIssuer,
					names.GetCASecretName(hostedControlPlane.Name),
					"kubernetes",
				),
			); err != nil {
				return fmt.Errorf("failed to reconcile CA certificate: %w", err)
			}

			caIssuer := certmanagerv1ac.Issuer(names.GetCAIssuerName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetCASecretName(hostedControlPlane.Name)),
					),
				)

			_, err = issuerClient.Apply(ctx, caIssuer, applyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch CA issuer: %w", err)
			}

			if err := cr.reconcileCertificate(ctx, hostedControlPlane,
				names.GetFrontProxyCAName(hostedControlPlane.Name),
				createCertificateSpec(
					caIssuer,
					names.GetFrontProxyCASecretName(hostedControlPlane.Name),
					"front-proxy-ca",
				),
			); err != nil {
				return fmt.Errorf("failed to reconcile front-proxy CA certificate: %w", err)
			}

			frontProxyCAIssuer := certmanagerv1ac.Issuer(
				names.GetFrontProxyCAName(hostedControlPlane.Name), hostedControlPlane.Namespace,
			).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetFrontProxyCASecretName(hostedControlPlane.Name)),
					),
				)

			_, err = issuerClient.
				Apply(ctx, frontProxyCAIssuer, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch front-proxy CA issuer: %w", err)
		},
	)
}

func (cr *CertificateReconciler) ReconcileCertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCertificates",
		func(ctx context.Context, span trace.Span) error {
			clusterDomain := "cluster.local"
			if cluster.Spec.ClusterNetwork != nil && cluster.Spec.ClusterNetwork.ServiceDomain != "" {
				clusterDomain = cluster.Spec.ClusterNetwork.ServiceDomain
			}

			createCertificateSpec := func(
				caIssuerName string,
				commonName string,
				secretName string,
				additionalUsages ...certmanagerv1.KeyUsage,
			) *certmanagerv1ac.CertificateSpecApplyConfiguration {
				usages := []certmanagerv1.KeyUsage{
					certmanagerv1.UsageDigitalSignature,
				}
				usages = append(usages, additionalUsages...)
				return certmanagerv1ac.CertificateSpec().
					WithSecretName(secretName).
					WithIssuerRef(certmanagermetav1ac.IssuerReference().
						WithKind(certmanagerv1.IssuerKind).
						WithName(caIssuerName),
					).
					WithUsages(usages...).
					WithCommonName(commonName).
					WithDuration(metav1.Duration{Duration: cr.certificateDuration}).
					WithRenewBeforePercentage(certificateRenewBefore)
			}

			certificates := []struct {
				name string
				spec *certmanagerv1ac.CertificateSpecApplyConfiguration
			}{
				{
					name: names.GetAPIServerCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						konstants.APIServerCertCommonName,
						names.GetAPIServerSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageKeyEncipherment, certmanagerv1.UsageServerAuth,
					).WithDNSNames(
						"kubernetes",
						"kubernetes.default",
						"kubernetes.default.svc",
						names.GetServiceName(hostedControlPlane.Name),
						fmt.Sprintf("%s.%s",
							names.GetServiceName(hostedControlPlane.Name),
							hostedControlPlane.Namespace,
						),
						fmt.Sprintf("%s.%s.svc",
							names.GetServiceName(hostedControlPlane.Name),
							hostedControlPlane.Namespace,
						),
						fmt.Sprintf(
							"%s.%s.svc.%s",
							names.GetServiceName(hostedControlPlane.Name),
							hostedControlPlane.Namespace,
							clusterDomain,
						),
					),
				},
				{
					name: names.GetAPIServerKubeletClientCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						konstants.APIServerKubeletClientCertCommonName,
						names.GetAPIServerKubeletClientSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageKeyEncipherment, certmanagerv1.UsageClientAuth,
					).WithSubject(certmanagerv1ac.X509Subject().
						WithOrganizations(konstants.ClusterAdminsGroupAndClusterRoleBinding),
					),
				},
				{
					name: names.GetFrontProxyCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetFrontProxyCAName(hostedControlPlane.Name),
						konstants.FrontProxyClientCertCommonName,
						names.GetFrontProxySecretName(hostedControlPlane.Name),
						certmanagerv1.UsageClientAuth,
					),
				},
				{
					name: names.GetServiceAccountCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						"service-account",
						names.GetServiceAccountSecretName(hostedControlPlane.Name),
					),
				},
				{
					name: names.GetAdminCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						"kubernetes-admin",
						names.GetAdminSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageClientAuth,
					).WithSubject(certmanagerv1ac.X509Subject().
						WithOrganizations(konstants.SystemPrivilegedGroup),
					),
				},
				{
					name: names.GetControllerManagerCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						konstants.ControllerManagerUser,
						names.GetControllerManagerSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageClientAuth,
					),
				},
				{
					name: names.GetSchedulerCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						konstants.SchedulerUser,
						names.GetSchedulerSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageClientAuth,
					),
				},
				{
					name: names.GetKonnectivityClientCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						"system:konnectivity-server",
						names.GetKonnectivityClientSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageClientAuth, certmanagerv1.UsageServerAuth, certmanagerv1.UsageCodeSigning,
					).WithSubject(certmanagerv1ac.X509Subject().
						WithOrganizations(konstants.SystemPrivilegedGroup),
					),
				},
			}

			for _, cert := range certificates {
				if err := cr.reconcileCertificate(ctx, hostedControlPlane, cert.name, cert.spec); err != nil {
					return fmt.Errorf("failed to reconcile certificate %s: %w", cert.name, err)
				}
			}

			return nil
		},
	)
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=create;update;patch

func (cr *CertificateReconciler) reconcileCertificate(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	name string,
	spec *certmanagerv1ac.CertificateSpecApplyConfiguration,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCertificate",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("CertificateName", name),
				attribute.String("CommonName", *spec.CommonName),
				attribute.String("CertificateSecretName", *spec.SecretName),
			)

			certificate := certmanagerv1ac.Certificate(name, hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(spec.WithRevisionHistoryLimit(1).
					WithSecretTemplate(certmanagerv1ac.CertificateSecretTemplate().
						WithLabels(names.GetLabels(hostedControlPlane.Name)),
					),
				)

			_, err := cr.certManagerClient.CertmanagerV1().Certificates(hostedControlPlane.Namespace).
				Apply(ctx, certificate, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch certificate: %w", err)
		},
	)
}
