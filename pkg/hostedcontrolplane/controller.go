// Package hostedcontrolplane contains the controller logic for reconciling HostedControlPlane objects.
package hostedcontrolplane

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/go-logr/logr"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/apiserverresources"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/certificates"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/etcd_cluster"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/infrastructure_cluster"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/kubeconfig"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/tlsroutes"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/workload"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
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
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

type HostedControlPlaneReconciler interface {
	reconcile.Reconciler
	SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles int, predicateLogger logr.Logger) error
}

func NewHostedControlPlaneReconciler(
	client client.Client,
	kubernetesClient kubernetes.Interface,
	certManagerClient cmclient.Interface,
	gatewayClient gwclient.Interface,
	recorder record.EventRecorder,
	controllerNamespace string,
	tracingWrapper func(rt http.RoundTripper) http.RoundTripper,
) HostedControlPlaneReconciler {
	return &hostedControlPlaneReconciler{
		client:                           client,
		kubernetesClient:                 kubernetesClient,
		certManagerClient:                certManagerClient,
		gatewayClient:                    gatewayClient,
		recorder:                         recorder,
		managementCluster:                workload.NewManagementCluster(kubernetesClient, tracingWrapper, "controller"),
		worldComponent:                   "world",
		controllerNamespace:              controllerNamespace,
		controllerComponent:              "hosted-control-plane-controller",
		caCertificatesDuration:           31 * 24 * time.Hour,
		certificatesDuration:             24 * time.Hour,
		apiServerComponentLabel:          "api-server",
		apiServerServicePort:             int32(443),
		etcdComponentLabel:               "etcd",
		etcdServerPort:                   int32(2379),
		etcdServerStorageBuffer:          resource.MustParse("500Mi"),
		etcdServerStorageIncrement:       resource.MustParse("1Gi"),
		konnectivityNamespace:            metav1.NamespaceSystem,
		konnectivityServiceAccount:       "konnectivity-agent",
		konnectivityClientKubeconfigName: "konnectivity-client",
		controllerKubeconfigName:         "controller",
		konnectivityServerAudience:       "system:konnectivity-server",
		apiServerServiceLegacyPortName:   "legacy-api",
		konnectivityServicePort:          int32(8132),
		finalizer:                        fmt.Sprintf("hcp.%s", api.GroupName),
		tracer:                           tracing.GetTracer(""),
	}
}

type hostedControlPlaneReconciler struct {
	client                           client.Client
	kubernetesClient                 kubernetes.Interface
	certManagerClient                cmclient.Interface
	gatewayClient                    gwclient.Interface
	recorder                         record.EventRecorder
	managementCluster                workload.ManagementCluster
	worldComponent                   string
	controllerNamespace              string
	controllerComponent              string
	caCertificatesDuration           time.Duration
	certificatesDuration             time.Duration
	apiServerComponentLabel          string
	apiServerServicePort             int32
	etcdComponentLabel               string
	etcdServerPort                   int32
	etcdServerStorageBuffer          resource.Quantity
	etcdServerStorageIncrement       resource.Quantity
	konnectivityNamespace            string
	konnectivityServiceAccount       string
	konnectivityClientKubeconfigName string
	controllerKubeconfigName         string
	konnectivityServerAudience       string
	apiServerServiceLegacyPortName   string
	konnectivityServicePort          int32
	finalizer                        string
	tracer                           string
}

var _ HostedControlPlaneReconciler = &hostedControlPlaneReconciler{}

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=watch;list
//+kubebuilder:rbac:groups=core,resources=services,verbs=watch;list
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=watch;list
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=watch;list
//+kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=watch;list
//+kubebuilder:rbac:groups=scheduling.k8s.io,resources=priorityclasses,verbs=watch;list
//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=watch;list
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=watch;list
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=watch;list
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes,verbs=watch;list

func (r *hostedControlPlaneReconciler) SetupWithManager(
	mgr ctrl.Manager,
	maxConcurrentReconciles int,
	predicateLogger logr.Logger,
) error {
	return errorsUtil.IfErrErrorf("failed to setup HostedControlPlane controller: %w",
		ctrl.NewControllerManagedBy(mgr).
			WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
			For(&v1alpha1.HostedControlPlane{}).
			Owns(&certmanagerv1.Certificate{}).
			Owns(&corev1.Secret{}).
			Owns(&corev1.Service{}).
			Owns(&corev1.ConfigMap{}).
			Owns(&appsv1.StatefulSet{}).
			Owns(&appsv1.Deployment{}).
			Owns(&policyv1.PodDisruptionBudget{}).
			Owns(&gwv1alpha2.TLSRoute{}).
			Watches(&corev1.Secret{},
				handler.EnqueueRequestsFromMapFunc(r.secretToHostedControlPlane),
				builder.WithPredicates(
					predicates.ResourceIsChanged(mgr.GetScheme(), predicateLogger),
				),
			).
			Watches(&corev1.ConfigMap{},
				handler.EnqueueRequestsFromMapFunc(r.configMapToHostedControlPlane),
				builder.WithPredicates(
					predicates.ResourceIsChanged(mgr.GetScheme(), predicateLogger),
				),
			).
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

func (r *hostedControlPlaneReconciler) clusterToHostedControlPlane(
	_ context.Context,
	object client.Object,
) []reconcile.Request {
	cluster, ok := object.(*capiv1.Cluster)
	if !ok {
		panic(fmt.Sprintf("Expected a Cluster but got a %T", cluster))
	}

	controlPlaneRef := cluster.Spec.ControlPlaneRef
	if controlPlaneRef != nil && controlPlaneRef.Kind == "HostedControlPlane" {
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

func (r *hostedControlPlaneReconciler) secretToHostedControlPlane(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	secret, ok := object.(*corev1.Secret)
	if !ok {
		panic(fmt.Sprintf("Expected a Secret but got a %T", secret))
	}

	certificateOwnerRefs := slices.Filter(secret.OwnerReferences, func(ownerRef metav1.OwnerReference, _ int) bool {
		return ownerRef.Kind == "Certificate" && ownerRef.APIVersion == certmanagerv1.SchemeGroupVersion.String()
	})

	if len(certificateOwnerRefs) > 0 {
		return slices.Flatten(slices.FilterMap(slices.Map(certificateOwnerRefs,
			func(ownerRef metav1.OwnerReference, _ int) client.ObjectKey {
				return client.ObjectKey{
					Namespace: secret.Namespace,
					Name:      ownerRef.Name,
				}
			},
		), func(key client.ObjectKey, _ int) ([]reconcile.Request, bool) {
			return r.resolveOwnerRefsToHostedControlPlanes(ctx, key)
		}))
	}

	hostedControlPlanes := v1alpha1.HostedControlPlaneList{}
	if err := r.client.List(ctx, &hostedControlPlanes, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}

	for _, hostedControlPlane := range hostedControlPlanes.Items {
		if slices.SomeBy(slices.Entries(hostedControlPlane.Spec.Deployment.APIServer.Mounts),
			func(mount slices.Entry[string, v1alpha1.HostedControlPlaneMount]) bool {
				return mount.Value.Secret != nil && mount.Value.Secret.SecretName == secret.Name
			},
		) {
			return []reconcile.Request{{
				NamespacedName: client.ObjectKey{
					Namespace: hostedControlPlane.Namespace,
					Name:      hostedControlPlane.Name,
				},
			}}
		}
	}

	return nil
}

func (r *hostedControlPlaneReconciler) configMapToHostedControlPlane(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	configMap, ok := object.(*corev1.ConfigMap)
	if !ok {
		panic(fmt.Sprintf("Expected a ConfigMap but got a %T", configMap))
	}

	hostedControlPlanes := v1alpha1.HostedControlPlaneList{}
	if err := r.client.List(ctx, &hostedControlPlanes, client.InNamespace(configMap.Namespace)); err != nil {
		return nil
	}

	for _, hostedControlPlane := range hostedControlPlanes.Items {
		if slices.SomeBy(slices.Entries(hostedControlPlane.Spec.Deployment.APIServer.Mounts),
			func(mount slices.Entry[string, v1alpha1.HostedControlPlaneMount]) bool {
				return mount.Value.ConfigMap != nil && mount.Value.ConfigMap.Name == configMap.Name
			},
		) {
			return []reconcile.Request{{
				NamespacedName: client.ObjectKey{
					Namespace: hostedControlPlane.Namespace,
					Name:      hostedControlPlane.Name,
				},
			}}
		}
	}

	return nil
}

func (r *hostedControlPlaneReconciler) resolveOwnerRefsToHostedControlPlanes(
	ctx context.Context,
	key client.ObjectKey,
) ([]reconcile.Request, bool) {
	certificate := &certmanagerv1.Certificate{}
	if err := r.client.Get(ctx, key, certificate); err != nil {
		return []reconcile.Request{}, false
	}
	return slices.FilterMap(
		certificate.OwnerReferences,
		func(ownerRef metav1.OwnerReference, _ int) (reconcile.Request,
			bool,
		) {
			if ownerRef.Kind == "HostedControlPlane" &&
				ownerRef.APIVersion == v1alpha1.SchemeGroupVersion.String() {
				return reconcile.Request{
					NamespacedName: client.ObjectKey{
						Namespace: key.Namespace,
						Name:      ownerRef.Name,
					},
				}, true
			}
			return reconcile.Request{}, false
		},
	), true
}

//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes,verbs=get;update;patch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get
//+kubebuilder:rbac:groups="",resources=events,verbs=create

func (r *hostedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	return tracing.WithSpan(ctx, r.tracer, "Reconcile",
		func(ctx context.Context, span trace.Span) (ctrl.Result, error) {
			span.SetAttributes(
				attribute.String("reconcile.id", string(controller.ReconcileIDFromContext(ctx))),
				attribute.String("namespace", req.Namespace),
				attribute.String("name", req.Name),
			)

			hostedControlPlane := &v1alpha1.HostedControlPlane{}
			if err := r.client.Get(ctx, req.NamespacedName, hostedControlPlane); err != nil {
				if apierrors.IsNotFound(err) {
					return reconcile.Result{}, nil
				}
				return reconcile.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
			}

			patchHelper, err := patch.NewHelper(hostedControlPlane, r.client)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create patch helper for HostedControlPlane: %w", err)
			}

			defer func() {
				if err := r.patch(ctx, patchHelper, hostedControlPlane); err != nil {
					reterr = kerrors.NewAggregate([]error{reterr, err})
				}
			}()

			cluster, err := util.GetOwnerCluster(ctx, r.client, hostedControlPlane.ObjectMeta)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to retrieve owner Cluster: %w", err)
			}
			if cluster == nil {
				span.AddEvent("Cluster Controller has not yet set OwnerRef")
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			span.SetAttributes(
				attribute.String("cluster.namespace", cluster.Namespace),
				attribute.String("cluster.name", cluster.Name),
			)

			if isPaused, requeue, err := paused.EnsurePausedCondition(ctx, r.client, cluster, hostedControlPlane); err != nil ||
				isPaused ||
				requeue {
				if err == nil || isPaused || requeue {
					return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
				}
				return ctrl.Result{}, errorsUtil.IfErrErrorf("failed to verify paused condition: %w", err)
			}

			if !hostedControlPlane.DeletionTimestamp.IsZero() {
				return r.reconcileDelete(ctx, patchHelper, hostedControlPlane)
			}

			return r.reconcileNormal(ctx, patchHelper, hostedControlPlane, cluster)
		},
	)
}

func (r *hostedControlPlaneReconciler) patch(
	ctx context.Context,
	patchHelper *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	options ...patch.Option,
) error {
	return tracing.WithSpan1(ctx, r.tracer, "Patch",
		func(ctx context.Context, span trace.Span) error {
			applicableConditions := slices.FilterMap(hostedControlPlane.Status.Conditions,
				func(condition capiv1.Condition, _ int) (capiv1.ConditionType, bool) {
					if condition.Type != capiv1.ReadyCondition &&
						!strings.HasPrefix(string(condition.Type), "Workload") {
						return condition.Type, true
					} else {
						return "", false
					}
				},
			)
			conditions.SetSummary(hostedControlPlane, conditions.WithConditions(
				applicableConditions...,
			))

			options = append(options,
				patch.WithOwnedConditions{
					Conditions: append(applicableConditions, capiv1.ReadyCondition),
				},
			)
			return errorsUtil.IfErrErrorf("failed to patch HostedControlPlane: %w",
				patchHelper.Patch(ctx, hostedControlPlane, options...),
			)
		},
	)
}

//nolint:funlen // this function is not really complex
func (r *hostedControlPlaneReconciler) reconcileNormal(
	ctx context.Context,
	_ *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (ctrl.Result, error) {
	return tracing.WithSpan(ctx, r.tracer, "ReconcileNormal",
		func(ctx context.Context, span trace.Span) (_ ctrl.Result, reterr error) {
			span.SetAttributes(
				attribute.String("hostedcontrolplane.version", hostedControlPlane.Spec.Version),
				attribute.Int("hostedcontrolplane.replicas", int(*hostedControlPlane.Spec.Replicas)),
			)
			if finalizerAdded, err := finalizers.EnsureFinalizer(ctx, r.client,
				hostedControlPlane, r.finalizer,
			); err != nil || finalizerAdded {
				return ctrl.Result{}, errorsUtil.IfErrErrorf("failed to ensure finalizer: %w", err)
			}

			type Phase struct {
				Reconcile    func(context.Context, *v1alpha1.HostedControlPlane, *capiv1.Cluster) (string, error)
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			serviceDomain := "cluster.local"
			serviceCIDR := "10.96.0.0/12"
			podCIDR := "10.0.0.0/16"
			if cluster.Spec.ClusterNetwork != nil {
				if cluster.Spec.ClusterNetwork.ServiceDomain != "" {
					serviceDomain = cluster.Spec.ClusterNetwork.ServiceDomain
				}

				if clusterServiceCIDR := cluster.Spec.ClusterNetwork.Services.String(); clusterServiceCIDR != "" {
					serviceCIDR = clusterServiceCIDR
				}

				if clusterPodCIDR := cluster.Spec.ClusterNetwork.Pods.String(); clusterPodCIDR != "" {
					podCIDR = clusterPodCIDR
				}
			}

			var dnsIP net.IP
			var kubernetesServiceIP net.IP
			if serviceCIDRIP, _, err := net.ParseCIDR(strings.Split(serviceCIDR, ",")[0]); err != nil {
				conditions.MarkFalse(
					hostedControlPlane,
					v1alpha1.WorkloadClusterResourcesReadyCondition,
					v1alpha1.WorkloadClusterResourcesFailedReason,
					capiv1.ConditionSeverityError,
					"Failed to parse Service CIDR %q: %v", serviceCIDR, err,
				)
				return ctrl.Result{}, errorsUtil.IfErrErrorf("failed to parse Service CIDR %q: %w", serviceCIDR, err)
			} else {
				dnsIP = make(net.IP, len(serviceCIDRIP))
				copy(dnsIP, serviceCIDRIP)
				kubernetesServiceIP = make(net.IP, len(serviceCIDRIP))
				copy(kubernetesServiceIP, serviceCIDRIP)
				switch {
				case serviceCIDRIP.To4() != nil:
					dnsIP[len(dnsIP)-1] += 10
					kubernetesServiceIP[len(kubernetesServiceIP)-1] += 1
				case serviceCIDRIP.To16() != nil:
					dnsIP[len(dnsIP)-1] += 16
					kubernetesServiceIP[len(kubernetesServiceIP)-1] += 1
				}
			}

			certificateReconciler := certificates.NewCertificateReconciler(
				r.certManagerClient,
				kubernetesServiceIP,
				r.caCertificatesDuration,
				r.certificatesDuration,
				r.konnectivityServerAudience,
			)
			kubeconfigReconciler := kubeconfig.NewKubeconfigReconciler(
				r.kubernetesClient,
				r.apiServerServicePort,
				r.konnectivityClientKubeconfigName,
				r.controllerKubeconfigName,
			)
			etcdClusterReconciler := etcd_cluster.NewEtcdClusterReconciler(
				r.kubernetesClient,
				r.recorder,
				r.etcdServerPort,
				r.etcdServerStorageBuffer,
				r.etcdServerStorageIncrement,
				r.etcdComponentLabel,
				r.apiServerComponentLabel,
				r.controllerNamespace,
				r.controllerComponent,
			)
			apiServerResourcesReconciler := apiserverresources.NewApiServerResourcesReconciler(
				r.kubernetesClient,
				r.worldComponent,
				serviceCIDR,
				r.apiServerComponentLabel,
				r.apiServerServicePort,
				r.apiServerServiceLegacyPortName,
				r.etcdComponentLabel,
				r.etcdServerPort,
				r.konnectivityNamespace,
				r.konnectivityServiceAccount,
				r.konnectivityServicePort,
				r.konnectivityClientKubeconfigName,
				r.konnectivityServerAudience,
			)
			tlsRoutesReconciler := tlsroutes.NewTLSRoutesReconciler(
				r.gatewayClient,
				r.apiServerServicePort,
				r.konnectivityServicePort,
			)
			infrastructureClusterReconciler := infrastructure_cluster.NewInfrastructureClusterReconciler(
				r.client,
				r.apiServerServicePort,
			)
			workloadClusterReconciler := workload.NewWorkloadClusterReconciler(
				r.kubernetesClient,
				r.managementCluster,
				r.caCertificatesDuration,
				r.certificatesDuration,
				serviceDomain,
				serviceCIDR,
				podCIDR,
				dnsIP,
				r.konnectivityNamespace,
				r.konnectivityServiceAccount,
				r.konnectivityServerAudience,
				r.apiServerServicePort,
			)

			phases := []Phase{
				{
					Name:         "CA certificates",
					Reconcile:    certificateReconciler.ReconcileCACertificates,
					Condition:    v1alpha1.CACertificatesReadyCondition,
					FailedReason: v1alpha1.CACertificatesFailedReason,
				},
				{
					Name:         "apiserver service",
					Reconcile:    apiServerResourcesReconciler.ReconcileApiServerService,
					Condition:    v1alpha1.APIServerServiceReadyCondition,
					FailedReason: v1alpha1.APIServerServiceFailedReason,
				},
				{
					Name:         "sync controlplane endpoint",
					Reconcile:    infrastructureClusterReconciler.SyncControlPlaneEndpoint,
					Condition:    v1alpha1.SyncControlPlaneEndpointReadyCondition,
					FailedReason: v1alpha1.SyncControlPlaneEndpointFailedReason,
				},
				{
					Name:         "certificates",
					Reconcile:    certificateReconciler.ReconcileCertificates,
					Condition:    v1alpha1.CertificatesReadyCondition,
					FailedReason: v1alpha1.CertificatesFailedReason,
				},
				{
					Name: "kubeconfig",
					Reconcile: func(
						ctx context.Context,
						hostedControlPlane *v1alpha1.HostedControlPlane,
						cluster *capiv1.Cluster,
					) (string, error) {
						return "", kubeconfigReconciler.ReconcileKubeconfigs(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.KubeconfigReadyCondition,
					FailedReason: v1alpha1.KubeconfigFailedReason,
				},
				{
					Name:         "etcd cluster",
					Reconcile:    etcdClusterReconciler.ReconcileEtcdCluster,
					Condition:    v1alpha1.EtcdClusterReadyCondition,
					FailedReason: v1alpha1.EtcdClusterFailedReason,
				},
				{
					Name:         "apiserver deployments",
					Reconcile:    apiServerResourcesReconciler.ReconcileApiServerDeployments,
					Condition:    v1alpha1.APIServerDeploymentsReadyCondition,
					FailedReason: v1alpha1.APIServerDeploymentsFailedReason,
				},
				{
					Name:         "tlsroutes",
					Reconcile:    tlsRoutesReconciler.ReconcileTLSRoutes,
					Condition:    v1alpha1.APIServerTLSRoutesReadyCondition,
					FailedReason: v1alpha1.APIServerTLSRoutesFailedReason,
				},
				{
					Name:         "workload cluster resources",
					Reconcile:    workloadClusterReconciler.ReconcileWorkloadClusterResources,
					Condition:    v1alpha1.WorkloadClusterResourcesReadyCondition,
					FailedReason: v1alpha1.WorkloadClusterResourcesFailedReason,
				},
			}

			for _, phase := range phases {
				if notReadyReason, err := phase.Reconcile(ctx, hostedControlPlane, cluster); err != nil {
					conditions.MarkFalse(
						hostedControlPlane,
						phase.Condition,
						phase.FailedReason,
						capiv1.ConditionSeverityError,
						"Reconciling phase %s failed: %v", phase.Name, err,
					)
					return reconcile.Result{}, err
				} else if notReadyReason != "" {
					conditions.MarkFalse(
						hostedControlPlane,
						phase.Condition,
						notReadyReason,
						capiv1.ConditionSeverityInfo,
						"phase %s not ready", phase.Name,
					)
					return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
				} else {
					conditions.MarkTrue(hostedControlPlane, phase.Condition)
				}
			}

			return ctrl.Result{
				RequeueAfter: 1 * time.Minute,
			}, nil
		},
	)
}

func (r *hostedControlPlaneReconciler) reconcileDelete(
	ctx context.Context,
	_ *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (ctrl.Result, error) {
	return tracing.WithSpan(ctx, r.tracer, "ReconcileDelete",
		func(ctx context.Context, span trace.Span) (ctrl.Result, error) {
			controllerutil.RemoveFinalizer(hostedControlPlane, r.finalizer)

			// all resources will be cleaned up by the garbage collector because of the owner references

			return ctrl.Result{}, nil
		},
	)
}
