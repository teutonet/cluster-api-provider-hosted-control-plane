package hostedcontrolplane

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	containerutil "sigs.k8s.io/cluster-api/util/container"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WorkloadCluster interface {
	ReconcileKubeProxy(context.Context, *v1alpha1.HostedControlPlane) error
}

type Workload struct {
	Client          client.Client
	CoreDNSMigrator coreDNSMigrator
	restConfig      *rest.Config
}

var _ WorkloadCluster = &Workload{}

const (
	kubeProxyKey = "kube-proxy"
)

func (w *Workload) ReconcileKubeProxy(ctx context.Context, hostedControlPlane *v1alpha1.HostedControlPlane) error {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeProxyKey,
			Namespace: metav1.NamespaceSystem,
		},
	}

	if err := w.Client.Get(ctx, client.ObjectKeyFromObject(ds), ds); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to determine if %s DaemonSet already exists: %w", kubeProxyKey, err)
	}
	container := findKubeProxyContainer(ds)
	if container == nil {
		return nil
	}

	newImageName, err := containerutil.ModifyImageTag(container.Image, hostedControlPlane.Spec.Version)
	if err != nil {
		return err
	}

	if container.Image != newImageName {
		helper, err := patch.NewHelper(ds, w.Client)
		if err != nil {
			return err
		}
		patchKubeProxyImage(ds, newImageName)
		return helper.Patch(ctx, ds)
	}
	return nil
}

func findKubeProxyContainer(ds *appsv1.DaemonSet) *corev1.Container {
	containers := ds.Spec.Template.Spec.Containers
	for idx := range containers {
		if containers[idx].Name == kubeProxyKey {
			return &containers[idx]
		}
	}
	return nil
}

func patchKubeProxyImage(ds *appsv1.DaemonSet, image string) {
	containers := ds.Spec.Template.Spec.Containers
	for idx := range containers {
		if containers[idx].Name == kubeProxyKey {
			containers[idx].Image = image
		}
	}
}
