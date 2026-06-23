package node_rotation

import (
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmfake "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/fake"
	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	testCluster = &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-cluster-uid"),
		},
	}
	testHCP = &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "default",
			UID:       types.UID("test-hcp-uid"),
		},
	}
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := capiv2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add capiv2 scheme: %v", err)
	}
	return s
}

func caCertificate(notBefore time.Time) *cmv1.Certificate {
	nb := metav1.NewTime(notBefore)
	return &cmv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.GetCACertificateName(testCluster),
			Namespace: testHCP.Namespace,
		},
		Status: cmv1.CertificateStatus{
			NotBefore: &nb,
		},
	}
}

func machineDeployment(name string) *capiv2.MachineDeployment {
	return &capiv2.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testCluster.Namespace,
			Labels:    map[string]string{capiv2.ClusterNameLabel: testCluster.Name},
		},
	}
}

func machinePool(name string) *capiv2.MachinePool {
	return &capiv2.MachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testCluster.Namespace,
			Labels:    map[string]string{capiv2.ClusterNameLabel: testCluster.Name},
		},
	}
}

func TestReconcileCARotation_SetsMachineDeploymentRolloutAfter(t *testing.T) {
	g, ctx, _ := G(t)
	notBefore := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cmClient := cmfake.NewClientset(caCertificate(notBefore))
	md := machineDeployment("md-1")
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(md).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmClient, tracer: "test"}

	notReady, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	updated := &capiv2.MachineDeployment{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: "md-1", Namespace: testCluster.Namespace}, updated)).To(Succeed())
	expected := metav1.NewTime(notBefore)
	g.Expect(updated.Spec.Rollout.After.Equal(&expected)).To(BeTrue())
}

func TestReconcileCARotation_AnnotatesMachinePoolTemplate(t *testing.T) {
	g, ctx, _ := G(t)
	notBefore := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cmClient := cmfake.NewClientset(caCertificate(notBefore))
	mp := machinePool("mp-1")
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(mp).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmClient, tracer: "test"}

	notReady, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())

	updated := &capiv2.MachinePool{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: "mp-1", Namespace: testCluster.Namespace}, updated)).To(Succeed())
	g.Expect(updated.Spec.Template.Annotations).To(HaveKey(caNotBeforeAnnotation))
	g.Expect(updated.Spec.Template.Annotations[caNotBeforeAnnotation]).
		To(Equal(notBefore.UTC().Format("2006-01-02T15:04:05Z07:00")))
}

func TestReconcileCARotation_SkipsAlreadySetRolloutAfter(t *testing.T) {
	g, ctx, _ := G(t)
	notBefore := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cmClient := cmfake.NewClientset(caCertificate(notBefore))
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(machineDeployment("md-1")).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmClient, tracer: "test"}

	_, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())

	annotated := &capiv2.MachineDeployment{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: "md-1", Namespace: testCluster.Namespace}, annotated)).To(Succeed())

	rvBefore := annotated.ResourceVersion
	_, err = r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())

	after := &capiv2.MachineDeployment{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: "md-1", Namespace: testCluster.Namespace}, after)).To(Succeed())
	g.Expect(after.ResourceVersion).To(Equal(rvBefore))
}

func TestReconcileCARotation_UpdatesRolloutAfterOnCAChange(t *testing.T) {
	g, ctx, _ := G(t)
	notBeforeV1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	notBeforeV2 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	md := machineDeployment("md-1")
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(md).Build()

	r := &nodeRotationReconciler{
		client:            cl,
		certManagerClient: cmfake.NewClientset(caCertificate(notBeforeV1)),
		tracer:            "test",
	}
	_, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())

	r.certManagerClient = cmfake.NewClientset(caCertificate(notBeforeV2))
	_, err = r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())

	updated := &capiv2.MachineDeployment{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: "md-1", Namespace: testCluster.Namespace}, updated)).To(Succeed())
	expected := metav1.NewTime(notBeforeV2)
	g.Expect(updated.Spec.Rollout.After.Equal(&expected)).To(BeTrue())
}

func TestReconcileCARotation_NothingToAnnotate(t *testing.T) {
	g, ctx, _ := G(t)
	notBefore := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cmClient := cmfake.NewClientset(caCertificate(notBefore))
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmClient, tracer: "test"}

	notReady, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).To(BeEmpty())
}

func TestReconcileCARotation_MissingCACert_NotReady(t *testing.T) {
	g, ctx, _ := G(t)
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmfake.NewClientset(), tracer: "test"}

	notReady, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).NotTo(BeEmpty())
}

func TestReconcileCARotation_CACertNotYetIssued_NotReady(t *testing.T) {
	g, ctx, _ := G(t)
	cert := &cmv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.GetCACertificateName(testCluster),
			Namespace: testHCP.Namespace,
		},
		Status: cmv1.CertificateStatus{}, // NotBefore is nil
	}
	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmfake.NewClientset(cert), tracer: "test"}

	notReady, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(notReady).NotTo(BeEmpty())
}

func TestReconcileCARotation_IgnoresOtherClusters(t *testing.T) {
	g, ctx, _ := G(t)
	notBefore := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cmClient := cmfake.NewClientset(caCertificate(notBefore))

	otherMD := machineDeployment("md-other")
	otherMD.Labels[capiv2.ClusterNameLabel] = "other-cluster"

	cl := fakeClient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(otherMD).Build()
	r := &nodeRotationReconciler{client: cl, certManagerClient: cmClient, tracer: "test"}

	_, err := r.ReconcileCARotation(ctx, testHCP, testCluster)
	g.Expect(err).NotTo(HaveOccurred())

	unchanged := &capiv2.MachineDeployment{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: "md-other", Namespace: testCluster.Namespace}, unchanged)).
		To(Succeed())
	g.Expect(unchanged.Spec.Rollout.After.IsZero()).To(BeTrue())
}
