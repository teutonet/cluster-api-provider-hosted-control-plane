package hostedcontrolplane

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	kubeconfigv1 "k8s.io/client-go/tools/clientcmd/api/v1"
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
	kubeconfigConfig KubeconfigConfig,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("KubeconfigName", kubeconfigConfig.Name),
				attribute.String("CertificateSecretName", kubeconfigConfig.CertificateSecretName),
			)

			kubeconfig, err := kr.generateKubeconfig(ctx,
				cluster,
				kubeconfigConfig.ApiServerEndpoint,
				kubeconfigConfig.Name,
				kubeconfigConfig.CertificateSecretName,
			)
			if err != nil {
				return fmt.Errorf("failed to generate kubeconfig: %w", err)
			}
			yaml, err := util.ToYaml(kubeconfig)
			if err != nil {
				return fmt.Errorf("failed to marshal kubeconfig: %w", err)
			}
			kubeconfigSecret := corev1ac.Secret(kubeconfigConfig.SecretName, cluster.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string][]byte{
					capisecretutil.KubeconfigDataName: yaml.Bytes(),
				})

			_, err = kr.kubernetesClient.CoreV1().Secrets(cluster.Namespace).
				Apply(ctx, kubeconfigSecret, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch kubeconfig secret: %w", err)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (kr *KubeconfigReconciler) generateKubeconfig(
	ctx context.Context,
	cluster *capiv1.Cluster,
	apiEndpoint capiv1.APIEndpoint,
	userName string,
	kubeconfiCertificateSecretName string,
) (*kubeconfigv1.Config, error) {
	clusterName := cluster.Name
	contextName := fmt.Sprintf("%s@%s", userName, clusterName)

	certSecret, err := kr.kubernetesClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, kubeconfiCertificateSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate secret: %w", err)
	}

	caSecret, err := kr.kubernetesClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get CA secret: %w", err)
	}

	kubeconfig := kubeconfigv1.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: []kubeconfigv1.NamedCluster{
			{
				Name: clusterName,
				Cluster: kubeconfigv1.Cluster{
					Server:                   fmt.Sprintf("https://%s", apiEndpoint.String()),
					CertificateAuthorityData: caSecret.Data[corev1.TLSCertKey],
				},
			},
		},
		Contexts: []kubeconfigv1.NamedContext{
			{
				Name: contextName,
				Context: kubeconfigv1.Context{
					Cluster:  clusterName,
					AuthInfo: userName,
				},
			},
		},
		CurrentContext: contextName,
		AuthInfos: []kubeconfigv1.NamedAuthInfo{
			{
				Name: userName,
				AuthInfo: kubeconfigv1.AuthInfo{
					ClientCertificateData: certSecret.Data[corev1.TLSCertKey],
					ClientKeyData:         certSecret.Data[corev1.TLSPrivateKeyKey],
				},
			},
		},
	}

	return &kubeconfig, nil
}
