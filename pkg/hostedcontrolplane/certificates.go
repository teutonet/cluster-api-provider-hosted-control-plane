package hostedcontrolplane

import (
	"context"
	"fmt"
	"sort"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagermetav1 "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	certmanagerv1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/certmanager/v1"
	certmanagermetav1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/meta/v1"
	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type CertificateReconciler struct {
	certManagerClient     cmclient.Interface
	caCertificateDuration time.Duration
	certificateDuration   time.Duration
}

var (
	ErrCANotReady          = fmt.Errorf("CA not ready: %w", ErrRequeueRequired)
	ErrCertificateNotReady = fmt.Errorf("certificate not ready: %w", ErrRequeueRequired)
)

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
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithSelfSigned(certmanagerv1ac.SelfSignedIssuer()),
				)

			issuerClient := cr.certManagerClient.CertmanagerV1().Issuers(hostedControlPlane.Namespace)
			_, err := issuerClient.Apply(ctx, rootIssuer, applyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch self-signed issuer: %w", err)
			}

			cert, err := cr.reconcileCertificate(ctx, hostedControlPlane,
				names.GetCACertificateName(hostedControlPlane.Name),
				createCertificateSpec(
					rootIssuer,
					names.GetCASecretName(hostedControlPlane.Name),
					"kubernetes",
				),
			)
			if err != nil {
				return fmt.Errorf("failed to reconcile CA certificate: %w", err)
			}
			if !cr.isCertificateReady(cert) {
				return ErrCANotReady
			}

			caIssuer := certmanagerv1ac.Issuer(names.GetCAIssuerName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name, "")).
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

			cert, err = cr.reconcileCertificate(ctx, hostedControlPlane,
				names.GetFrontProxyCAName(hostedControlPlane.Name),
				createCertificateSpec(
					caIssuer,
					names.GetFrontProxyCASecretName(hostedControlPlane.Name),
					"front-proxy-ca",
				),
			)
			if err != nil {
				return fmt.Errorf("failed to reconcile front-proxy CA certificate: %w", err)
			}
			if !cr.isCertificateReady(cert) {
				return ErrCANotReady
			}

			frontProxyCAIssuer := certmanagerv1ac.Issuer(
				names.GetFrontProxyCAName(hostedControlPlane.Name), hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetFrontProxyCASecretName(hostedControlPlane.Name)),
					),
				)

			_, err = issuerClient.Apply(ctx, frontProxyCAIssuer, applyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch front-proxy CA issuer: %w", err)
			}

			cert, err = cr.reconcileCertificate(ctx, hostedControlPlane,
				names.GetEtcdCAName(hostedControlPlane.Name),
				createCertificateSpec(
					caIssuer,
					names.GetEtcdCASecretName(hostedControlPlane.Name),
					"etcd-ca",
				),
			)
			if err != nil {
				return fmt.Errorf("failed to reconcile etcd CA certificate: %w", err)
			}
			if !cr.isCertificateReady(cert) {
				return ErrCANotReady
			}

			etcdCAIssuer := certmanagerv1ac.Issuer(
				names.GetEtcdCAName(hostedControlPlane.Name), hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetEtcdCASecretName(hostedControlPlane.Name)),
					),
				)

			_, err = issuerClient.Apply(ctx, etcdCAIssuer, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch etcd CA issuer: %w", err)
		},
	)
}

func (cr *CertificateReconciler) createCertificateSpecs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) []struct {
	name string
	spec *certmanagerv1ac.CertificateSpecApplyConfiguration
} {
	createCertificateSpec := func(
		caIssuerName string,
		commonName string,
		secretName string,
		additionalUsages ...certmanagerv1.KeyUsage,
	) *certmanagerv1ac.CertificateSpecApplyConfiguration {
		usages := []certmanagerv1.KeyUsage{
			certmanagerv1.UsageKeyEncipherment,
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

	etcdDNSNames := []string{
		"localhost",
	}

	dnsNames := names.GetEtcdDNSNames(hostedControlPlane)
	etcdDNSNames = append(etcdDNSNames, slices.Keys(dnsNames)...)
	etcdDNSNames = append(etcdDNSNames, slices.Values(dnsNames)...)
	etcdDNSNames = append(etcdDNSNames, names.GetEtcdServiceName(hostedControlPlane.Name))

	sort.Strings(etcdDNSNames)

	return []struct {
		name string
		spec *certmanagerv1ac.CertificateSpecApplyConfiguration
	}{
		{
			name: names.GetAPIServerCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetCAIssuerName(hostedControlPlane.Name),
				konstants.APIServerCertCommonName,
				names.GetAPIServerSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageServerAuth,
			).WithDNSNames(
				"localhost",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.svc",
				names.GetServiceName(hostedControlPlane.Name),
				fmt.Sprintf("%s.%s",
					names.GetServiceName(hostedControlPlane.Name),
					hostedControlPlane.Namespace,
				),
				names.GetInternalServiceHost(hostedControlPlane.Name, hostedControlPlane.Namespace),
				cluster.Spec.ControlPlaneEndpoint.Host,
			),
		},
		{
			name: names.GetAPIServerKubeletClientCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetCAIssuerName(hostedControlPlane.Name),
				konstants.APIServerKubeletClientCertCommonName,
				names.GetAPIServerKubeletClientSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth,
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
				names.GetAdminKubeconfigCertificateSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		{
			name: names.GetControllerManagerKubeconfigCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetCAIssuerName(hostedControlPlane.Name),
				konstants.ControllerManagerUser,
				names.GetControllerManagerKubeconfigCertificateSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth,
			),
		},
		{
			name: names.GetSchedulerKubeconfigCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetCAIssuerName(hostedControlPlane.Name),
				konstants.SchedulerUser,
				names.GetSchedulerKubeconfigCertificateSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth,
			),
		},
		{
			name: names.GetKonnectivityClientKubeconfigCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetCAIssuerName(hostedControlPlane.Name),
				"system:konnectivity-server",
				names.GetKonnectivityClientKubeconfigCertificateSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth, certmanagerv1.UsageServerAuth, certmanagerv1.UsageCodeSigning,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		{
			name: names.GetControllerKubeconfigCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetCAIssuerName(hostedControlPlane.Name),
				"system:controller",
				names.GetControllerKubeconfigCertificateSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		{
			name: names.GetEtcdServerCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetEtcdCAName(hostedControlPlane.Name),
				"etcd-server",
				names.GetEtcdServerSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageServerAuth, certmanagerv1.UsageClientAuth,
			).WithDNSNames(etcdDNSNames...).WithIPAddresses("127.0.0.1"),
		},
		{
			name: names.GetEtcdPeerCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetEtcdCAName(hostedControlPlane.Name),
				"etcd-peer",
				names.GetEtcdPeerSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageServerAuth, certmanagerv1.UsageClientAuth,
			).WithDNSNames(etcdDNSNames...).WithIPAddresses("127.0.0.1"),
		},
		{
			name: names.GetEtcdAPIServerClientCertificateName(hostedControlPlane.Name),
			spec: createCertificateSpec(
				names.GetEtcdCAName(hostedControlPlane.Name),
				"apiserver-etcd-client",
				names.GetEtcdAPIServerClientSecretName(hostedControlPlane.Name),
				certmanagerv1.UsageClientAuth,
			).WithDNSNames(names.GetServiceName(hostedControlPlane.Name)),
		},
	}
}

func (cr *CertificateReconciler) ReconcileCertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCertificates",
		func(ctx context.Context, span trace.Span) error {
			for _, cert := range cr.createCertificateSpecs(hostedControlPlane, cluster) {
				if certObj, err := cr.reconcileCertificate(ctx, hostedControlPlane, cert.name, cert.spec); err != nil {
					return fmt.Errorf("failed to reconcile certificate: %w", err)
				} else if !cr.isCertificateReady(certObj) {
					return ErrCertificateNotReady
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
) (*certmanagerv1.Certificate, error) {
	return tracing.WithSpan(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCertificate",
		func(ctx context.Context, span trace.Span) (*certmanagerv1.Certificate, error) {
			span.SetAttributes(
				attribute.String("CertificateName", name),
				attribute.String("CommonName", *spec.CommonName),
				attribute.String("CertificateSecretName", *spec.SecretName),
			)

			certificate := certmanagerv1ac.Certificate(name, hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(spec.WithRevisionHistoryLimit(1).
					WithSecretTemplate(certmanagerv1ac.CertificateSecretTemplate().
						WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name, "")),
					),
				)

			result, err := cr.certManagerClient.CertmanagerV1().Certificates(hostedControlPlane.Namespace).
				Apply(ctx, certificate, applyOptions)
			if err != nil {
				return nil, fmt.Errorf("failed to patch certificate: %w", err)
			}
			return result, nil
		},
	)
}

func (cr *CertificateReconciler) isCertificateReady(
	certificate *certmanagerv1.Certificate,
) bool {
	return slices.ContainsBy(certificate.Status.Conditions, func(condition certmanagerv1.CertificateCondition) bool {
		return condition.Type == certmanagerv1.CertificateConditionReady &&
			condition.Status == certmanagermetav1.ConditionTrue
	})
}
