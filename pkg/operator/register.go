// Package operator contains the whole schema for the operator.
package operator

import (
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/api/controlplane/kubeadm/v1beta2"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	addToSchemeFuncs := []func(*runtime.Scheme) error{
		appsv1.AddToScheme,
		capiv2.AddToScheme,
		certmanagerv1.AddToScheme,
		ciliumv2.AddToScheme,
		corev1.AddToScheme,
		gwv1.Install,
		gwv1alpha2.Install,
		networkingv1.AddToScheme,
		policyv1.AddToScheme,
		v1alpha1.AddToScheme,
		v1beta2.AddToScheme,
	}

	for _, f := range addToSchemeFuncs {
		if err := f(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}
