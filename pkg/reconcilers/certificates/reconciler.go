package certificates

import (
	"bytes"
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
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/emit"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type CertificateReconciler interface {
	ReconcileCACertificates(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
	ReconcileCABundles(
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
	kubernetesClient kubernetes.Interface,
	kubernetesServiceIP net.IP,
	rootCACertificateDuration time.Duration,
	caCertificateDuration time.Duration,
	certificateDuration time.Duration,
	konnectivityServerAudience string,
) CertificateReconciler {
	return &certificateReconciler{
		certManagerClient:          certManagerClient,
		kubernetesClient:           kubernetesClient,
		kubernetesServiceIP:        kubernetesServiceIP,
		rootCACertificateDuration:  rootCACertificateDuration,
		caCertificateDuration:      caCertificateDuration,
		certificateDuration:        certificateDuration,
		certificateRenewBefore:     int32(50),
		konnectivityServerAudience: konnectivityServerAudience,
		tracer:                     tracing.GetTracer("certificates"),
	}
}

type certificateReconciler struct {
	certManagerClient          cmclient.Interface
	kubernetesClient           kubernetes.Interface
	kubernetesServiceIP        net.IP
	rootCACertificateDuration  time.Duration
	caCertificateDuration      time.Duration
	certificateDuration        time.Duration
	certificateRenewBefore     int32
	konnectivityServerAudience string
	tracer                     string
}

var _ CertificateReconciler = &certificateReconciler{}

type certificateSpec struct {
	kind         string
	spec         *certmanagerv1ac.CertificateSpecApplyConfiguration
	customLabels map[string]string
}

// caDefinition is the single source of truth for a control plane CA: its certificate/secret/bundle
// naming, the issuer it produces, and the (already-existing) issuer it's signed by. ReconcileCACertificates
// and ReconcileCABundles both iterate this same list so a newly added CA can't be forgotten in one of
// them.
type caDefinition struct {
	kind             string
	commonName       string
	certificateName  func(cluster *capiv2.Cluster) string
	secretName       func(cluster *capiv2.Cluster) string
	bundleSecretName func(cluster *capiv2.Cluster) string
	issuerName       func(cluster *capiv2.Cluster) string
	parentIssuerName func(cluster *capiv2.Cluster) string
	duration         func(cr *certificateReconciler) time.Duration
}

var caDefinitions = []caDefinition{
	{
		kind:             "kubernetes CA",
		commonName:       "kubernetes",
		certificateName:  names.GetCACertificateName,
		secretName:       names.GetCASecretName,
		bundleSecretName: names.GetCABundleSecretName,
		issuerName:       names.GetCAIssuerName,
		parentIssuerName: names.GetRootIssuerName,
		duration:         func(cr *certificateReconciler) time.Duration { return cr.rootCACertificateDuration },
	},
	{
		kind:             "etcd CA",
		commonName:       "etcd-ca",
		certificateName:  names.GetEtcdCAName,
		secretName:       names.GetEtcdCASecretName,
		bundleSecretName: names.GetEtcdCABundleSecretName,
		issuerName:       names.GetEtcdCAName,
		parentIssuerName: names.GetCAIssuerName,
		duration:         func(cr *certificateReconciler) time.Duration { return cr.caCertificateDuration },
	},
	{
		kind:             "front-proxy CA",
		commonName:       "front-proxy-ca",
		certificateName:  names.GetFrontProxyCAName,
		secretName:       names.GetFrontProxyCASecretName,
		bundleSecretName: names.GetFrontProxyCABundleSecretName,
		issuerName:       names.GetFrontProxyCAName,
		parentIssuerName: names.GetCAIssuerName,
		duration:         func(cr *certificateReconciler) time.Duration { return cr.caCertificateDuration },
	},
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

			rootIssuerAC := cr.createIssuer(hostedControlPlane, cluster, names.GetRootIssuerName(cluster), "")

			rootIssuer, err := issuerClient.Apply(ctx, rootIssuerAC, operatorutil.ApplyOptions)
			if err != nil {
				return "", fmt.Errorf("failed to patch self-signed issuer: %w", err)
			}
			if !cr.isIssuerReady(rootIssuer) {
				return "root issuer not ready", nil
			}

			var notReadyReasons []string
			for _, def := range caDefinitions {
				notReady, err := cr.reconcileCA(ctx, hostedControlPlane, cluster, def)
				if err != nil {
					return "", err
				}
				if notReady != "" {
					notReadyReasons = append(notReadyReasons, notReady)
				}
			}

			return strings.Join(notReadyReasons, ","), nil
		},
	)
}

// reconcileCA reconciles a CA certificate issued by def's parent issuer, then reconciles the issuer it
// in turn produces so certificates one level below can be signed by it. The parent issuer doesn't need
// to be ready yet: cert-manager leaves the certificate Pending until it is, which reconcileCertificate
// reports as not-ready like any other certificate.
func (cr *certificateReconciler) reconcileCA(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	def caDefinition,
) (string, error) {
	certificate, ready, err := cr.reconcileCertificate(ctx, hostedControlPlane, cluster,
		def.certificateName(cluster),
		certificateSpec{
			kind: def.kind,
			spec: cr.createCertificateSpec(def.parentIssuerName(cluster), def.commonName, def.secretName(cluster), true).
				WithDuration(metav1.Duration{Duration: def.duration(cr)}),
			customLabels: map[string]string{
				names.CertificateKindLabel: string(names.CACertificateKind),
			},
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to reconcile %s certificate: %w", def.kind, err)
	}
	if !ready {
		return fmt.Sprintf("%s certificate not ready", def.kind), nil
	}

	issuerAC := cr.createIssuer(hostedControlPlane, cluster, def.issuerName(cluster), certificate.Spec.SecretName)

	issuer, err := cr.certManagerClient.CertmanagerV1().Issuers(hostedControlPlane.Namespace).
		Apply(ctx, issuerAC, operatorutil.ApplyOptions)
	if err != nil {
		return "", fmt.Errorf("failed to patch %s issuer: %w", def.kind, err)
	}
	if !cr.isIssuerReady(issuer) {
		return fmt.Sprintf("%s issuer not ready", def.kind), nil
	}

	return "", nil
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

const (
	caBundleCurrentCertKey = "current.crt"
	caBundleOldCertKey     = "old.crt"
)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;patch

func (cr *certificateReconciler) ReconcileCABundles(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, cr.tracer, "ReconcileCABundles",
		func(ctx context.Context, _ trace.Span) (string, error) {
			var notReadyReasons []string
			for _, def := range caDefinitions {
				notReady, err := cr.reconcileCABundle(ctx, hostedControlPlane, cluster,
					def.secretName(cluster),
					def.bundleSecretName(cluster),
				)
				if err != nil {
					return "", fmt.Errorf("failed to reconcile %s bundle: %w", def.kind, err)
				}
				if notReady != "" {
					notReadyReasons = append(notReadyReasons, notReady)
				}
			}

			return strings.Join(notReadyReasons, ","), nil
		},
	)
}

func (cr *certificateReconciler) reconcileCABundle(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	caSecretName string,
	caBundleSecretName string,
) (string, error) {
	return tracing.WithSpan(ctx, cr.tracer, "ReconcileCABundle",
		func(ctx context.Context, span trace.Span) (string, error) {
			span.SetAttributes(
				attribute.String("caBundle.caSecretName", caSecretName),
				attribute.String("caBundle.bundleSecretName", caBundleSecretName),
			)

			secretsClient := cr.kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace)

			realCASecret, err := secretsClient.Get(ctx, caSecretName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return "CA secret not yet available", nil
			}
			if err != nil {
				return "", fmt.Errorf("failed to get CA secret: %w", err)
			}
			currentCACert := realCASecret.Data[konstants.CACertName]
			if len(currentCACert) == 0 {
				return "CA secret not yet populated", nil
			}

			oldCACert := []byte{}
			bundleSecret, err := secretsClient.Get(ctx, caBundleSecretName, metav1.GetOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return "", fmt.Errorf("failed to get CA bundle secret: %w", err)
			}
			if err == nil {
				bundleCurrentCert := bundleSecret.Data[caBundleCurrentCertKey]
				if bytes.Equal(bundleCurrentCert, currentCACert) {
					return "", nil // no rotation, nothing to do
				}
				oldCACert = bundleCurrentCert
			}

			caBundlePEM := append(append([]byte{}, oldCACert...), currentCACert...)

			bundleSecretAC := corev1ac.Secret(caBundleSecretName, hostedControlPlane.Namespace).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithData(map[string][]byte{
					caBundleCurrentCertKey: currentCACert,
					caBundleOldCertKey:     oldCACert,
					konstants.CACertName:   caBundlePEM,
				})

			if _, err = secretsClient.Apply(ctx, bundleSecretAC, operatorutil.ApplyOptions); err != nil {
				return "", fmt.Errorf("failed to apply CA bundle secret: %w", err)
			}
			return "", nil
		},
	)
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
							emit.Info(ctx, emit.SinkRecorder,
								&cert,
								"CertificateDeleted",
								"CertificateDeleted",
								"Deleted orphaned certificate",
								"name", cert.Name,
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
