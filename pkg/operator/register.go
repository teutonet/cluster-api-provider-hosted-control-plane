// Package operator contains the whole schema for the operator.
package operator

import (
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	addToSchemeFuncs := []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		appsv1.AddToScheme,
		capiv1.AddToScheme,
		v1beta1.AddToScheme,
		v1alpha1.AddToScheme,
		certmanagerv1.AddToScheme,
		gwv1.Install,
		gwv1alpha2.Install,
	}

	for _, f := range addToSchemeFuncs {
		if err := f(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}
