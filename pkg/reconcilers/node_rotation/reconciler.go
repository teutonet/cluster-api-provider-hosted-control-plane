package node_rotation

import (
	"context"
	"fmt"

	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;patch;update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinepools,verbs=get;list;patch;update

func (r *nodeRotationReconciler) ReconcileCARotation(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, r.tracer, "ReconcileCARotation",
		func(ctx context.Context, _ trace.Span) (string, error) {
			caCertificate, err := r.certManagerClient.CertmanagerV1().
				Certificates(hostedControlPlane.Namespace).
				Get(ctx, names.GetCACertificateName(cluster), metav1.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to get CA certificate: %w", err)
			}

			if caCertificate.Status.NotBefore == nil {
				return "CA certificate not yet issued", nil
			}
			caNotBefore := caCertificate.Status.NotBefore.String()

			machineDeployments := &capiv2.MachineDeploymentList{}
			if err := r.client.List(ctx, machineDeployments,
				client.InNamespace(cluster.Namespace),
				client.MatchingLabels{capiv2.ClusterNameLabel: cluster.Name},
			); err != nil {
				return "", fmt.Errorf("failed to list MachineDeployments: %w", err)
			}

			machinePools := &capiv2.MachinePoolList{}
			if err := r.client.List(ctx, machinePools,
				client.InNamespace(cluster.Namespace),
				client.MatchingLabels{capiv2.ClusterNameLabel: cluster.Name},
			); err != nil {
				return "", fmt.Errorf("failed to list MachinePools: %w", err)
			}

			// objectWithTemplateAnnotations pairs a client.Object with a pointer to its
			// spec.template.metadata.annotations so we can mutate them uniformly.
			type objectWithTemplateAnnotations struct {
				obj         client.Object
				annotations *map[string]string
			}

			objectsToAnnotate := make([]objectWithTemplateAnnotations, 0,
				len(machineDeployments.Items)+len(machinePools.Items))
			for machineDeploymentIndex := range machineDeployments.Items {
				objectsToAnnotate = append(objectsToAnnotate, objectWithTemplateAnnotations{
					obj:         &machineDeployments.Items[machineDeploymentIndex],
					annotations: &machineDeployments.Items[machineDeploymentIndex].Spec.Template.Annotations,
				})
			}
			for machinePoolIndex := range machinePools.Items {
				objectsToAnnotate = append(objectsToAnnotate, objectWithTemplateAnnotations{
					obj:         &machinePools.Items[machinePoolIndex],
					annotations: &machinePools.Items[machinePoolIndex].Spec.Template.Annotations,
				})
			}

			for _, objectToAnnotate := range objectsToAnnotate {
				if (*objectToAnnotate.annotations)[names.CANotBeforeAnnotation] == caNotBefore {
					continue
				}
				base := objectToAnnotate.obj.DeepCopyObject().(client.Object)
				if *objectToAnnotate.annotations == nil {
					*objectToAnnotate.annotations = make(map[string]string)
				}
				(*objectToAnnotate.annotations)[names.CANotBeforeAnnotation] = caNotBefore
				if err := r.client.Patch(ctx, objectToAnnotate.obj, client.MergeFrom(base)); err != nil {
					return "", fmt.Errorf("failed to patch %s/%s: %w",
						objectToAnnotate.obj.GetNamespace(), objectToAnnotate.obj.GetName(), err)
				}
			}

			return "", nil
		},
	)
}
