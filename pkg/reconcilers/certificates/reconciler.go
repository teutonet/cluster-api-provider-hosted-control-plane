package certificates

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
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type CertificateReconciler interface {
	ReconcileCACertificates(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
	ReconcileCertificates(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
}

func NewCertificateReconciler(
	certManagerClient cmclient.Interface,
	caCertificateDuration time.Duration,
	certificateDuration time.Duration,
	konnectivityServerAudience string,
) CertificateReconciler {
	return &certificateReconciler{
		certManagerClient:          certManagerClient,
		caCertificateDuration:      caCertificateDuration,
		certificateDuration:        certificateDuration,
		certificateRenewBefore:     int32(90),
		konnectivityServerAudience: konnectivityServerAudience,
		tracer:                     tracing.GetTracer("certificates"),
	}
}

type certificateReconciler struct {
	certManagerClient          cmclient.Interface
	caCertificateDuration      time.Duration
	certificateDuration        time.Duration
	certificateRenewBefore     int32
	konnectivityServerAudience string
	tracer                     string
}

var _ CertificateReconciler = &certificateReconciler{}

var (
	errCANotReady          = fmt.Errorf("CA not ready: %w", operatorutil.ErrRequeueRequired)
	errCertificateNotReady = fmt.Errorf("certificate not ready: %w", operatorutil.ErrRequeueRequired)
)

//+kubebuilder:rbac:groups=cert-manager.io,resources=issuers,verbs=create;update;patch
//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=create;update;patch

func (cr *certificateReconciler) ReconcileCACertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, cr.tracer, "ReconcileCACertificates",
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
					WithRenewBeforePercentage(cr.certificateRenewBefore)
			}

			rootIssuer := certmanagerv1ac.Issuer(names.GetRootIssuerName(cluster), hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithSelfSigned(certmanagerv1ac.SelfSignedIssuer()),
				)

			issuerClient := cr.certManagerClient.CertmanagerV1().Issuers(hostedControlPlane.Namespace)
			_, err := issuerClient.Apply(ctx, rootIssuer, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch self-signed issuer: %w", err)
			}

			cert, err := cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
				names.GetCACertificateName(cluster),
				createCertificateSpec(
					rootIssuer,
					names.GetCASecretName(cluster),
					"kubernetes",
				),
			)
			if err != nil {
				return fmt.Errorf("failed to reconcile CA certificate: %w", err)
			}
			if !cr.isCertificateReady(cert) {
				return errCANotReady
			}

			caIssuer := certmanagerv1ac.Issuer(names.GetCAIssuerName(cluster), hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetCASecretName(cluster)),
					),
				)

			_, err = issuerClient.Apply(ctx, caIssuer, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch CA issuer: %w", err)
			}

			cert, err = cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
				names.GetFrontProxyCAName(cluster),
				createCertificateSpec(
					caIssuer,
					names.GetFrontProxyCASecretName(cluster),
					"front-proxy-ca",
				),
			)
			if err != nil {
				return fmt.Errorf("failed to reconcile front-proxy CA certificate: %w", err)
			}
			if !cr.isCertificateReady(cert) {
				return errCANotReady
			}

			frontProxyCAIssuer := certmanagerv1ac.Issuer(
				names.GetFrontProxyCAName(cluster), hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetFrontProxyCASecretName(cluster)),
					),
				)

			_, err = issuerClient.Apply(ctx, frontProxyCAIssuer, operatorutil.ApplyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch front-proxy CA issuer: %w", err)
			}

			cert, err = cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
				names.GetEtcdCAName(cluster),
				createCertificateSpec(
					caIssuer,
					names.GetEtcdCASecretName(cluster),
					"etcd-ca",
				),
			)
			if err != nil {
				return fmt.Errorf("failed to reconcile etcd CA certificate: %w", err)
			}
			if !cr.isCertificateReady(cert) {
				return errCANotReady
			}

			etcdCAIssuer := certmanagerv1ac.Issuer(
				names.GetEtcdCAName(cluster), hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithCA(certmanagerv1ac.CAIssuer().
						WithSecretName(names.GetEtcdCASecretName(cluster)),
					),
				)

			_, err = issuerClient.Apply(ctx, etcdCAIssuer, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to patch etcd CA issuer: %w", err)
		},
	)
}

func (cr *certificateReconciler) createCertificateSpecs(cluster *capiv1.Cluster) []struct {
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
			WithRenewBeforePercentage(cr.certificateRenewBefore)
	}

	etcdDNSNames := []string{
		"localhost",
	}

	dnsNames := names.GetEtcdDNSNames(cluster)
	etcdDNSNames = append(etcdDNSNames, slices.Keys(dnsNames)...)
	etcdDNSNames = append(etcdDNSNames, slices.Values(dnsNames)...)
	etcdDNSNames = append(etcdDNSNames, names.GetEtcdServiceName(cluster))
	etcdDNSNames = append(etcdDNSNames, names.GetEtcdClientServiceName(cluster))

	sort.Strings(etcdDNSNames)

	return []struct {
		name string
		spec *certmanagerv1ac.CertificateSpecApplyConfiguration
	}{
		{
			name: names.GetAPIServerCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.APIServerCertCommonName,
				names.GetAPIServerSecretName(cluster),
				certmanagerv1.UsageServerAuth,
			).WithDNSNames(
				"localhost",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.svc",
				names.GetServiceName(cluster),
				names.GetInternalServiceHost(cluster),
			).WithIPAddresses(cluster.Spec.ControlPlaneEndpoint.Host, "10.96.0.1", "127.0.0.1"),
		},
		{
			name: names.GetAPIServerKubeletClientCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.APIServerKubeletClientCertCommonName,
				names.GetAPIServerKubeletClientSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.ClusterAdminsGroupAndClusterRoleBinding),
			),
		},
		{
			name: names.GetFrontProxyCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetFrontProxyCAName(cluster),
				konstants.FrontProxyClientCertCommonName,
				names.GetFrontProxySecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		{
			name: names.GetServiceAccountCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				"service-account",
				names.GetServiceAccountSecretName(cluster),
			),
		},
		{
			name: names.GetAdminCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				"kubernetes-admin",
				names.GetAdminKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		{
			name: names.GetControllerManagerKubeconfigCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.ControllerManagerUser,
				names.GetControllerManagerKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		{
			name: names.GetSchedulerKubeconfigCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.SchedulerUser,
				names.GetSchedulerKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		{
			name: names.GetKonnectivityClientKubeconfigCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				cr.konnectivityServerAudience,
				names.GetKonnectivityClientKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth, certmanagerv1.UsageServerAuth, certmanagerv1.UsageCodeSigning,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		{
			name: names.GetControllerKubeconfigCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				"system:control-plane-controller",
				names.GetControllerKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		{
			name: names.GetEtcdServerCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"etcd-server",
				names.GetEtcdServerSecretName(cluster),
				certmanagerv1.UsageServerAuth, certmanagerv1.UsageClientAuth,
			).WithDNSNames(etcdDNSNames...).WithIPAddresses("127.0.0.1"),
		},
		{
			name: names.GetEtcdPeerCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"etcd-peer",
				names.GetEtcdPeerSecretName(cluster),
				certmanagerv1.UsageServerAuth, certmanagerv1.UsageClientAuth,
			).WithDNSNames(etcdDNSNames...).WithIPAddresses("127.0.0.1"),
		},
		{
			name: names.GetEtcdAPIServerClientCertificateName(cluster),
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"apiserver-etcd-client",
				names.GetEtcdAPIServerClientSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
	}
}

func (cr *certificateReconciler) ReconcileCertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, cr.tracer, "ReconcileCertificates",
		func(ctx context.Context, span trace.Span) error {
			needsRequeue := false
			for _, cert := range cr.createCertificateSpecs(cluster) {
				if certObj, err := cr.reconcileCertificate(ctx,
					hostedControlPlane, cluster,
					cert.name, cert.spec,
				); err != nil {
					return fmt.Errorf("failed to reconcile certificate: %w", err)
				} else if !cr.isCertificateReady(certObj) {
					needsRequeue = true
				}
			}

			if needsRequeue {
				return errCertificateNotReady
			}
			return nil
		},
	)
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=create;update;patch

func (cr *certificateReconciler) reconcileCertificate(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	name string,
	spec *certmanagerv1ac.CertificateSpecApplyConfiguration,
) (*certmanagerv1.Certificate, error) {
	return tracing.WithSpan(ctx, cr.tracer, "ReconcileCertificate",
		func(ctx context.Context, span trace.Span) (*certmanagerv1.Certificate, error) {
			span.SetAttributes(
				attribute.String("CertificateName", name),
				attribute.String("CommonName", *spec.CommonName),
				attribute.String("CertificateSecretName", *spec.SecretName),
			)

			certificate := certmanagerv1ac.Certificate(name, hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(spec.WithRevisionHistoryLimit(1).
					WithSecretTemplate(certmanagerv1ac.CertificateSecretTemplate().
						WithLabels(names.GetControlPlaneLabels(cluster, "")),
					),
				)

			result, err := cr.certManagerClient.CertmanagerV1().Certificates(hostedControlPlane.Namespace).
				Apply(ctx, certificate, operatorutil.ApplyOptions)
			if err != nil {
				return nil, fmt.Errorf("failed to patch certificate: %w", err)
			}
			return result, nil
		},
	)
}

func (cr *certificateReconciler) isCertificateReady(
	certificate *certmanagerv1.Certificate,
) bool {
	return slices.ContainsBy(certificate.Status.Conditions, func(condition certmanagerv1.CertificateCondition) bool {
		return condition.Type == certmanagerv1.CertificateConditionReady &&
			condition.Status == certmanagermetav1.ConditionTrue
	})
}
