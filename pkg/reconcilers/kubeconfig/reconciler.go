package kubeconfig

import (
	"context"
	"fmt"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
		cluster *capiv1.Cluster,
	) error
}

func NewKubeconfigReconciler(
	kubernetesClient kubernetes.Interface,
	apiServerServicePort int32,
	konnectivityClientKubeconfigName string,
	controllerKubeconfigName string,
) KubeconfigReconciler {
	return &kubeconfigReconciler{
		kubernetesClient:                 kubernetesClient,
		apiServerServicePort:             apiServerServicePort,
		konnectivityClientKubeconfigName: konnectivityClientKubeconfigName,
		controllerKubeconfigName:         controllerKubeconfigName,
		tracer:                           tracing.GetTracer("kubeconfig"),
	}
}

type kubeconfigReconciler struct {
	kubernetesClient                 kubernetes.Interface
	apiServerServicePort             int32
	konnectivityClientKubeconfigName string
	controllerKubeconfigName         string
	tracer                           string
}

var _ KubeconfigReconciler = &kubeconfigReconciler{}

type kubeconfigConfig struct {
	Name                  string
	SecretName            string
	CertificateSecretName string
	ClusterName           string
	ApiServerEndpoint     capiv1.APIEndpoint
}

func (kr *kubeconfigReconciler) ReconcileKubeconfigs(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, kr.tracer, "ReconcileKubeconfig",
		func(ctx context.Context, span trace.Span) error {
			localEndpoint := capiv1.APIEndpoint{
				Host: "localhost",
				Port: 6443,
			}
			controlPlaneName := hostedControlPlane.Name
			internalServiceEndpoint := capiv1.APIEndpoint{
				Host: names.GetInternalServiceHost(cluster),
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

func (kr *kubeconfigReconciler) reconcileKubeconfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	kubeconfigConfig kubeconfigConfig,
) error {
	return tracing.WithSpan1(ctx, kr.tracer, "ReconcileKubeconfig",
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
			kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
			if err != nil {
				return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
			}
			kubeconfigSecret := corev1ac.Secret(kubeconfigConfig.SecretName, cluster.Namespace).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string][]byte{
					capisecretutil.KubeconfigDataName: kubeconfigBytes,
				})

			_, err = kr.kubernetesClient.CoreV1().Secrets(*kubeconfigSecret.Namespace).
				Apply(ctx, kubeconfigSecret, operatorutil.ApplyOptions)
			return errorsUtil.IfErrErrorf("failed to patch kubeconfig secret: %w", err)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (kr *kubeconfigReconciler) generateKubeconfig(
	ctx context.Context,
	cluster *capiv1.Cluster,
	apiEndpoint capiv1.APIEndpoint,
	userName string,
	kubeconfiCertificateSecretName string,
) (*api.Config, error) {
	return tracing.WithSpan(ctx, kr.tracer, "GenerateKubeconfig",
		func(ctx context.Context, span trace.Span) (*api.Config, error) {
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
