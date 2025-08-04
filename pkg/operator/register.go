// Package operator contains the whole schema for the operator.
package operator

import (
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	kubeadmv1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	addToSchemeFuncs := []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		capiv1.AddToScheme,
		kubeadmv1.AddToScheme,
		v1alpha1.AddToScheme,
		certmanagerv1.AddToScheme,
		gwv1.Install,
	}

	for _, f := range addToSchemeFuncs {
		if err := f(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}
