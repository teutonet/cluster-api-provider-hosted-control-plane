// Package hostedcontrolplane contains the controller logic for reconciling HostedControlPlane objects.
package hostedcontrolplane

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/go-logr/logr"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
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
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

var ErrRequeueRequired = errors.New("requeue required")

const (
	hostedControlPlaneReconcilerTracer = "HostedControlPlaneReconciler"
	hostedControlPlaneControllerName   = "hosted-control-plane-controller"
	certificateRenewBefore             = int32(90)
)

var applyOptions = metav1.ApplyOptions{
	FieldManager: hostedControlPlaneControllerName,
	Force:        true,
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

type HostedControlPlaneReconciler struct {
	Client            client.Client
	KubernetesClient  kubernetes.Interface
	CertManagerClient cmclient.Interface
	GatewayClient     gwclient.Interface
	Recorder          record.EventRecorder
	ManagementCluster ManagementCluster
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=watch;list
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=watch;list
//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=watch;list
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=watch;list
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
			Owns(&certmanagerv1.Certificate{}).
			Owns(&corev1.Secret{}).
			Owns(&corev1.ConfigMap{}).
			Owns(&appsv1.StatefulSet{}).
			Owns(&appsv1.Deployment{}).
			Owns(&gwv1alpha2.TLSRoute{}).
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
				return reconcile.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
			}

			patchHelper, err := patch.NewHelper(hostedControlPlane, r.Client)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create patch helper for HostedControlPlane: %w", err)
			}

			defer func() {
				if err := r.patch(ctx, patchHelper, hostedControlPlane); err != nil {
					reterr = kerrors.NewAggregate([]error{reterr, err})
				}
			}()

			cluster, err := util.GetOwnerCluster(ctx, r.Client, hostedControlPlane.ObjectMeta)
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

func (r *HostedControlPlaneReconciler) patch(
	ctx context.Context,
	patchHelper *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	options ...patch.Option,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "Patch",
		func(ctx context.Context, span trace.Span) error {
			applicableConditions := slices.FilterMap(hostedControlPlane.Status.Conditions,
				func(condition capiv1.Condition, _ int) (capiv1.ConditionType, bool) {
					if condition.Type != capiv1.ReadyCondition {
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

func (r *HostedControlPlaneReconciler) reconcileNormal(ctx context.Context, _ *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane, cluster *capiv1.Cluster,
) (ctrl.Result, error) {
	return tracing.WithSpan(ctx, hostedControlPlaneReconcilerTracer, "ReconcileNormal",
		func(ctx context.Context, span trace.Span) (_ ctrl.Result, reterr error) {
			if finalizerAdded, err := finalizers.EnsureFinalizer(ctx, r.Client,
				hostedControlPlane, hostedControlPlaneFinalizer,
			); err != nil || finalizerAdded {
				return ctrl.Result{}, errorsUtil.IfErrErrorf("failed to ensure finalizer: %w", err)
			}

			type Phase struct {
				Reconcile    func(context.Context, *v1alpha1.HostedControlPlane, *capiv1.Cluster) error
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			certificateReconciler := &CertificateReconciler{
				certManagerClient:     r.CertManagerClient,
				caCertificateDuration: 365 * 24 * time.Hour,
				certificateDuration:   (365 * 24 * time.Hour) / 12,
			}
			kubeconfigReconciler := &KubeconfigReconciler{
				kubernetesClient: r.KubernetesClient,
			}
			etcdClusterReconciler := &EtcdClusterReconciler{
				client:           r.Client,
				kubernetesClient: r.KubernetesClient,
			}
			apiServerReconciler := &APIServerResourcesReconciler{
				kubernetesClient: r.KubernetesClient,
			}

			phases := []Phase{
				{
					Name:         "CA certificates",
					Reconcile:    certificateReconciler.ReconcileCACertificates,
					Condition:    v1alpha1.CACertificatesReadyCondition,
					FailedReason: v1alpha1.CACertificatesFailedReason,
				},
				{
					Name:         "certificates",
					Reconcile:    certificateReconciler.ReconcileCertificates,
					Condition:    v1alpha1.CertificatesReadyCondition,
					FailedReason: v1alpha1.CertificatesFailedReason,
				},
				{
					Name:         "kubeconfig",
					Reconcile:    kubeconfigReconciler.ReconcileKubeconfigs,
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
					Name:         "etcd cluster",
					Reconcile:    etcdClusterReconciler.ReconcileEtcdCluster,
					Condition:    v1alpha1.EtcdClusterReadyCondition,
					FailedReason: v1alpha1.EtcdClusterFailedReason,
				},
				{
					Name:         "apiserver resources",
					Reconcile:    apiServerReconciler.ReconcileAPIServerResources,
					Condition:    v1alpha1.APIServerResourcesReadyCondition,
					FailedReason: v1alpha1.DeploymentFailedReason,
				},
				{
					Name:         "workload cluster resources",
					Reconcile:    r.reconcileWorkloadClusterResources,
					Condition:    v1alpha1.WorkloadClusterResourcesReadyCondition,
					FailedReason: v1alpha1.WorkloadClusterResourcesFailedReason,
				},
				{
					Name:         "TLSRoutes",
					Reconcile:    r.reconcileTLSRoutes,
					Condition:    v1alpha1.TLSRoutesReadyCondition,
					FailedReason: v1alpha1.TLSRoutesFailedReason,
				},
			}

			for _, phase := range phases {
				if err := phase.Reconcile(ctx, hostedControlPlane, cluster); err != nil {
					conditions.MarkFalse(
						hostedControlPlane,
						phase.Condition,
						phase.FailedReason,
						capiv1.ConditionSeverityError,
						"Reconciling %s failed: %v", phase.Name, err,
					)
					if errors.Is(err, ErrRequeueRequired) {
						return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
					}
					return reconcile.Result{}, err
				} else {
					conditions.MarkTrue(hostedControlPlane, phase.Condition)
				}
			}

			return ctrl.Result{}, nil
		},
	)
}

func (r *HostedControlPlaneReconciler) reconcileDelete(
	ctx context.Context,
	_ *patch.Helper,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (ctrl.Result, error) {
	return tracing.WithSpan(ctx, hostedControlPlaneReconcilerTracer, "ReconcileDelete",
		func(ctx context.Context, span trace.Span) (ctrl.Result, error) {
			controllerutil.RemoveFinalizer(hostedControlPlane, hostedControlPlaneFinalizer)

			// all resources will be cleaned up by the garbage collector because of the owner references

			return ctrl.Result{}, nil
		},
	)
}
