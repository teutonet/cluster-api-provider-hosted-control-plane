// Package hostedcontrolplane contains the controller logic for reconciling HostedControlPlane objects.
package hostedcontrolplane

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/go-logr/logr"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	apiserverv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
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

var certificateRenewBefore = int32(90)

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

var (
	ErrCloudNotReady            = errors.New("TeutonetesCloud is not ready")
	ErrDomainNotFound           = errors.New("OpenStack domain not found")
	ErrFlavorNotFound           = errors.New("OpenStack flavor not found")
	ErrAvailabilityZoneNotFound = errors.New("OpenStack availability zone not found")
	ErrCANotReady               = errors.New("CA not ready")
	ErrCertificateNotReady      = errors.New("certificate not ready")
)

type HostedControlPlaneReconciler struct {
	Client            client.Client
	KubernetesClient  kubernetes.Interface
	CertManagerClient cmclient.Interface
	Recorder          record.EventRecorder
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=watch;list
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=watch;list
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=watch;list
//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=watch;list
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
			Owns(&certmanagerv1.Certificate{}).
			Owns(&corev1.Secret{}).
			Owns(&corev1.ConfigMap{}).
			Owns(&appsv1.StatefulSet{}).
			Owns(&v1.Deployment{}).
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
				return reconcile.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
			}

			patchHelper, err := patch.NewHelper(hostedControlPlane, r.Client)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create patch helper for HostedControlPlane: %w", err)
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
			return errorsUtil.IfErrErrorf("failed to patch HostedControlPlane: %w",
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
				return ctrl.Result{}, errorsUtil.IfErrErrorf("failed to ensure finalizer: %w", err)
			}

			if hostedControlPlane.Spec.ControlPlaneEndpoint == nil {
				if cluster.Spec.ControlPlaneEndpoint.IsZero() {
					hostedControlPlane.Spec.ControlPlaneEndpoint = &capiv1.APIEndpoint{
						Host: cluster.Name + "." + cluster.Namespace + ".svc",
						Port: 443,
					}
				} else {
					hostedControlPlane.Spec.ControlPlaneEndpoint = &cluster.Spec.ControlPlaneEndpoint
				}
			}

			type Phase struct {
				Reconcile    func(context.Context, *v1alpha1.HostedControlPlane) error
				Condition    capiv1.ConditionType
				FailedReason string
				Name         string
			}

			certificateReconciler := &CertificateReconciler{
				certManagerClient:     r.CertManagerClient,
				caCertificateDuration: 1 * time.Hour,
				certificateDuration:   1 * time.Hour,
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
					Reconcile:    certificateReconciler.ReconcileCACertificates,
					Condition:    v1alpha1.CACertificatesReadyCondition,
					FailedReason: v1alpha1.CACertificatesFailedReason,
				},
				{
					Name: "certificates",
					Reconcile: func(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) error {
						return certificateReconciler.ReconcileCertificates(ctx, hostedControlPlane, cluster)
					},
					Condition:    v1alpha1.CertificatesReadyCondition,
					FailedReason: v1alpha1.CertificatesFailedReason,
				},
				{
					Name: "kubeconfig",
					Reconcile: func(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) error {
						kubeconfigReconciler := &KubeconfigReconciler{
							kubernetesClient: r.KubernetesClient,
						}
						return kubeconfigReconciler.ReconcileKubeconfigs(
							ctx,
							hostedControlPlane,
							cluster.Spec.ControlPlaneEndpoint,
						)
					},
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
					Name: "etcd cluster",
					Reconcile: func(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) error {
						etcdClusterReconciler := &EtcdClusterReconciler{
							client:           r.Client,
							kubernetesClient: r.KubernetesClient,
						}
						return etcdClusterReconciler.ReconcileEtcdCluster(ctx, hostedControlPlane)
					},
					Condition:    v1alpha1.EtcdClusterReadyCondition,
					FailedReason: v1alpha1.EtcdClusterFailedReason,
				},
				{
					Name: "deployment",
					Reconcile: func(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) error {
						deploymentReconciler := &DeploymentReconciler{
							kubernetesClient: r.KubernetesClient,
						}
						return deploymentReconciler.ReconcileDeployment(ctx, hostedControlPlane)
					},
					Condition:    v1alpha1.DeploymentReadyCondition,
					FailedReason: v1alpha1.DeploymentFailedReason,
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
						"Reconciling %s failed: %v", phase.Name, err,
					)
					if errors.Is(err, ErrCANotReady) ||
						errors.Is(err, ErrCertificateNotReady) ||
						errors.Is(err, ErrStatefulSetRecreateRequired) {
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

//+kubebuilder:rbac:groups=core,resources=services,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileService",
		func(ctx context.Context, span trace.Span) error {
			service := corev1ac.Service(names.GetServiceName(hostedControlPlane.Name), hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name)).
				WithSpec(corev1ac.ServiceSpec().
					WithType(corev1.ServiceTypeClusterIP).
					WithSelector(names.GetControlPlaneLabels(hostedControlPlane.Name)).
					WithPorts(corev1ac.ServicePort().
						WithName(APIServerPortName).
						WithPort(443).
						WithTargetPort(intstr.FromString(APIServerPortName)).
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

//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileKonnectivityConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKonnectivityConfig",
		func(ctx context.Context, span trace.Span) error {
			egressSelectorConfig := &apiserverv1beta1.EgressSelectorConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.k8s.io/v1beta1",
					Kind:       "EgressSelectorConfiguration",
				},
				EgressSelections: []apiserverv1beta1.EgressSelection{
					{
						Name: "cluster",
						Connection: apiserverv1beta1.Connection{
							ProxyProtocol: apiserverv1beta1.ProtocolGRPC,
							Transport: &apiserverv1beta1.Transport{
								UDS: &apiserverv1beta1.UDSTransport{
									UDSName: "/run/konnectivity/konnectivity-server.sock",
								},
							},
						},
					},
				},
			}

			buf, err := ToYaml(egressSelectorConfig)
			if err != nil {
				return err
			}

			configMap := corev1ac.ConfigMap(
				names.GetKonnectivityConfigMapName(hostedControlPlane.Name),
				hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string]string{
					EgressSelectorConfigurationFileName: buf.String(),
				})

			_, err = r.KubernetesClient.CoreV1().ConfigMaps(hostedControlPlane.Namespace).
				Apply(ctx, configMap, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch konnectivity configmap: %w", err)
		},
	)
}

func ToYaml(obj runtime.Object) (*bytes.Buffer, error) {
	scheme := runtime.NewScheme()
	encoder := json.NewSerializerWithOptions(json.SimpleMetaFactory{}, scheme, scheme, json.SerializerOptions{
		Yaml:   true,
		Pretty: true,
		Strict: false,
	})

	buf := bytes.NewBuffer([]byte{})
	if err := encoder.Encode(obj, buf); err != nil {
		return nil, fmt.Errorf("failed to encode egress selector config: %w", err)
	}
	return buf, nil
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
	_ *v1alpha1.HostedControlPlane,
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
