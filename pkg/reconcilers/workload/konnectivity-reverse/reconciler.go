package konnectivityreverse

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"

	ciliumclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type ReverseKonnectivityReconciler interface {
	ReconcileReverseKonnectivity(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) (string, error)
}

func NewReverseKonnectivityReconciler(
	managementClusterClient *alias.WorkloadClusterClient,
	ciliumClient ciliumclient.Interface,
	konnectivityNamespace string,
	konnectivityServiceAccount string,
	konnectivityServerAudience string,
	konnectivityServerPort int32,
) ReverseKonnectivityReconciler {
	return &reverseKonnectivityReconciler{
		WorkloadResourceReconciler: reconcilers.WorkloadResourceReconciler{
			WorkloadClusterClient: managementClusterClient,
			CiliumClient:          ciliumClient,
			Tracer:                tracing.GetTracer("konnectivity-reverse"),
		},
		konnectivityServerAudience:      konnectivityServerAudience,
		konnectivityServerPort:          konnectivityServerPort,
		konnectivityNamespace:           konnectivityNamespace,
		konnectivityServerServiceAccount: konnectivityServiceAccount,
		konnectivityServerTokenName:     "konnectivity-server-token",
	}
}

type reverseKonnectivityReconciler struct {
	reconcilers.WorkloadResourceReconciler
	konnectivityNamespace              string
	konnectivityServerAudience         string
	konnectivityServerPort             int32
	konnectivityServerServiceAccount   string
	konnectivityServerTokenName        string
}

var _ ReverseKonnectivityReconciler = &reverseKonnectivityReconciler{}

// ReconcileReverseKonnectivity sets up the reverse konnectivity tunnel
// (workload cluster → control plane communication via konnectivity).
//
// Phases:
// 1. RBAC: Create service account and roles for reverse server
// 2. Deployment: Deploy reverse konnectivity server to workload cluster
// 3. Network Policy: Allow CP → WL traffic on reverse tunnel port
func (rkr *reverseKonnectivityReconciler) ReconcileReverseKonnectivity(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "ReconcileReverseKonnectivity",
		func(ctx context.Context, span trace.Span) (string, error) {
			// TODO: Implement reverse konnectivity reconciliation
			// See ARCHITECTURE.md for design details

			phases := []struct {
				Name      string
				Reconcile func(context.Context) (string, error)
			}{
				{
					Name:      "RBAC",
					Reconcile: rkr.reconcileReverseKonnectivityRBAC,
				},
				{
					Name: "Deployment",
					Reconcile: func(ctx context.Context) (string, error) {
						return rkr.reconcileReverseKonnectivityDeployment(ctx, hostedControlPlane, cluster)
					},
				},
				{
					Name: "NetworkPolicy",
					Reconcile: func(ctx context.Context) (string, error) {
						return rkr.reconcileReverseKonnectivityNetworkPolicy(ctx, cluster)
					},
				},
			}

			logger := logr.FromContextAsSlogLogger(ctx)
			for _, phase := range phases {
				notReadyReason, err := tracing.WithSpan(ctx, rkr.Tracer, phase.Name,
					func(ctx context.Context, _ trace.Span) (string, error) {
						return phase.Reconcile(logr.NewContextWithSlogLogger(ctx, logger.With("phase", phase.Name)))
					},
				)
				if err != nil {
					return "", fmt.Errorf("failed to reconcile reverse konnectivity phase %s: %w", phase.Name, err)
				} else if notReadyReason != "" {
					return notReadyReason, nil
				}
			}

			return "", nil
		},
	)
}

func (rkr *reverseKonnectivityReconciler) reconcileReverseKonnectivityRBAC(ctx context.Context) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "reconcileReverseKonnectivityRBAC",
		func(ctx context.Context, span trace.Span) (string, error) {
			// TODO: Create service account, roles, and role bindings for reverse server
			// Similar to pkg/reconcilers/workload/konnectivity/reconciler.go:reconcileKonnectivityRBAC
			// but for the server side instead of agent side

			return "", nil
		},
	)
}

func (rkr *reverseKonnectivityReconciler) reconcileReverseKonnectivityDeployment(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "reconcileReverseKonnectivityDeployment",
		func(ctx context.Context, span trace.Span) (string, error) {
			// TODO: Deploy reverse konnectivity server to workload cluster
			// This should:
			// 1. Create a pod or sidecar container with konnectivity server
			// 2. Configure it to accept connections on the reverse port
			// 3. Mount projected token for authentication
			// 4. Set up health probes

			// Placeholder: Get nodes to determine replica count
			nodes, err := rkr.WorkloadClusterClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to list nodes: %w", err)
			}

			_ = nodes // TODO: Use node count for replica determination

			return "", nil
		},
	)
}

func (rkr *reverseKonnectivityReconciler) reconcileReverseKonnectivityNetworkPolicy(
	ctx context.Context,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, rkr.Tracer, "reconcileReverseKonnectivityNetworkPolicy",
		func(ctx context.Context, span trace.Span) (string, error) {
			// TODO: Create or update network policies to allow CP → WL traffic
			// on the reverse konnectivity server port

			return "", nil
		},
	)
}
