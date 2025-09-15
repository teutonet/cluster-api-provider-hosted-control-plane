package kubeconfig

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capisecretutil "sigs.k8s.io/cluster-api/util/secret"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
)

type KubeconfigReconciler interface {
	ReconcileKubeconfigs(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv2.Cluster,
	) error
}

func NewKubeconfigReconciler(
	kubernetesClient kubernetes.Interface,
	apiServerServicePort int32,
	konnectivityClientKubeconfigName string,
	controllerKubeconfigName string,
) KubeconfigReconciler {
	return &kubeconfigReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			KubernetesClient: kubernetesClient,
			Tracer:           tracing.GetTracer("kubeconfig"),
		},
		apiServerServicePort:             apiServerServicePort,
		konnectivityClientKubeconfigName: konnectivityClientKubeconfigName,
		controllerKubeconfigName:         controllerKubeconfigName,
	}
}

type kubeconfigReconciler struct {
	reconcilers.ManagementResourceReconciler
	apiServerServicePort             int32
	konnectivityClientKubeconfigName string
	controllerKubeconfigName         string
}

var _ KubeconfigReconciler = &kubeconfigReconciler{}

type kubeconfigConfig struct {
	Name                  string
	SecretName            string
	CertificateSecretName string
	ClusterName           string
	ApiServerEndpoint     capiv2.APIEndpoint
}

func (kr *kubeconfigReconciler) ReconcileKubeconfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeconfigs",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("konnectivity.client.kubeconfig.name", kr.konnectivityClientKubeconfigName),
				attribute.String("controller.kubeconfig.name", kr.controllerKubeconfigName),
			)
			localEndpoint := capiv2.APIEndpoint{
				Host: "localhost",
				Port: 6443,
			}
			controlPlaneName := hostedControlPlane.Name
			clusterInternalServiceEndpoint := capiv2.APIEndpoint{
				Host: names.GetInternalServiceHost(cluster),
				Port: kr.apiServerServicePort,
			}
			internalServiceEndpoint := capiv2.APIEndpoint{
				Host: names.GetServiceName(cluster),
				Port: kr.apiServerServicePort,
			}
			kubeconfigs := []kubeconfigConfig{
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
					Name:                  kr.konnectivityClientKubeconfigName,
					CertificateSecretName: names.GetKonnectivityClientKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     localEndpoint,
				},
				{
					Name:                  kr.controllerKubeconfigName,
					CertificateSecretName: names.GetControllerKubeconfigCertificateSecretName(cluster),
					ClusterName:           controlPlaneName,
					ApiServerEndpoint:     clusterInternalServiceEndpoint,
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

func (kr *kubeconfigReconciler) reconcileKubeconfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	kubeconfigConfig kubeconfigConfig,
) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("kubeconfig.name", kubeconfigConfig.Name),
				attribute.String("kubeconfig.certificate.secretName", kubeconfigConfig.CertificateSecretName),
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
			kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
			}
			return kr.ReconcileSecret(
				ctx,
				hostedControlPlane,
				cluster,
				cluster.Namespace,
				kubeconfigConfig.SecretName,
				map[string][]byte{
					capisecretutil.KubeconfigDataName: kubeconfigBytes,
				},
			)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (kr *kubeconfigReconciler) generateKubeconfig(
	ctx context.Context,
	cluster *capiv2.Cluster,
	apiEndpoint capiv2.APIEndpoint,
	userName string,
	kubeconfiCertificateSecretName string,
) (*api.Config, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "GenerateKubeconfig",
		func(ctx context.Context, span trace.Span) (*api.Config, error) {
			span.SetAttributes(
				attribute.String("kubeconfig.user", userName),
				attribute.String("kubeconfig.certificate.secret", kubeconfiCertificateSecretName),
				attribute.String("kubeconfig.api.endpoint", apiEndpoint.String()),
			)
			clusterName := cluster.Name
			contextName := fmt.Sprintf("%s@%s", userName, clusterName)

			certSecret, err := kr.KubernetesClient.CoreV1().Secrets(cluster.Namespace).
				Get(ctx, kubeconfiCertificateSecretName, metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to get certificate secret: %w", err)
			}

			caSecret, err := kr.KubernetesClient.CoreV1().Secrets(cluster.Namespace).
				Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to get CA secret: %w", err)
			}

			return &api.Config{
				Clusters: map[string]*api.Cluster{
					clusterName: {
						Server:                   fmt.Sprintf("https://%s", apiEndpoint.String()),
						CertificateAuthorityData: caSecret.Data[corev1.TLSCertKey],
					},
				},
				Contexts: map[string]*api.Context{
					contextName: {
						Cluster:  clusterName,
						AuthInfo: userName,
					},
				},
				CurrentContext: contextName,
				AuthInfos: map[string]*api.AuthInfo{
					userName: {
						ClientCertificateData: certSecret.Data[corev1.TLSCertKey],
						ClientKeyData:         certSecret.Data[corev1.TLSPrivateKeyKey],
					},
				},
			}, nil
		},
	)
}
