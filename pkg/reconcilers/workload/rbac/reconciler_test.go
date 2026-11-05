package rbac

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/importcycle"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestRBACReconciler(client *fake.Clientset, metricsScrapingEnabled bool) RBACReconciler {
	return NewRBACReconciler(
		&alias.WorkloadClusterClient{Interface: client},
		metav1.NamespaceSystem, "kubeadm-config", "kubelet-config", metav1.NamespacePublic, "cluster-info",
		metricsScrapingEnabled,
	)
}

func TestReconcileRBAC_MetricsScraping(t *testing.T) {
	g, ctx, _ := G(t)
	client := fake.NewClientset()

	notReady, err := newTestRBACReconciler(client, true).ReconcileRBAC(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	role, err := client.RbacV1().Roles(metav1.NamespaceSystem).Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(role.Rules).To(ConsistOf(
		rbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch"},
		},
		rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/proxy"}, Verbs: []string{"get"}},
	))
	clusterRole, err := client.RbacV1().ClusterRoles().Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(clusterRole.Rules).To(ConsistOf(
		rbacv1.PolicyRule{NonResourceURLs: []string{"/metrics"}, Verbs: []string{"get"}},
	))
	clusterRoleBinding, err := client.RbacV1().ClusterRoleBindings().
		Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(clusterRoleBinding.RoleRef.Name).To(Equal(metricsScrapingRBACName))
	g.Expect(clusterRoleBinding.Subjects).To(ConsistOf(MatchFields(IgnoreExtras, Fields{
		"Kind": Equal(rbacv1.UserKind),
		"Name": Equal(importcycle.MetricsClientUsername),
	})))
	roleBinding, err := client.RbacV1().RoleBindings(metav1.NamespaceSystem).
		Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(roleBinding.RoleRef.Name).To(Equal(metricsScrapingRBACName))
	g.Expect(roleBinding.Subjects).To(ConsistOf(MatchFields(IgnoreExtras, Fields{
		"Kind": Equal(rbacv1.UserKind),
		"Name": Equal(importcycle.MetricsClientUsername),
	})))
}

func TestReconcileRBAC_MetricsScrapingRemovedWhenDisabled(t *testing.T) {
	g, ctx, _ := G(t)
	client := fake.NewClientset()
	_, err := newTestRBACReconciler(client, true).ReconcileRBAC(ctx)
	g.Expect(err).NotTo(HaveOccurred())

	notReady, err := newTestRBACReconciler(client, false).ReconcileRBAC(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	_, err = client.RbacV1().Roles(metav1.NamespaceSystem).Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	_, err = client.RbacV1().RoleBindings(metav1.NamespaceSystem).Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	_, err = client.RbacV1().ClusterRoles().Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	_, err = client.RbacV1().ClusterRoleBindings().Get(ctx, metricsScrapingRBACName, metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

func TestReconcileRBAC_NoMetricsScrapingRBACWhenNeverEnabled(t *testing.T) {
	g, ctx, _ := G(t)

	_, err := newTestRBACReconciler(fake.NewClientset(), false).ReconcileRBAC(ctx)

	g.Expect(err).NotTo(HaveOccurred())
}
