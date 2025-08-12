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

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
)

type KubeconfigConfig struct {
	Name                  string
	SecretName            string
	CertificateSecretName string
	ClusterName           string
	ApiServerEndpoint     capiv1.APIEndpoint
}

type KubeconfigReconciler struct {
	kubernetesClient kubernetes.Interface
}

func (kr *KubeconfigReconciler) ReconcileKubeconfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			localEndpoint := capiv1.APIEndpoint{
				Host: "localhost",
				Port: 6443,
			}
			controlPlaneName := hostedControlPlane.Name
			internalServiceHost := names.GetInternalServiceHost(cluster)
			internalServiceEndpoint := capiv1.APIEndpoint{
				Host: internalServiceHost,
				Port: 443,
			}
			kubeconfigs := []KubeconfigConfig{
				{
					Name:                  "admin",
					SecretName:            fmt.Sprintf("%s-kubeconfig", cluster.Name),
					CertificateSecretName: names.GetAdminKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     cluster.Spec.ControlPlaneEndpoint,
				},
				{
					Name:                  konstants.KubeControllerManager,
					CertificateSecretName: names.GetControllerManagerKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     internalServiceEndpoint,
				},
				{
					Name:                  konstants.KubeScheduler,
					CertificateSecretName: names.GetSchedulerKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     internalServiceEndpoint,
				},
				{
					Name:                  "konnectivity-client",
					CertificateSecretName: names.GetKonnectivityClientKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     localEndpoint,
				},
				{
					Name:                  "controller",
					CertificateSecretName: names.GetControllerKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     internalServiceEndpoint,
				},
			}

			for _, kubeconfig := range kubeconfigs {
				if kubeconfig.SecretName == "" {
					kubeconfig.SecretName = names.GetKubeconfigSecretName(cluster, kubeconfig.Name)
				}
				if err := kr.reconcileKubeconfig(ctx, hostedControlPlane, cluster, kubeconfig); err != nil {
					return fmt.Errorf("failed to reconcile kubeconfig: %w", err)
				}
			}

			return nil
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;patch

func (kr *KubeconfigReconciler) reconcileKubeconfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	kubeconfig KubeconfigConfig,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("KubeconfigName", kubeconfig.Name),
				attribute.String("CertificateSecretName", kubeconfig.CertificateSecretName),
			)

			certSecret, err := kr.kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Get(ctx, kubeconfig.CertificateSecretName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("certificate secret not found: %w", err)
				}
				return fmt.Errorf("failed to get certificate secret: %w", err)
			}

			caSecret, err := kr.kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace).
				Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("CA secret not found: %w", err)
				}
				return fmt.Errorf("failed to get CA secret: %w", err)
			}

			kubeconfigSecret := corev1ac.Secret(kubeconfig.SecretName, hostedControlPlane.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
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
