package certificates

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
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
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type CertificateReconciler interface {
	ReconcileCACertificates(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
	ReconcileCertificates(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewCertificateReconciler(
	certManagerClient cmclient.Interface,
	kubernetesServiceIP net.IP,
	caCertificateDuration time.Duration,
	certificateDuration time.Duration,
	konnectivityServerAudience string,
	recorder recorder.Recorder,
) CertificateReconciler {
	return &certificateReconciler{
		certManagerClient:          certManagerClient,
		kubernetesServiceIP:        kubernetesServiceIP,
		caCertificateDuration:      caCertificateDuration,
		certificateDuration:        certificateDuration,
		certificateRenewBefore:     int32(50),
		konnectivityServerAudience: konnectivityServerAudience,
		recorder:                   recorder,
		tracer:                     tracing.GetTracer("certificates"),
	}
}

type certificateReconciler struct {
	certManagerClient          cmclient.Interface
	kubernetesServiceIP        net.IP
	caCertificateDuration      time.Duration
	certificateDuration        time.Duration
	certificateRenewBefore     int32
	konnectivityServerAudience string
	recorder                   recorder.Recorder
	tracer                     string
}

var _ CertificateReconciler = &certificateReconciler{}

type certificateSpec struct {
	kind         string
	username     string // only set for additional kubeconfigs, used for labeling
	spec         *certmanagerv1ac.CertificateSpecApplyConfiguration
	customLabels map[string]string
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=issuers,verbs=create;update;patch

func (cr *certificateReconciler) ReconcileCACertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, cr.tracer, "ReconcileCACertificates",
		func(ctx context.Context, span trace.Span) (string, error) {
			issuerClient := cr.certManagerClient.CertmanagerV1().Issuers(hostedControlPlane.Namespace)
			createCertificateSpec := func(
				issuer *certmanagerv1.Issuer,
				name string,
				commonName string,
				secretName string,
				additionalUsages ...certmanagerv1.KeyUsage,
			) certificateSpec {
				return certificateSpec{
					kind: name,
					spec: cr.createCertificateSpec(
						issuer.Name,
						commonName,
						secretName,
						true,
						additionalUsages...,
					),
					customLabels: map[string]string{
						names.CertificateKindLabel: string(names.CACertificateKind),
					},
				}
			}

			rootIssuerAC := cr.createIssuer(
				hostedControlPlane,
				cluster,
				names.GetRootIssuerName(cluster),
				"",
			)

			rootIssuer, err := issuerClient.Apply(ctx, rootIssuerAC, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to patch self-signed issuer: %w", err)
			}
			if !cr.isIssuerReady(rootIssuer) {
				return "root issuer not ready", nil
			}

			kubernetesCACertificate, ready, err := cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
				names.GetCACertificateName(cluster),
				createCertificateSpec(
					rootIssuer,
					names.GetCACertificateName(cluster),
					"kubernetes",
					names.GetCASecretName(cluster),
				),
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile CA certificate: %w", err)
			}
			if !ready {
				return "kubernetes CA certificate not ready", nil
			}

			kubernetesCAIssuerAC := cr.createIssuer(
				hostedControlPlane,
				cluster,
				names.GetCAIssuerName(cluster),
				kubernetesCACertificate.Spec.SecretName,
			)

			kubernetesCAIssuer, err := issuerClient.Apply(ctx, kubernetesCAIssuerAC, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to patch CA issuer: %w", err)
			}
			if !cr.isIssuerReady(kubernetesCAIssuer) {
				return "kubernetes CA issuer not ready", nil
			}

			var notReadyReasons []string

			frontProxyCACertificate, ready, err := cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
				names.GetFrontProxyCAName(cluster),
				createCertificateSpec(
					kubernetesCAIssuer,
					names.GetFrontProxyCAName(cluster),
					"front-proxy-ca",
					names.GetFrontProxyCASecretName(cluster),
				),
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile front-proxy CA certificate: %w", err)
			}
			if !ready {
				notReadyReasons = append(notReadyReasons, "front-proxy CA certificate not ready")
			} else {
				frontProxyCAIssuerAC := cr.createIssuer(
					hostedControlPlane,
					cluster,
					names.GetFrontProxyCAName(cluster),
					frontProxyCACertificate.Spec.SecretName,
				)

				if frontProxyCAIssuer, err := issuerClient.Apply(ctx, frontProxyCAIssuerAC, operatorutil.ApplyOptions); err != nil {
					return "", fmt.Errorf("failed to patch front-proxy CA issuer: %w", err)
				} else if !cr.isIssuerReady(frontProxyCAIssuer) {
					notReadyReasons = append(notReadyReasons, "front-proxy CA issuer not ready")
				}
			}

			etcdCACertificate, ready, err := cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
				names.GetEtcdCAName(cluster),
				createCertificateSpec(
					kubernetesCAIssuer,
					names.GetEtcdCAName(cluster),
					"etcd-ca",
					names.GetEtcdCASecretName(cluster),
				),
			)
			if err != nil {
				return "", fmt.Errorf("failed to reconcile etcd CA certificate: %w", err)
			}
			if !ready {
				notReadyReasons = append(notReadyReasons, "etcd CA certificate not ready")
			} else {
				etcdCAIssuerAC := cr.createIssuer(
					hostedControlPlane,
					cluster,
					names.GetEtcdCAName(cluster),
					etcdCACertificate.Spec.SecretName,
				)

				if etcdCAIssuer, err := issuerClient.Apply(ctx, etcdCAIssuerAC, operatorutil.ApplyOptions); err != nil {
					return "", fmt.Errorf("failed to patch etcd CA issuer: %w", err)
				} else if !cr.isIssuerReady(etcdCAIssuer) {
					notReadyReasons = append(notReadyReasons, "etcd CA issuer not ready")
				}
			}

			return strings.Join(notReadyReasons, ","), nil
		},
	)
}

func (cr *certificateReconciler) createIssuer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	name string,
	issuerSecretName string,
) *certmanagerv1ac.IssuerApplyConfiguration {
	spec := certmanagerv1ac.IssuerSpec()
	if issuerSecretName == "" {
		spec = spec.WithSelfSigned(certmanagerv1ac.SelfSignedIssuer())
	} else {
		spec = spec.WithCA(certmanagerv1ac.CAIssuer().
			WithSecretName(issuerSecretName),
		)
	}
	return certmanagerv1ac.Issuer(name, hostedControlPlane.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, "")).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(spec)
}

func (cr *certificateReconciler) createCertificateSpec(
	caIssuerName string,
	commonName string,
	secretName string,
	isCA bool,
	additionalUsages ...certmanagerv1.KeyUsage,
) *certmanagerv1ac.CertificateSpecApplyConfiguration {
	usages := []certmanagerv1.KeyUsage{
		certmanagerv1.UsageKeyEncipherment,
		certmanagerv1.UsageDigitalSignature,
	}
	usages = append(usages, additionalUsages...)
	if isCA {
		usages = append(usages, certmanagerv1.UsageCertSign)
	}

	return certmanagerv1ac.CertificateSpec().
		WithSecretName(secretName).
		WithIssuerRef(certmanagermetav1ac.IssuerReference().
			WithKind(certmanagerv1.IssuerKind).
			WithName(caIssuerName),
		).
		WithUsages(usages...).
		WithIsCA(isCA).
		WithCommonName(commonName).
		WithDuration(metav1.Duration{Duration: slices.Ternary(isCA, cr.caCertificateDuration, cr.certificateDuration)}).
		WithRenewBeforePercentage(cr.certificateRenewBefore)
}

func (cr *certificateReconciler) createCertificateSpecs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) map[string]certificateSpec {
	createCertificateSpec := func(
		caIssuerName string,
		commonName string,
		secretName string,
		additionalUsages ...certmanagerv1.KeyUsage,
	) *certmanagerv1ac.CertificateSpecApplyConfiguration {
		return cr.createCertificateSpec(
			caIssuerName,
			commonName,
			secretName,
			false,
			additionalUsages...,
		)
	}

	etcdDNSNames := []string{
		"localhost",
	}

	dnsNames := names.GetEtcdDNSNames(cluster)
	etcdDNSNames = append(etcdDNSNames, slices.Keys(dnsNames)...)
	etcdDNSNames = append(etcdDNSNames, slices.Values(dnsNames)...)
	etcdDNSNames = append(etcdDNSNames, names.GetEtcdServiceName(cluster))
	etcdDNSNames = append(etcdDNSNames, names.GetEtcdClientServiceDNSName(cluster))

	sort.Strings(etcdDNSNames)

	specs := map[string]certificateSpec{
		names.GetAPIServerCertificateName(cluster): {
			kind: "APIServer",
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
				cluster.Spec.ControlPlaneEndpoint.Host,
				names.GetKonnectivityServerHost(cluster),
				names.GetServiceName(cluster),
				names.GetInternalServiceHost(cluster),
			).WithIPAddresses(hostedControlPlane.Status.LegacyIP, cr.kubernetesServiceIP.String(), "127.0.0.1"),
		},
		names.GetAPIServerKubeletClientCertificateName(cluster): {
			kind: "APIServerKubeletClient",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.APIServerKubeletClientCertCommonName,
				names.GetAPIServerKubeletClientSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.ClusterAdminsGroupAndClusterRoleBinding),
			),
		},
		names.GetFrontProxyCertificateName(cluster): {
			kind: "FrontProxy",
			spec: createCertificateSpec(
				names.GetFrontProxyCAName(cluster),
				konstants.FrontProxyClientCertCommonName,
				names.GetFrontProxySecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		names.GetServiceAccountCertificateName(cluster): {
			kind: "ServiceAccount",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				"service-account",
				names.GetServiceAccountSecretName(cluster),
			),
		},
		names.GetAdminCertificateName(cluster): {
			kind: "Admin",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				"kubernetes-admin",
				names.GetAdminKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
			customLabels: names.GetKubeconfigLabels("kubernetes-admin"),
		},
		names.GetControllerManagerKubeconfigCertificateName(cluster): {
			kind: "ControllerManager",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.ControllerManagerUser,
				names.GetControllerManagerKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		names.GetSchedulerKubeconfigCertificateName(cluster): {
			kind: "Scheduler",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				konstants.SchedulerUser,
				names.GetSchedulerKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		names.GetKonnectivityClientKubeconfigCertificateName(cluster): {
			kind: "KonnectivityClient",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				cr.konnectivityServerAudience,
				names.GetKonnectivityClientKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth, certmanagerv1.UsageServerAuth, certmanagerv1.UsageCodeSigning,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
		},
		names.GetControlPlaneControllerKubeconfigCertificateName(cluster): {
			kind: "ControlPlaneController",
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				"system:control-plane-controller",
				names.GetControlPlaneControllerKubeconfigCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			).WithSubject(certmanagerv1ac.X509Subject().
				WithOrganizations(konstants.SystemPrivilegedGroup),
			),
			customLabels: names.GetKubeconfigLabel(),
		},
		names.GetEtcdServerCertificateName(cluster): {
			kind: "EtcdServer",
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"etcd-server",
				names.GetEtcdServerSecretName(cluster),
				certmanagerv1.UsageServerAuth, certmanagerv1.UsageClientAuth,
			).WithDNSNames(etcdDNSNames...).WithIPAddresses("127.0.0.1"),
		},
		names.GetEtcdPeerCertificateName(cluster): {
			kind: "EtcdPeer",
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"etcd-peer",
				names.GetEtcdPeerSecretName(cluster),
				certmanagerv1.UsageServerAuth, certmanagerv1.UsageClientAuth,
			).WithDNSNames(etcdDNSNames...).WithIPAddresses("127.0.0.1"),
		},
		names.GetEtcdAPIServerClientCertificateName(cluster): {
			kind: "EtcdAPIServerClient",
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"apiserver-etcd-client",
				names.GetEtcdAPIServerClientCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
		names.GetEtcdControllerClientCertificateName(cluster): {
			kind: "EtcdControllerClient",
			spec: createCertificateSpec(
				names.GetEtcdCAName(cluster),
				"controller-etcd-client",
				names.GetEtcdControllerClientCertificateSecretName(cluster),
				certmanagerv1.UsageClientAuth,
			),
		},
	}

	for username := range hostedControlPlane.Spec.CustomKubeconfigs {
		specs[names.GetCustomKubeconfigCertificateName(cluster, username)] = certificateSpec{
			kind:     fmt.Sprintf("CustomKubeconfig-%s", username),
			username: username,
			spec: createCertificateSpec(
				names.GetCAIssuerName(cluster),
				username,
				names.GetCustomKubeconfigCertificateName(cluster, username),
				certmanagerv1.UsageClientAuth,
			),
			customLabels: names.GetKubeconfigLabels(username),
		}
	}

	specs = slices.MapValues(specs, func(spec certificateSpec, _ string) certificateSpec {
		spec.customLabels = slices.Assign(spec.customLabels,
			map[string]string{
				names.CertificateKindLabel: string(names.ClientCertificateKind),
			},
		)
		return spec
	})

	return specs
}

func (cr *certificateReconciler) ReconcileCertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, cr.tracer, "ReconcileCertificates",
		func(ctx context.Context, span trace.Span) (string, error) {
			span.SetAttributes(
				attribute.String("certificate.duration", cr.certificateDuration.String()),
				attribute.Int("certificate.renewBeforePercentage", int(cr.certificateRenewBefore)),
				attribute.String("konnectivity.serverAudience", cr.konnectivityServerAudience),
			)
			var notReadyReasons []string
			for name, certificate := range cr.createCertificateSpecs(hostedControlPlane, cluster) {
				if _, ready, err := cr.reconcileCertificate(ctx,
					hostedControlPlane, cluster,
					name, certificate,
				); err != nil {
					return "", fmt.Errorf("failed to reconcile certificate %s: %w", certificate.kind, err)
				} else if !ready {
					notReadyReasons = append(notReadyReasons,
						fmt.Sprintf("certificate %s not ready", certificate.kind),
					)
				}
			}

			if err := cr.cleanupOrphanedCertificates(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to cleanup orphaned certificates: %w", err)
			}

			if len(notReadyReasons) > 0 {
				return strings.Join(notReadyReasons, ","), nil
			}
			return "", nil
		},
	)
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=create;update;patch

func (cr *certificateReconciler) reconcileCertificate(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	name string,
	certificate certificateSpec,
) (*certmanagerv1.Certificate, bool, error) {
	return tracing.WithSpan3(ctx, cr.tracer, "ReconcileCertificate",
		func(ctx context.Context, span trace.Span) (*certmanagerv1.Certificate, bool, error) {
			span.SetAttributes(
				attribute.String("certificate.name", name),
				attribute.String("certificate.commonName", *certificate.spec.CommonName),
				attribute.String("certificate.secretName", *certificate.spec.SecretName),
			)

			certificateLabels := slices.Assign(certificate.customLabels, names.GetControlPlaneLabels(cluster, ""))
			certificateAC := certmanagerv1ac.Certificate(name, hostedControlPlane.Namespace).
				WithLabels(certificateLabels).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certificate.spec.WithRevisionHistoryLimit(1).
					WithSecretTemplate(certmanagerv1ac.CertificateSecretTemplate().
						WithLabels(certificateLabels),
					),
				)

			certificate, err := cr.certManagerClient.CertmanagerV1().Certificates(*certificateAC.Namespace).
				Apply(ctx, certificateAC, operatorutil.ApplyOptions)
			if err != nil {
				return nil, false, fmt.Errorf("failed to patch certificate %s: %w", *certificateAC.Name, err)
			}

			return certificate, cr.isCertificateReady(certificate), nil
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

func (cr *certificateReconciler) isIssuerReady(
	issuer *certmanagerv1.Issuer,
) bool {
	return slices.ContainsBy(issuer.Status.Conditions, func(condition certmanagerv1.IssuerCondition) bool {
		return condition.Type == certmanagerv1.IssuerConditionReady &&
			condition.Status == certmanagermetav1.ConditionTrue
	})
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=list;delete

func (cr *certificateReconciler) cleanupOrphanedCertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) error {
	return tracing.WithSpan1(ctx, cr.tracer, "CleanupOrphanedCertificates",
		func(ctx context.Context, span trace.Span) error {
			certificateClient := cr.certManagerClient.CertmanagerV1().Certificates(hostedControlPlane.Namespace)

			certificates, err := certificateClient.List(ctx, metav1.ListOptions{
				LabelSelector: labels.SelectorFromSet(slices.Assign(
					map[string]string{
						names.CertificateKindLabel: string(names.ClientCertificateKind),
					},
					names.GetControlPlaneLabels(cluster, ""),
				)).String(),
			})
			if err != nil {
				return fmt.Errorf("failed to list certificates: %w", err)
			}

			desiredCertificateNames := slices.Keys(cr.createCertificateSpecs(hostedControlPlane, cluster))

			for _, cert := range certificates.Items {
				if !slices.Contains(desiredCertificateNames, cert.Name) {
					err := tracing.WithSpan1(ctx, cr.tracer, "DeleteOrphanedCertificate",
						func(ctx context.Context, span trace.Span) error {
							span.SetAttributes(
								attribute.String("certificate.name", cert.Name),
							)
							if err := certificateClient.Delete(
								ctx, cert.Name, metav1.DeleteOptions{},
							); err != nil && !apierrors.IsNotFound(err) {
								return fmt.Errorf("failed to delete orphaned certificate %s: %w", cert.Name, err)
							}
							cr.recorder.Normalf(
								&cert,
								"CertificateDeleted",
								"CertificateDeleted",
								"Deleted orphaned certificate %s",
								cert.Name,
							)
							return nil
						},
					)
					if err != nil {
						return err
					}
				}
			}

			return nil
		},
	)
}
