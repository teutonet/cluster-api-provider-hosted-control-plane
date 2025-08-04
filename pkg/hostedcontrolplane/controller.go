// Package hostedcontrolplane contains the controller logic for reconciling HostedControlPlane objects.
package hostedcontrolplane

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagerv1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/certmanager/v1"
	certmanagermetav1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/meta/v1"
	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apiserver/pkg/apis/apiserver"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/finalizers"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/paused"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	hostedControlPlaneReconcilerTracer = "HostedControlPlaneReconciler"
	hostedControlPlaneControllerName   = "hosted-control-plane-controller"
)

var (
	caCertificateDuration  = 1 * time.Hour
	certificateDuration    = 1 * time.Hour
	certificateRenewBefore = int32(90)
)

var applyOptions = metav1.ApplyOptions{
	FieldManager: hostedControlPlaneControllerName,
	Force:        true,
}

func getOwnerReference(hostedControlPlane *v1alpha1.HostedControlPlane) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         hostedControlPlane.APIVersion,
		Kind:               hostedControlPlane.Kind,
		Name:               hostedControlPlane.Name,
		UID:                hostedControlPlane.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

func getOwnerReferenceApplyConfiguration(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *metav1ac.OwnerReferenceApplyConfiguration {
	return metav1ac.OwnerReference().
		WithAPIVersion(hostedControlPlane.APIVersion).
		WithKind(hostedControlPlane.Kind).
		WithName(hostedControlPlane.Name).
		WithUID(hostedControlPlane.UID).
		WithController(true).
		WithBlockOwnerDeletion(true)
}

var hostedControlPlaneFinalizer = fmt.Sprintf("hcp.%s", api.GroupName)

var (
	ErrCloudNotReady            = errors.New("TeutonetesCloud is not ready")
	ErrDomainNotFound           = errors.New("OpenStack domain not found")
	ErrFlavorNotFound           = errors.New("OpenStack flavor not found")
	ErrAvailabilityZoneNotFound = errors.New("OpenStack availability zone not found")
)

type HostedControlPlaneReconciler struct {
	Client            client.Client
	KubernetesClient  kubernetes.Interface
	CertManagerClient cmclient.Interface
	Recorder          record.EventRecorder
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=watch;list
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=watch;list
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes,verbs=watch;list

func (r *HostedControlPlaneReconciler) SetupWithManager(
	mgr ctrl.Manager,
	maxConcurrentReconciles int,
	predicateLogger logr.Logger,
) error {
	return errorsUtil.IfErrErrorf("failed to setup HostedControlPlane controller: %w",
		ctrl.NewControllerManagedBy(mgr).
			WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
			For(&v1alpha1.HostedControlPlane{}).
			Owns(&v1.Deployment{}).
			Owns(&corev1.Secret{}).
			Owns(&gwv1.HTTPRoute{}).
			Watches(
				&capiv1.Cluster{},
				handler.EnqueueRequestsFromMapFunc(r.clusterToHostedControlPlane),
				builder.WithPredicates(
					predicates.ResourceIsChanged(mgr.GetScheme(), predicateLogger),
				),
			).
			Complete(r),
	)
}

func (r *HostedControlPlaneReconciler) clusterToHostedControlPlane(
	_ context.Context,
	o client.Object,
) []reconcile.Request {
	c, ok := o.(*capiv1.Cluster)
	if !ok {
		panic(fmt.Sprintf("Expected a Cluster but got a %T", c))
	}

	controlPlaneRef := c.Spec.ControlPlaneRef
	if controlPlaneRef != nil && controlPlaneRef.Kind == (&v1alpha1.HostedControlPlane{}).Kind {
		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: controlPlaneRef.Namespace,
					Name:      controlPlaneRef.Name,
				},
			},
		}
	}

	return nil
}

//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes,verbs=get;update;patch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get
//+kubebuilder:rbac:groups="",resources=events,verbs=create

func (r *HostedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	return tracing.WithSpan(ctx, hostedControlPlaneReconcilerTracer, "Reconcile",
		func(ctx context.Context, span trace.Span) (ctrl.Result, error) {
			span.SetAttributes(
				attribute.String("ReconcileID", string(controller.ReconcileIDFromContext(ctx))),
				attribute.String("Namespace", req.Namespace),
				attribute.String("Name", req.Name),
			)

			hostedControlPlane := &v1alpha1.HostedControlPlane{}
			if err := r.Client.Get(ctx, req.NamespacedName, hostedControlPlane); err != nil {
				if apierrors.IsNotFound(err) {
					return reconcile.Result{}, nil
				}
				return reconcile.Result{}, fmt.Errorf("failed to get HostedControlPlane %q: %w", req, err)
			}

			patchHelper, err := patch.NewHelper(hostedControlPlane, r.Client)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create patch helper for HostedControlPlane %q/%q: %w",
					hostedControlPlane.Namespace, hostedControlPlane.Name,
					err,
				)
			}

			defer func() {
				if err := r.updateStatus(ctx, hostedControlPlane); err != nil {
					reterr = kerrors.NewAggregate([]error{reterr, err})
				}

				r.updateV1Beta2Status(ctx, hostedControlPlane)

				if err := r.patch(ctx, patchHelper, hostedControlPlane); err != nil {
					reterr = kerrors.NewAggregate([]error{reterr, err})
				}
			}()

			cluster, err := capiutil.GetOwnerCluster(ctx, r.Client, hostedControlPlane.ObjectMeta)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to retrieve owner Cluster: %w", err)
			}
			if cluster == nil {
				span.AddEvent("Cluster Controller has not yet set OwnerRef")
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			span.SetAttributes(
				attribute.String("ClusterNamespace", cluster.Namespace),
				attribute.String("ClusterName", cluster.Name),
			)

			if isPaused, requeue, err := paused.EnsurePausedCondition(ctx, r.Client, cluster, hostedControlPlane); err != nil ||
				isPaused ||
				requeue {
				if err == nil && isPaused {
					r.pauseDeployment(ctx, hostedControlPlane)
				}
				return ctrl.Result{}, err
			}

			if !hostedControlPlane.DeletionTimestamp.IsZero() {
				return r.reconcileDelete(ctx, patchHelper, hostedControlPlane)
			}

			return r.reconcileNormal(ctx, patchHelper, hostedControlPlane, cluster)
		},
	)
}

func (r *HostedControlPlaneReconciler) patch(
	ctx context.Context,
	patchHelper *patch.Helper,
	teutonetesCluster *v1alpha1.HostedControlPlane,
	options ...patch.Option,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "Patch",
		func(ctx context.Context, span trace.Span) error {
			applicableConditions := []capiv1.ConditionType{
				// TODO: add conditions
			}

			conditions.SetSummary(teutonetesCluster,
				conditions.WithConditions(applicableConditions...),
			)

			options = append(options,
				patch.WithOwnedConditions{
					Conditions: append(applicableConditions, capiv1.ReadyCondition),
				},
			)
			return errorsUtil.IfErrErrorf("failed to patch HostedControlPlane %q/%q: %w",
				teutonetesCluster.Namespace, teutonetesCluster.Name,
				patchHelper.Patch(ctx, teutonetesCluster, options...),
			)
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileNormal(ctx context.Context, _ *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane, cluster *capiv1.Cluster,
) (ctrl.Result, error) {
	return tracing.WithSpan(ctx, hostedControlPlaneReconcilerTracer, "ReconcileNormal",
		func(ctx context.Context, span trace.Span) (_ ctrl.Result, reterr error) {
			if finalizerAdded, err := finalizers.EnsureFinalizer(ctx, r.Client,
				hostedControlPlane, hostedControlPlaneFinalizer,
			); err != nil || finalizerAdded {
				return ctrl.Result{}, err
			}

			type Phase struct {
				Reconcile    func(context.Context, *v1alpha1.HostedControlPlane) error
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			phases := []Phase{
				{
					Name:         "service",
					Reconcile:    r.reconcileService,
					Condition:    v1alpha1.ServiceReadyCondition,
					FailedReason: v1alpha1.ServiceFailedReason,
				},
				{
					Name:         "CA certificates",
					Reconcile:    r.reconcileCACertificates,
					Condition:    v1alpha1.CACertificatesReadyCondition,
					FailedReason: v1alpha1.CACertificatesFailedReason,
				},
				{
					Name: "certificates",
					Reconcile: func(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) error {
						return r.reconcileCertificates(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.CertificatesReadyCondition,
					FailedReason: v1alpha1.CertificatesFailedReason,
				},
				{
					Name:         "kubeconfig",
					Reconcile:    r.reconcileKubeconfigs,
					Condition:    v1alpha1.KubeconfigReadyCondition,
					FailedReason: v1alpha1.KubeconfigFailedReason,
				},
				{
					Name:         "konnectivity config",
					Reconcile:    r.reconcileKonnectivityConfig,
					Condition:    v1alpha1.KonnectivityConfigReadyCondition,
					FailedReason: v1alpha1.KonnectivityConfigFailedReason,
				},
				{
					Name:         "deployment",
					Reconcile:    r.reconcileDeployment,
					Condition:    v1alpha1.DeploymentReadyCondition,
					FailedReason: v1alpha1.DeploymentFailedReason,
				},
				{
					Name:         "kubeadm config",
					Reconcile:    r.reconcileKubeadmConfig,
					Condition:    v1alpha1.KubeadmConfigReadyCondition,
					FailedReason: v1alpha1.KubeadmConfigFailedReason,
				},
				{
					Name:         "kubelet config",
					Reconcile:    r.reconcileKubeletConfig,
					Condition:    v1alpha1.KubeletConfigReadyCondition,
					FailedReason: v1alpha1.KubeletConfigFailedReason,
				},
				{
					Name:         "bootstrap token",
					Reconcile:    r.reconcileBootstrapToken,
					Condition:    v1alpha1.BootstrapTokenReadyCondition,
					FailedReason: v1alpha1.BootstrapTokenFailedReason,
				},
				{
					Name:         "cluster admin RBAC",
					Reconcile:    r.reconcileClusterAdminRBAC,
					Condition:    v1alpha1.ClusterAdminRBACReadyCondition,
					FailedReason: v1alpha1.ClusterAdminRBACFailedReason,
				},
				{
					Name:         "HTTPRoute",
					Reconcile:    r.reconcileHTTPRoute,
					Condition:    v1alpha1.HTTPRouteReadyCondition,
					FailedReason: v1alpha1.HTTPRouteFailedReason,
				},
			}

			for _, phase := range phases {
				if err := phase.Reconcile(ctx, hostedControlPlane); err != nil {
					conditions.MarkFalse(
						hostedControlPlane,
						phase.Condition,
						phase.FailedReason,
						capiv1.ConditionSeverityError,
						fmt.Sprintf("Reconciling %s failed", phase.Name), err,
					)
					return reconcile.Result{}, err
				} else {
					conditions.MarkTrue(hostedControlPlane, phase.Condition)
				}
			}

			// workloadCluster, err := r.managementCluster.GetWorkloadCluster(ctx, client.ObjectKeyFromObject(cluster))
			// if err != nil {
			//	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			//}
			//
			// if err := workloadCluster.AllowBootstrapTokensToGetNodes(ctx); err != nil {
			//	return ctrl.Result{}, fmt.Errorf("failed to set role and role binding for kubeadm: %w", err)
			//}
			//
			// parsedVersion, err := version.ParseMajorMinorPatchTolerant(controlPlane.KCP.Spec.Version)
			// if err != nil {
			//	return ctrl.Result{}, errors.Wrapf(err, "failed to parse kubernetes version %q", controlPlane.KCP.Spec.Version)
			//}
			//
			// if hostedControlPlane.Spec.KubeProxy.Enabled {
			//	if err := workloadCluster.ReconcileKubeProxy(ctx, hostedControlPlane, parsedVersion); err != nil {
			//		return ctrl.Result{}, fmt.Errorf("failed to reconcile kube-proxy: %w", err)
			//	}
			//}
			//
			// if err := r.reconcileControlPlaneStatusDeployment(ctx, hostedControlPlane); err != nil {
			//	return ctrl.Result{}, err
			//}

			return ctrl.Result{}, nil
		},
	)
}

// reconcileDelete handles the deletion of the HostedControlPlane resource.
func (r *HostedControlPlaneReconciler) reconcileDelete(
	ctx context.Context,
	_ *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (ctrl.Result, error) {
	return tracing.WithSpan(ctx, hostedControlPlaneReconcilerTracer, "ReconcileDelete",
		func(ctx context.Context, span trace.Span) (ctrl.Result, error) {
			controllerutil.RemoveFinalizer(hostedControlPlane, hostedControlPlaneFinalizer)

			return ctrl.Result{}, nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCAPICluster",
		func(ctx context.Context, span trace.Span) error {
			// deployment := acv1.Deployment(hostedControlPlane.Name, hostedControlPlane.Namespace).
			//	WithSpec(acv1.DeploymentSpec())

			return nil
		},
	)
}

//+kubebuilder:rbac:groups=core,resources=services,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileService",
		func(ctx context.Context, span trace.Span) error {
			service := corev1ac.Service(names.GetServiceName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithSpec(corev1ac.ServiceSpec().
					WithType(corev1.ServiceTypeClusterIP).
					WithSelector(names.GetSelector(hostedControlPlane.Name)).
					WithPorts(corev1ac.ServicePort().
						WithName("api").
						WithPort(443).
						WithTargetPort(intstr.FromString("api")).
						WithProtocol(corev1.ProtocolTCP),
					),
				).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane))
			_, err := r.KubernetesClient.CoreV1().Services(hostedControlPlane.Namespace).Apply(ctx,
				service,
				applyOptions,
			)

			return errorsUtil.IfErrErrorf("failed to patch service: %w", err)
		},
	)
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=issuers,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileCACertificates(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileCACertificates",
		func(ctx context.Context, span trace.Span) error {
			createCertificateSpec := func(
				issuerName string,
				secretName string,
				name string,
			) *certmanagerv1ac.CertificateSpecApplyConfiguration {
				return certmanagerv1ac.CertificateSpec().
					WithSecretName(secretName).
					WithIssuerRef(certmanagermetav1ac.IssuerReference().
						WithKind(certmanagerv1.IssuerKind).
						WithName(issuerName),
					).
					WithCommonName(name).
					WithDNSNames(name).
					WithUsages(
						certmanagerv1.UsageDigitalSignature,
						certmanagerv1.UsageKeyEncipherment,
						certmanagerv1.UsageCertSign,
					).
					WithIsCA(true).
					WithDuration(metav1.Duration{Duration: caCertificateDuration}).
					WithRenewBeforePercentage(certificateRenewBefore)
			}

			rootIssuer := certmanagerv1ac.Issuer(names.GetRootIssuerName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(certmanagerv1ac.IssuerSpec().
					WithSelfSigned(certmanagerv1ac.SelfSignedIssuer()),
				)

			issuerClient := r.CertManagerClient.CertmanagerV1().Issuers(hostedControlPlane.Namespace)
			_, err := issuerClient.Apply(ctx, rootIssuer, applyOptions)
			if err != nil {
				return fmt.Errorf("failed to patch self-signed issuer: %w", err)
			}

			if err := r.reconcileCertificate(ctx, hostedControlPlane,
				names.GetCACertificateName(hostedControlPlane.Name),
				createCertificateSpec(
					names.GetRootIssuerName(hostedControlPlane.Name),
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

			if err := r.reconcileCertificate(ctx, hostedControlPlane,
				names.GetFrontProxyCAName(hostedControlPlane.Name),
				createCertificateSpec(
					names.GetCAIssuerName(hostedControlPlane.Name),
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

func (r *HostedControlPlaneReconciler) reconcileCertificates(
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
					WithDuration(metav1.Duration{Duration: certificateDuration}).
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
						"kube-apiserver",
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
					name: names.GetFrontProxyCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetFrontProxyCAName(hostedControlPlane.Name),
						"front-proxy-client",
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
						WithOrganizations("system:masters"),
					),
				},
				{
					name: names.GetControllerManagerCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						"system:kube-controller-manager",
						names.GetControllerManagerSecretName(hostedControlPlane.Name),
						certmanagerv1.UsageClientAuth,
					),
				},
				{
					name: names.GetSchedulerCertificateName(hostedControlPlane.Name),
					spec: createCertificateSpec(
						names.GetCAIssuerName(hostedControlPlane.Name),
						"system:kube-scheduler",
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
						WithOrganizations("systemd:masters"),
					),
				},
			}

			for _, cert := range certificates {
				if err := r.reconcileCertificate(ctx, hostedControlPlane, cert.name, cert.spec); err != nil {
					return fmt.Errorf("failed to reconcile certificate %s: %w", cert.name, err)
				}
			}

			return nil
		},
	)
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileCertificate(
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
				attribute.String("SecretName", *spec.SecretName),
			)

			certificate := certmanagerv1ac.Certificate(name, hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithSpec(spec.WithRevisionHistoryLimit(1).
					WithSecretTemplate(certmanagerv1ac.CertificateSecretTemplate().
						WithLabels(names.GetLabels(hostedControlPlane.Name)),
					),
				)

			_, err := r.CertManagerClient.CertmanagerV1().Certificates(hostedControlPlane.Namespace).
				Apply(ctx, certificate, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch certificate: %w", err)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileKubeconfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			kubeconfigs := []struct {
				name        string
				secretName  string
				certName    string
				commonName  string
				clusterName string
			}{
				{
					name:        "admin",
					secretName:  names.GetAdminSecretName(hostedControlPlane.Name),
					certName:    names.GetAdminCertificateName(hostedControlPlane.Name),
					commonName:  "kubernetes-admin",
					clusterName: hostedControlPlane.Name,
				},
				{
					name:        "controller-manager",
					secretName:  names.GetControllerManagerSecretName(hostedControlPlane.Name),
					certName:    names.GetControllerManagerCertificateName(hostedControlPlane.Name),
					commonName:  "system:kube-controller-manager",
					clusterName: hostedControlPlane.Name,
				},
				{
					name:        "scheduler",
					secretName:  names.GetSchedulerSecretName(hostedControlPlane.Name),
					certName:    names.GetSchedulerCertificateName(hostedControlPlane.Name),
					commonName:  "system:kube-scheduler",
					clusterName: hostedControlPlane.Name,
				},
				{
					name:        "konnectivity-client",
					secretName:  names.GetKonnectivityClientSecretName(hostedControlPlane.Name),
					certName:    names.GetKonnectivityClientCertificateName(hostedControlPlane.Name),
					commonName:  "system:konnectivity-client",
					clusterName: hostedControlPlane.Name,
				},
			}

			for _, kubeconfig := range kubeconfigs {
				if err := r.reconcileKubeconfig(ctx, hostedControlPlane, kubeconfig); err != nil {
					return fmt.Errorf("failed to reconcile kubeconfig %s: %w", kubeconfig.name, err)
				}
			}

			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileKubeconfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	kubeconfig struct {
		name        string
		secretName  string
		certName    string
		commonName  string
		clusterName string
	},
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("KubeconfigName", kubeconfig.name),
				attribute.String("SecretName", kubeconfig.secretName),
				attribute.String("CertificateName", kubeconfig.certName),
			)

			certSecret, err := r.KubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Get(ctx, kubeconfig.secretName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("certificate secret %s not found: %w", kubeconfig.secretName, err)
				}
				return fmt.Errorf("failed to get certificate secret %s: %w", kubeconfig.secretName, err)
			}

			caSecret, err := r.KubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Get(ctx, names.GetCASecretName(hostedControlPlane.Name), metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("CA secret %s not found: %w", names.GetCASecretName(hostedControlPlane.Name), err)
				}
				return fmt.Errorf("failed to get CA secret %s: %w", names.GetCASecretName(hostedControlPlane.Name), err)
			}

			apiServerURL := fmt.Sprintf("https://%s.%s.svc:6443",
				names.GetServiceName(hostedControlPlane.Name),
				hostedControlPlane.Namespace,
			)

			kubeconfigData := r.generateKubeconfig(
				apiServerURL,
				kubeconfig.clusterName,
				kubeconfig.commonName,
				certSecret.Data["tls.crt"],
				certSecret.Data["tls.key"],
				caSecret.Data["tls.crt"],
			)

			kubeconfigSecret := corev1ac.Secret(fmt.Sprintf("%s-kubeconfig", kubeconfig.name), hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string][]byte{
					"kubeconfig": kubeconfigData,
				})

			_, err = r.KubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Apply(ctx, kubeconfigSecret, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch kubeconfig secret: %w", err)
		},
	)
}

func (r *HostedControlPlaneReconciler) generateKubeconfig(
	apiServerURL string,
	clusterName string,
	userName string,
	clientCert []byte,
	clientKey []byte,
	caCert []byte,
) []byte {
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
current-context: %s
users:
- name: %s
  user:
    client-certificate-data: %s
    client-key-data: %s
`,
		clusterName,
		apiServerURL,
		base64.StdEncoding.EncodeToString(caCert),
		clusterName,
		clusterName,
		userName,
		clusterName,
		userName,
		base64.StdEncoding.EncodeToString(clientCert),
		base64.StdEncoding.EncodeToString(clientKey),
	)

	return []byte(kubeconfig)
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileKonnectivityConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKonnectivityConfig",
		func(ctx context.Context, span trace.Span) error {
			egressSelectorConfig := &apiserver.EgressSelectorConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.k8s.io/v1beta1",
					Kind:       "EgressSelectorConfiguration",
				},
				EgressSelections: []apiserver.EgressSelection{
					{
						Name: "cluster",
						Connection: apiserver.Connection{
							ProxyProtocol: apiserver.ProtocolGRPC,
							Transport: &apiserver.Transport{
								UDS: &apiserver.UDSTransport{
									UDSName: "/run/konnectivity/konnectivity-server.sock",
								},
							},
						},
					},
				},
			}

			scheme := runtime.NewScheme()
			encoder := json.NewSerializerWithOptions(json.SimpleMetaFactory{}, scheme, scheme, json.SerializerOptions{
				Yaml:   true,
				Pretty: true,
				Strict: false,
			})

			buf := bytes.NewBuffer([]byte{})
			if err := encoder.Encode(egressSelectorConfig, buf); err != nil {
				return fmt.Errorf("failed to encode egress selector config: %w", err)
			}

			configMap := corev1ac.ConfigMap(names.GetKonnectivityConfigMapName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithBinaryData(map[string][]byte{
					"egress-selector-configuration.yaml": buf.Bytes(),
				})

			_, err := r.KubernetesClient.CoreV1().ConfigMaps(hostedControlPlane.Namespace).
				Apply(ctx, configMap, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch konnectivity configmap: %w", err)
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileKubeadmConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeadmConfigs",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement kubeadm config reconciliation
			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileKubeletConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeletConfig",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement kubelet config reconciliation
			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileBootstrapToken(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileBootstrapToken",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement bootstrap token reconciliation
			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileClusterAdminRBAC(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileClusterAdminRBAC",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement cluster admin RBAC reconciliation
			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileHTTPRoute(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileHTTPRoute",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement HTTPRoute reconciliation
			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) updateStatus(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "UpdateStatus",
		func(ctx context.Context, span trace.Span) error {
			// TODO: Implement status update logic
			return nil
		},
	)
}

func (r *HostedControlPlaneReconciler) updateV1Beta2Status(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) {
	// TODO: Implement v1beta2 status update logic
}

func (r *HostedControlPlaneReconciler) pauseDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) {
	// TODO: Implement deployment pause logic
}

//+kubebuilder:rbac:groups=core,resources=deployments,verbs=get

func (r *HostedControlPlaneReconciler) reconcileControlPlaneStatusDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileClusterStatusKubeadmControlPlane",
		func(ctx context.Context, span trace.Span) error {
			deployment := &v1.Deployment{ObjectMeta: metav1.ObjectMeta{
				Namespace: hostedControlPlane.Namespace,
				Name:      hostedControlPlane.Name,
			}}
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to get Deployment for HostedControlPlane %q: %w",
						client.ObjectKeyFromObject(hostedControlPlane),
						err,
					)
				} else {
					return nil
				}
			}

			// TODO: get conditions of deployment into sensible conditions on us

			return nil
		},
	)
}
