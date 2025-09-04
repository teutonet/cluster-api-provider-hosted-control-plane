// Package t8soperator contains the whole schema for the operator.
package operator

import (
	"github.com/teutonet/cluster-api-control-plane-provder-hcp/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	kubeadmv1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
)

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	addToSchemeFuncs := []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		clusterv1.AddToScheme,
		kubeadmv1.AddToScheme,
		v1alpha1.AddToScheme,
	}

	for _, f := range addToSchemeFuncs {
		if err := f(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}
