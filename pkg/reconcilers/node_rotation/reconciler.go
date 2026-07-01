package node_rotation

import (
	"context"
	"fmt"
	"time"

	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const caNotBeforeAnnotation = "controlplane.cluster.x-k8s.io/ca-not-before"

type NodeRotationReconciler interface {
	ReconcileCARotation(ctx context.Context, hcp *v1alpha1.HostedControlPlane, cluster *capiv2.Cluster) (string, error)
}

type nodeRotationReconciler struct {
	client            client.Client
	certManagerClient cmclient.Interface
	tracer            string
}

func NewNodeRotationReconciler(c client.Client, certManagerClient cmclient.Interface) NodeRotationReconciler {
	return &nodeRotationReconciler{
		client:            c,
		certManagerClient: certManagerClient,
		tracer:            tracing.GetTracer("node-rotation"),
	}
}

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch;patch;update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinepools,verbs=get;list;watch;patch;update

func (r *nodeRotationReconciler) ReconcileCARotation(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, r.tracer, "ReconcileCARotation",
		func(ctx context.Context, _ trace.Span) (string, error) {
			caCert, err := r.certManagerClient.CertmanagerV1().Certificates(hostedControlPlane.Namespace).
				Get(ctx, names.GetCACertificateName(cluster), metav1.GetOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return "", fmt.Errorf("failed to get CA certificate: %w", err)
			}
			if apierrors.IsNotFound(err) || caCert.Status.NotBefore == nil {
				return "CA certificate not yet issued", nil
			}

			rolloutAfter := *caCert.Status.NotBefore
			notBeforeStr := rolloutAfter.UTC().Format(time.RFC3339)

			machineDeployments := &capiv2.MachineDeploymentList{}
			if err := r.client.List(ctx, machineDeployments,
				client.InNamespace(cluster.Namespace),
				client.MatchingLabels{capiv2.ClusterNameLabel: cluster.Name},
			); err != nil {
				return "", fmt.Errorf("failed to list MachineDeployments: %w", err)
			}

			for machineDeploymentIndex := range machineDeployments.Items {
				machineDeployment := &machineDeployments.Items[machineDeploymentIndex]
				if machineDeployment.Spec.Rollout.After.Equal(&rolloutAfter) {
					continue
				}
				base := machineDeployment.DeepCopy()
				// CAPI drops all template metadata from the rollout hash, so template annotation
				// changes never create a new MachineSet. spec.rollout.after is the correct trigger;
				// it is idempotent since the CA's notBefore is stable for a given certificate generation.
				machineDeployment.Spec.Rollout.After = rolloutAfter
				if err := r.client.Patch(ctx, machineDeployment, client.MergeFrom(base)); err != nil {
					return "", fmt.Errorf("failed to patch MachineDeployment %s/%s: %w",
						machineDeployment.Namespace, machineDeployment.Name, err)
				}
			}

			machinePools := &capiv2.MachinePoolList{}
			if err := r.client.List(ctx, machinePools,
				client.InNamespace(cluster.Namespace),
				client.MatchingLabels{capiv2.ClusterNameLabel: cluster.Name},
			); err != nil {
				return "", fmt.Errorf("failed to list MachinePools: %w", err)
			}

			for machinePoolIndex := range machinePools.Items {
				machinePool := &machinePools.Items[machinePoolIndex]
				if machinePool.Spec.Template.Annotations[caNotBeforeAnnotation] == notBeforeStr {
					continue
				}
				base := machinePool.DeepCopy()
				// MachinePool has no rolloutAfter equivalent. Set the annotation on the template
				// so infrastructure providers that react to template metadata changes can trigger
				// their own rollout.
				if machinePool.Spec.Template.Annotations == nil {
					machinePool.Spec.Template.Annotations = make(map[string]string)
				}
				machinePool.Spec.Template.Annotations[caNotBeforeAnnotation] = notBeforeStr
				if err := r.client.Patch(ctx, machinePool, client.MergeFrom(base)); err != nil {
					return "", fmt.Errorf("failed to patch MachinePool %s/%s: %w",
						machinePool.Namespace, machinePool.Name, err)
				}
			}

			return "", nil
		},
	)
}
