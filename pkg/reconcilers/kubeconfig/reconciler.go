package kubeconfig

import (
	"context"
	"fmt"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
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
	managementClusterClient *alias.ManagementClusterClient,
	apiServerServicePort int32,
	konnectivityClientUsername string,
	controllerUsername string,
) KubeconfigReconciler {
	return &kubeconfigReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			ManagementClusterClient: managementClusterClient,
			Tracer:                  tracing.GetTracer("kubeconfig"),
		},
		apiServerServicePort:       apiServerServicePort,
		konnectivityClientUsername: konnectivityClientUsername,
		controllerUsername:         controllerUsername,
	}
}

type kubeconfigReconciler struct {
	reconcilers.ManagementResourceReconciler
	apiServerServicePort       int32
	konnectivityClientUsername string
	controllerUsername         string
}

var _ KubeconfigReconciler = &kubeconfigReconciler{}

type kubeconfigConfig struct {
	Username          string
	SecretName        string
	CertificateName   string
	ApiServerEndpoint capiv2.APIEndpoint
	AdditionalLabels  map[string]string
}

func (kr *kubeconfigReconciler) ReconcileKubeconfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeconfigs",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("konnectivity.client.kubeconfig.name", kr.konnectivityClientUsername),
				attribute.String("controller.kubeconfig.name", kr.controllerUsername),
			)
			localEndpoint := capiv2.APIEndpoint{
				Host: "localhost",
				Port: 6443,
			}
			clusterInternalServiceEndpoint := capiv2.APIEndpoint{
				Host: names.GetInternalServiceHost(cluster),
				Port: kr.apiServerServicePort,
			}
			internalServiceEndpoint := capiv2.APIEndpoint{
				Host: names.GetServiceName(cluster),
				Port: kr.apiServerServicePort,
			}
			endpointMap := map[v1alpha1.KubeconfigEndpointType]capiv2.APIEndpoint{
				v1alpha1.KubeconfigEndpointTypeExternal: cluster.Spec.ControlPlaneEndpoint,
				v1alpha1.KubeconfigEndpointTypeInternal: internalServiceEndpoint,
			}
			kubeconfigs := []kubeconfigConfig{
				{
					Username:          "admin",
					SecretName:        fmt.Sprintf("%s-kubeconfig", cluster.Name),
					CertificateName:   names.GetAdminKubeconfigCertificateName(cluster),
					ApiServerEndpoint: cluster.Spec.ControlPlaneEndpoint,
				},
				{
					Username:          konstants.KubeControllerManager,
					CertificateName:   names.GetControllerManagerKubeconfigCertificateName(cluster),
					ApiServerEndpoint: internalServiceEndpoint,
				},
				{
					Username:          konstants.KubeScheduler,
					CertificateName:   names.GetSchedulerKubeconfigCertificateName(cluster),
					ApiServerEndpoint: internalServiceEndpoint,
				},
				{
					Username:          kr.konnectivityClientUsername,
					CertificateName:   names.GetKonnectivityClientKubeconfigCertificateName(cluster),
					ApiServerEndpoint: localEndpoint,
				},
				{
					Username:          kr.controllerUsername,
					CertificateName:   names.GetControlPlaneControllerKubeconfigCertificateName(cluster),
					ApiServerEndpoint: clusterInternalServiceEndpoint,
				},
			}

			for username, endpointType := range hostedControlPlane.Spec.CustomKubeconfigs {
				kubeconfigs = append(kubeconfigs, kubeconfigConfig{
					Username:          username,
					SecretName:        names.GetCustomKubeconfigSecretName(cluster, username),
					CertificateName:   names.GetCustomKubeconfigCertificateName(cluster, username),
					ApiServerEndpoint: endpointMap[endpointType],
					AdditionalLabels:  names.GetCustomKubeconfigLabels(username),
				})
			}

			for _, kubeconfig := range kubeconfigs {
				if kubeconfig.SecretName == "" {
					kubeconfig.SecretName = names.GetKubeconfigSecretName(cluster, kubeconfig.Username)
				}
				if err := kr.reconcileKubeconfig(ctx, cluster, kubeconfig); err != nil {
					return fmt.Errorf("failed to reconcile kubeconfig: %w", err)
				}
			}

			return nil
		},
	)
}

func (kr *kubeconfigReconciler) reconcileKubeconfig(
	ctx context.Context,
	cluster *capiv2.Cluster,
	kubeconfigConfig kubeconfigConfig,
) error {
	return tracing.WithSpan1(ctx, kr.Tracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("kubeconfig.name", kubeconfigConfig.Username),
				attribute.String("kubeconfig.certificate.name", kubeconfigConfig.CertificateName),
			)

			certSecret, err := kr.ManagementClusterClient.CoreV1().Secrets(cluster.Namespace).
				Get(ctx, kubeconfigConfig.CertificateName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get certificate secret: %w", err)
			}

			kubeconfig, err := kr.generateKubeconfigFromSecret(ctx,
				cluster,
				kubeconfigConfig.ApiServerEndpoint,
				kubeconfigConfig.Username,
				certSecret,
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
				metav1ac.OwnerReference().
					WithAPIVersion("v1").
					WithKind("Secret").
					WithName(certSecret.Name).
					WithUID(certSecret.UID),
				slices.Assign(
					kubeconfigConfig.AdditionalLabels,
					names.GetControlPlaneLabels(cluster, "kubeconfig"),
				),
				cluster.Namespace,
				kubeconfigConfig.SecretName,
				false,
				map[string][]byte{
					capisecretutil.KubeconfigDataName: kubeconfigBytes,
				},
				capiv2.ClusterSecretType,
			)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (kr *kubeconfigReconciler) generateKubeconfigFromSecret(
	ctx context.Context,
	cluster *capiv2.Cluster,
	apiEndpoint capiv2.APIEndpoint,
	userName string,
	certSecret *corev1.Secret,
) (*api.Config, error) {
	return tracing.WithSpan(ctx, kr.Tracer, "GenerateKubeconfig",
		func(ctx context.Context, span trace.Span) (*api.Config, error) {
			span.SetAttributes(
				attribute.String("kubeconfig.user", userName),
				attribute.String("kubeconfig.certificate.secret", certSecret.Name),
				attribute.String("kubeconfig.api.endpoint", apiEndpoint.String()),
			)
			clusterName := cluster.Name
			contextName := fmt.Sprintf("%s@%s", userName, clusterName)

			caSecret, err := kr.ManagementClusterClient.CoreV1().Secrets(cluster.Namespace).
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
