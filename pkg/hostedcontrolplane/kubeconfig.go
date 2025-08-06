package hostedcontrolplane

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/tools/clientcmd/api/v1"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
)

type KubeconfigConfig struct {
	Name                  string
	SecretName            string
	CertificateSecretName string
	CertName              string
	ClusterName           string
	ApiServerEndpoint     capiv1.APIEndpoint
}

type KubeconfigReconciler struct {
	kubernetesClient kubernetes.Interface
}

func (kr *KubeconfigReconciler) ReconcileKubeconfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	endpoint capiv1.APIEndpoint,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			localEndpoint := capiv1.APIEndpoint{
				Host: "localhost",
				Port: 6443,
			}
			kubeconfigs := []KubeconfigConfig{
				{
					Name:                  "admin",
					SecretName:            names.GetKubeconfigSecretName(hostedControlPlane.Name, ""),
					CertificateSecretName: names.GetAdminSecretName(hostedControlPlane.Name),
					CertName:              names.GetAdminCertificateName(hostedControlPlane.Name),
					ClusterName:           hostedControlPlane.Name,
					ApiServerEndpoint:     endpoint,
				},
				{
					Name:                  konstants.KubeControllerManager,
					CertificateSecretName: names.GetControllerManagerSecretName(hostedControlPlane.Name),
					CertName:              names.GetControllerManagerCertificateName(hostedControlPlane.Name),
					ClusterName:           hostedControlPlane.Name,
					ApiServerEndpoint:     localEndpoint,
				},
				{
					Name:                  konstants.KubeScheduler,
					CertificateSecretName: names.GetSchedulerSecretName(hostedControlPlane.Name),
					CertName:              names.GetSchedulerCertificateName(hostedControlPlane.Name),
					ClusterName:           hostedControlPlane.Name,
					ApiServerEndpoint:     localEndpoint,
				},
				{
					Name:                  "konnectivity-client",
					CertificateSecretName: names.GetKonnectivityClientSecretName(hostedControlPlane.Name),
					CertName:              names.GetKonnectivityClientCertificateName(hostedControlPlane.Name),
					ClusterName:           hostedControlPlane.Name,
					ApiServerEndpoint:     localEndpoint,
				},
			}

			for _, kubeconfig := range kubeconfigs {
				if kubeconfig.SecretName == "" {
					kubeconfig.SecretName = names.GetKubeconfigSecretName(hostedControlPlane.Name, kubeconfig.Name)
				}
				if err := kr.reconcileKubeconfig(ctx, hostedControlPlane, kubeconfig); err != nil {
					return fmt.Errorf("failed to reconcile kubeconfig %s: %w", kubeconfig.Name, err)
				}
			}

			return nil
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch

func (kr *KubeconfigReconciler) reconcileKubeconfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	kubeconfig KubeconfigConfig,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("KubeconfigName", kubeconfig.Name),
				attribute.String("CertificateSecretName", kubeconfig.CertificateSecretName),
				attribute.String("CertificateName", kubeconfig.CertName),
			)

			certSecret, err := kr.kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Get(ctx, kubeconfig.CertificateSecretName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("certificate secret %s not found: %w", kubeconfig.CertificateSecretName, err)
				}
				return fmt.Errorf("failed to get certificate secret %s: %w", kubeconfig.CertificateSecretName, err)
			}

			caSecret, err := kr.kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Get(ctx, names.GetCASecretName(hostedControlPlane.Name), metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("CA secret %s not found: %w", names.GetCASecretName(hostedControlPlane.Name), err)
				}
				return fmt.Errorf("failed to get CA secret %s: %w", names.GetCASecretName(hostedControlPlane.Name), err)
			}

			kubeconfigSecret := corev1ac.Secret(kubeconfig.SecretName, hostedControlPlane.Namespace).
				WithLabels(names.GetLabels(hostedControlPlane.Name)).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string][]byte{
					capisecretutil.KubeconfigDataName: kr.generateKubeconfig(
						kubeconfig.ApiServerEndpoint,
						kubeconfig.ClusterName,
						kubeconfig.Name,
						certSecret.Data[corev1.TLSCertKey],
						certSecret.Data[corev1.TLSPrivateKeyKey],
						caSecret.Data[corev1.TLSCertKey],
					),
				})

			_, err = kr.kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Apply(ctx, kubeconfigSecret, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch kubeconfig secret: %w", err)
		},
	)
}

func (kr *KubeconfigReconciler) generateKubeconfig(
	endpoint capiv1.APIEndpoint,
	clusterName string,
	userName string,
	clientCert []byte,
	clientKey []byte,
	caCert []byte,
) []byte {
	contextName := fmt.Sprintf("%s@%s", userName, clusterName)
	apiServerURL := fmt.Sprintf("https://%s", endpoint.String())

	kubeconfig := api.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: []api.NamedCluster{
			{
				Name: clusterName,
				Cluster: api.Cluster{
					Server:                   apiServerURL,
					CertificateAuthorityData: caCert,
				},
			},
		},
		Contexts: []api.NamedContext{
			{
				Name: contextName,
				Context: api.Context{
					Cluster:  clusterName,
					AuthInfo: userName,
				},
			},
		},
		CurrentContext: contextName,
		AuthInfos: []api.NamedAuthInfo{
			{
				Name: userName,
				AuthInfo: api.AuthInfo{
					ClientCertificateData: clientCert,
					ClientKeyData:         clientKey,
				},
			},
		},
	}

	data, err := ToYaml(&kubeconfig)
	if err != nil {
		panic(err)
	}

	return data.Bytes()
}
