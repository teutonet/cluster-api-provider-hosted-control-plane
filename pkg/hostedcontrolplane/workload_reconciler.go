package hostedcontrolplane

import (
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/bootstraptoken/clusterinfo"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/bootstraptoken/node"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	workloadClusterReconcilerTracer = "WorkloadClusterReconciler"
)

type WorkloadClusterReconciler struct {
	kubernetesClient kubernetes.Interface
}

var workloadApplyOptions = metav1.ApplyOptions{
	FieldManager: "workload-cluster-reconciler",
	Force:        true,
}

//noling:lll // urls are long 🤷

// ReconcileWorkloadRBAC mimics kuebadm phases, e.g.
// - https://github.com/kubernetes/kubernetes/blob/6f06cd6e05704a9a7b18e74a048a297e5bdb5498/cmd/kubeadm/app/cmd/phases/init/bootstraptoken.go#L65
func (wr *WorkloadClusterReconciler) ReconcileWorkloadRBAC(
	ctx context.Context,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, workloadClusterReconcilerTracer, "ReconcileWorkloadRBAC",
		func(ctx context.Context, span trace.Span) error {
			kubeadmPhases := []func(kubernetes.Interface) error{
				node.AllowBootstrapTokensToGetNodes,
				node.AllowBootstrapTokensToPostCSRs,
				node.AutoApproveNodeBootstrapTokens,
				node.AutoApproveNodeCertificateRotation,
				clusterinfo.CreateClusterInfoRBACRules,
			}

			for _, phase := range kubeadmPhases {
				if err := phase(wr.kubernetesClient); err != nil {
					return fmt.Errorf("failed to reconcile kubeadm phase: %w", err)
				}
			}

			if err := wr.reconcileClusterInfoConfigMap(ctx, cluster); err != nil {
				return fmt.Errorf("failed to reconcile cluster info configmap: %w", err)
			}

			if err := wr.reconcileClusterAdminBinding(ctx); err != nil {
				return fmt.Errorf("failed to reconcile cluster admin binding: %w", err)
			}

			return nil
		},
	)
}

func (wr *WorkloadClusterReconciler) reconcileClusterAdminBinding(
	ctx context.Context,
) error {
	clusterRoleBinding := rbacv1ac.ClusterRoleBinding(konstants.ClusterAdminsGroupAndClusterRoleBinding).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup(rbacv1.GroupName).
				WithKind(rbacv1.ClusterRole{}.Kind).
				WithName("cluster-admin"),
		).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind(rbacv1.GroupKind).
				WithName(konstants.ClusterAdminsGroupAndClusterRoleBinding),
		)

	_, err := wr.kubernetesClient.RbacV1().
		ClusterRoleBindings().
		Apply(ctx, clusterRoleBinding, workloadApplyOptions)
	return errorsUtil.IfErrErrorf("failed to apply cluster admin binding: %w", err)
}

func (wr *WorkloadClusterReconciler) reconcileClusterInfoConfigMap(
	ctx context.Context,
	cluster *capiv1.Cluster,
) error {
	caSecret, err := wr.kubernetesClient.CoreV1().Secrets(cluster.Namespace).
		Get(ctx, names.GetCASecretName(cluster), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CA secret: %w", err)
	}
	kubeconfig := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"": {
				Server:                   cluster.Spec.ControlPlaneEndpoint.String(),
				CertificateAuthorityData: caSecret.Data[corev1.TLSCertKey],
			},
		},
	}
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errorsUtil.IfErrErrorf("failed to marshal kubeconfig: %w", err)
	}

	configMap := corev1ac.ConfigMap(bootstrapapi.ConfigMapClusterInfo, metav1.NamespacePublic).
		WithData(map[string]string{
			bootstrapapi.KubeConfigKey: string(kubeconfigBytes),
		})

	_, err = wr.kubernetesClient.CoreV1().
		ConfigMaps(metav1.NamespacePublic).
		Apply(ctx, configMap, workloadApplyOptions)
	return errorsUtil.IfErrErrorf("failed to apply cluster info configmap: %w", err)
}
