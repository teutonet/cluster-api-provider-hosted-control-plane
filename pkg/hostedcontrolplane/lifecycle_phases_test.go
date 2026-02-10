package hostedcontrolplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagerv1ac "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations/certmanager/v1"
	certmanagerfake "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/fake"
	cmclient "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	types2 "github.com/onsi/gomega/types"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/etcd_cluster/etcd_client"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/etcd_cluster/s3_client"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	v2 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/cluster-bootstrap/token/api"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/pkg/controller/certificates/rootcacertpublisher"
	"k8s.io/utils/ptr"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/contract"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	v1 "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	"sigs.k8s.io/gateway-api/applyconfiguration/apis/v1alpha2"
	gwfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

var (
	certManagerFieldManager = "cert-manager"
	certManagerOptions      = metav1.ApplyOptions{FieldManager: certManagerFieldManager}
	k8sFieldManager         = "kubernetes"
	k8sOptions              = metav1.ApplyOptions{FieldManager: k8sFieldManager}
	gatewayFieldManager     = "gateway-api"
	gatewayOptions          = metav1.ApplyOptions{FieldManager: gatewayFieldManager}
)

type testPhase struct {
	name string
	// Function to patch HCP BEFORE reconciliation
	patchHCP func()
	// Function to patch Cluster BEFORE reconciliation
	patchCluster func()
	// Function to simulate external systems BEFORE reconciliation
	// (e.g., cert-manager creating secrets, marking resources as ready)
	simulateExternalSystems func(context.Context, *WithT)
	verifyConditionsBefore  map[bool][]types2.GomegaMatcher
	verifyConditionsAfter   map[bool][]types2.GomegaMatcher
	// Custom resource verifications AFTER reconciliation and simulation
	verifyResources func(context.Context, *WithT)
	expectError     string
	expectNoRequeue bool
}

func NewConditionVerification(
	conditionType string,
	reasonMatchers ...types2.GomegaMatcher,
) types2.GomegaMatcher {
	var conditionMatchers []types2.GomegaMatcher

	if conditionType != "" {
		conditionMatchers = append(conditionMatchers, HaveField("Type", Equal(conditionType)))
	}
	if len(reasonMatchers) > 0 {
		var reasonMatcher types2.GomegaMatcher
		if len(reasonMatchers) == 1 {
			reasonMatcher = reasonMatchers[0]
		} else {
			reasonMatcher = And(reasonMatchers...)
		}
		conditionMatchers = append(conditionMatchers, HaveField("Reason", reasonMatcher))
	}

	return And(conditionMatchers...)
}

// TestHostedControlPlane_FullLifecycle tests the complete lifecycle through all phases.
func TestHostedControlPlane_FullLifecycle(t *testing.T) {
	logger := logr.FromSlogHandler(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // Set to LevelDebug for verbose output
	}))
	ctx := log.IntoContext(t.Context(), logger)
	g := NewWithT(t)

	scheme, err := NewScheme()
	g.Expect(err).To(Succeed())

	infraGroup := "infrastructure.cluster.x-k8s.io"
	infraVersion := "v1beta1"
	infraClusterKind := "InfraCluster"
	infraClusterName := "test-cluster"
	infraClusterCRD := createInfraClusterCRD(infraGroup, infraVersion, infraClusterKind)
	infraCluster := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": fmt.Sprintf("%s/%s", infraGroup, infraVersion),
			"kind":       infraClusterKind,
			"metadata": map[string]interface{}{
				"name":      infraClusterName,
				"namespace": "default",
				"uid":       string(uuid.NewUUID()),
			},
			"spec":   map[string]interface{}{},
			"status": map[string]interface{}{},
		},
	}

	cluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       uuid.NewUUID(),
		},
		Spec: capiv2.ClusterSpec{
			InfrastructureRef: capiv2.ContractVersionedObjectReference{
				APIGroup: infraGroup,
				Kind:     infraClusterKind,
				Name:     infraClusterName,
			},
			ClusterNetwork: capiv2.ClusterNetwork{
				Services: capiv2.NetworkRanges{
					CIDRBlocks: []string{"10.96.0.0/12"},
				},
				Pods: capiv2.NetworkRanges{
					CIDRBlocks: []string{"10.244.0.0/16"},
				},
				ServiceDomain: "cluster.local",
			},
		},
		Status: capiv2.ClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:   capiv2.PausedCondition,
					Status: metav1.ConditionFalse,
					Reason: capiv2.NotPausedReason,
				},
			},
		},
	}

	hcp := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "default",
			UID:       uuid.NewUUID(),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: capiv2.GroupVersion.String(),
					Kind:       "Cluster",
					Name:       cluster.Name,
					UID:        cluster.UID,
				},
			},
		},
		Spec: v1alpha1.HostedControlPlaneSpec{
			Version: "v1.28.0",
			HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
				Gateway: v1alpha1.GatewayReference{
					Namespace: "default",
					Name:      "test-gateway",
				},
				ETCD: v1alpha1.ETCDComponent{
					AutoGrow:   ptr.To(false),
					VolumeSize: ptr.To(resource.MustParse("1Gi")),
				},
			},
		},
	}
	hostedControlPlaneWebhook := v1alpha1.NewHostedControlPlaneWebhook()
	g.Expect(hostedControlPlaneWebhook.ValidateCreate(ctx, hcp)).Error().To(Succeed())

	gatewayClassName := "test-gateway-class"
	gateway := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gateway",
			Namespace: "default",
		},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: gwv1.ObjectName(gatewayClassName),
			Listeners: []gwv1.Listener{
				{
					Name:     "tls",
					Protocol: gwv1.TLSProtocolType,
					Port:     443,
					Hostname: ptr.To(gwv1.Hostname("*.example.com")),
				},
			},
		},
	}

	k8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		// gwfake.NewClientset(gateway) used the wrong kind (gatewaies)
		WithObjects(infraClusterCRD, infraCluster, gateway, cluster, hcp).
		WithStatusSubresource(&v1alpha1.HostedControlPlane{}).
		WithStatusSubresource(&capiv2.Cluster{}).
		Build()

	managementClusterClient := &alias.ManagementClusterClient{Interface: fake.NewClientset()}
	var managementClusterCiliumClient cmclient.Interface = nil
	workloadClusterClient := &alias.WorkloadClusterClient{Interface: fake.NewClientset()}
	var workloadClusterCiliumClient cmclient.Interface = nil
	certManagerclient := certmanagerfake.NewClientset()
	gatewayInterface := gwfake.NewClientset()
	etcdClient := &EtcdClientStub{
		StatusError: errors.New("statefulset offline"),
		AlarmError:  errors.New("statefulset offline"),
	}
	etcdClientFactory := func(_ context.Context, _ *alias.ManagementClusterClient, _ *v1alpha1.HostedControlPlane,
		_ *capiv2.Cluster, _ int32,
	) (etcd_client.EtcdClient, error) {
		return etcdClient, nil
	}
	s3ClientFactory := func(_ context.Context, _ *alias.ManagementClusterClient, _ *v1alpha1.HostedControlPlane,
		_ *capiv2.Cluster,
	) (s3_client.S3Client, error) {
		return NewS3ClientStub(), nil
	}
	workloadClusterClientFactory := func(
		_ context.Context,
		_ *alias.ManagementClusterClient,
		_ *capiv2.Cluster,
		_ string,
	) (*alias.WorkloadClusterClient, cmclient.Interface, error) {
		return workloadClusterClient, workloadClusterCiliumClient, nil
	}
	reconciler := NewHostedControlPlaneReconciler(
		k8sClient,
		managementClusterClient,
		certManagerclient,
		gatewayInterface,
		func(ctx context.Context) (cmclient.Interface, error) {
			return managementClusterCiliumClient, nil
		},
		workloadClusterClientFactory,
		etcdClientFactory,
		s3ClientFactory,
		&recorder.InfiniteDiscardingFakeRecorder{},
		"default",
	)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      hcp.Name,
			Namespace: hcp.Namespace,
		},
	}

	phases := []testPhase{
		{
			name: "Verify Paused and ExternalManagedControlPlane Status",
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						capiv2.PausedCondition,
						Equal(capiv2.NotPausedReason),
					),
				},
			},
			verifyResources: func(ctx context.Context, t *WithT) {
				g.Expect(hcp.Status.ExternalManagedControlPlane).To(PointTo(BeTrue()))
			},
		},
		{
			name: "Add Finalizer",
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(hcp.Finalizers).To(ContainElement("hcp.controlplane.cluster.x-k8s.io"))
			},
		},
		{
			name: "Create Root CA Issuer",
		},
		{
			name: "Make Root Issuer Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Equal("RootIssuerNotReady"),
					),
				},
			},
			simulateExternalSystems: makeIssuerReady(certManagerclient, cluster, "root"),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Not(Equal("RootIssuerNotReady")),
					),
				},
			},
		},
		{
			name: "Make kubernetes CA Certificate Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Equal("KubernetesCaCertificateNotReady"),
					),
				},
			},
			simulateExternalSystems: makeCertificateReady(
				certManagerclient, managementClusterClient, hcp, cluster, "ca",
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Not(Equal("KubernetesCaCertificateNotReady")),
					),
				},
			},
		},
		{
			name: "Make kubernetes CA Issuer Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Equal("KubernetesCaIssuerNotReady"),
					),
				},
			},
			simulateExternalSystems: makeIssuerReady(certManagerclient, cluster, "ca"),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Not(Equal("KubernetesCaIssuerNotReady")),
					),
				},
			},
		},
		{
			name: "Make Etcd and Front Proxy CA Certificate Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						And(
							ContainSubstring("EtcdCaCertificateNotReady"),
							ContainSubstring("FrontProxyCaCertificateNotReady"),
						),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				makeCertificateReady(certManagerclient, managementClusterClient, hcp, cluster, "etcd-ca")(ctx, g)
				makeCertificateReady(certManagerclient, managementClusterClient, hcp, cluster, "front-proxy-ca")(ctx, g)
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						Not(Or(
							ContainSubstring("EtcdCaCertificateNotReady"),
							ContainSubstring("FrontProxyCaCertificateNotReady"),
						)),
					),
				},
			},
		},
		{
			name: "Make Etcd and Front Proxy CA Issuer Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
						And(
							ContainSubstring("EtcdCaIssuerNotReady"),
							ContainSubstring("FrontProxyCaIssuerNotReady"),
						),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				makeIssuerReady(certManagerclient, cluster, "etcd-ca")(ctx, g)
				makeIssuerReady(certManagerclient, cluster, "front-proxy-ca")(ctx, g)
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.CACertificatesReadyCondition,
					),
				},
			},
		},
	}

	phases = append(phases, []testPhase{
		{
			name: "Assign IP to API Server Service",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerServiceReadyCondition,
						Equal("ApiServerServiceIsWaitingOnItsIp"),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				serviceInterface := managementClusterClient.CoreV1().Services(hcp.Namespace)
				svc, err := serviceInterface.Get(ctx, fmt.Sprintf("s-%s", cluster.Name), metav1.GetOptions{})
				g.Expect(err).To(Succeed())

				serviceApplyConfiguration, err := corev1ac.ExtractService(svc, k8sFieldManager)
				g.Expect(err).To(Succeed())
				g.Expect(serviceInterface.ApplyStatus(ctx,
					serviceApplyConfiguration.WithStatus(
						corev1ac.ServiceStatus().
							WithLoadBalancer(
								corev1ac.LoadBalancerStatus().
									WithIngress(
										corev1ac.LoadBalancerIngress().
											WithIP("1.1.1.1"),
									),
							),
					),
					k8sOptions,
				)).Error().To(Succeed())
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.APIServerServiceReadyCondition,
					),
				},
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(hcp.Status.LegacyIP).To(Equal("1.1.1.1"))
			},
		},
		{
			name: "Sync InfraCluster Control Plane Endpoint to Cluster",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.SyncControlPlaneEndpointReadyCondition,
						Equal("ControlPlaneEndpointIsNotYetSet"),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				endpoint, found, err := unstructured.NestedMap(infraCluster.Object, "spec", "controlPlaneEndpoint")
				g.Expect(err).To(Succeed())
				g.Expect(found).To(BeTrue())
				g.Expect(endpoint).To(And(
					HaveKeyWithValue("host", "test-cluster.default.example.com"),
					HaveKeyWithValue("port", int64(443)),
				))
				cluster.Spec.ControlPlaneEndpoint = capiv2.APIEndpoint{
					Host: "test-cluster.default.example.com",
					Port: 443,
				}
				g.Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.SyncControlPlaneEndpointReadyCondition,
					),
				},
			},
		},
	}...)

	phases = append(
		phases,
		slices.MapToSlice(map[string]string{
			"admin":                    "CertificateAdminNotReady",
			"controller-manager":       "CertificateControllerManagerNotReady",
			"scheduler":                "CertificateSchedulerNotReady",
			"konnectivity-client":      "CertificateKonnectivityClientNotReady",
			"control-plane-controller": "CertificateControlPlaneControllerNotReady",
			"apiserver":                "CertificateApiServerNotReady",
			"apiserver-kubelet-client": "CertificateApiServerKubeletClientNotReady",
			"front-proxy":              "CertificateFrontProxyNotReady",
			"service-account":          "CertificateServiceAccountNotReady",
			"etcd-server":              "CertificateEtcdServerNotReady",
			"etcd-peer":                "CertificateEtcdPeerNotReady",
			"etcd-apiserver-client":    "CertificateEtcdApiServerClientNotReady",
		}, func(
			name string, reasonMatcher string,
		) testPhase {
			return testPhase{
				name: fmt.Sprintf("Make %s Certificate Ready", slices.Capitalize(name)),
				verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
					false: {
						NewConditionVerification(
							v1alpha1.CertificatesReadyCondition,
							ContainSubstring(reasonMatcher),
						),
					},
				},
				simulateExternalSystems: makeCertificateReady(
					certManagerclient, managementClusterClient, hcp, cluster, name,
				),
				verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
					false: {
						NewConditionVerification(
							v1alpha1.CertificatesReadyCondition,
							Not(ContainSubstring(reasonMatcher)),
						),
					},
				},
			}
		})...,
	)
	phases = append(phases, []testPhase{
		{
			name: "Make etcd-controller-client Certificate Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.CertificatesReadyCondition,
						ContainSubstring("CertificateEtcdControllerClientNotReady"),
					),
				},
			},
			simulateExternalSystems: makeCertificateReady(
				certManagerclient, managementClusterClient, hcp, cluster, "etcd-controller-client",
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.CertificatesReadyCondition,
					),
				},
			},
		},
		{
			name: "Verify Kubeconfigs Were Created",
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.KubeconfigReadyCondition,
					),
				},
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				secretInterface := managementClusterClient.CoreV1().Secrets(hcp.Namespace)
				for _, name := range []string{
					"admin",
					"kube-controller-manager",
					"kube-scheduler",
					"konnectivity-client",
					"control-plane-controller",
				} {
					kubeconfigName := fmt.Sprintf("%s-%s-kubeconfig", cluster.Name, name)
					if name == "admin" {
						kubeconfigName = fmt.Sprintf("%s-kubeconfig", cluster.Name)
					}
					g.Expect(secretInterface.Get(
						ctx,
						kubeconfigName,
						metav1.GetOptions{},
					)).Error().To(Succeed())
				}
			},
		},
		{
			name: "Make ETCD Client Service Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.EtcdClusterReadyCondition,
						Equal("EtcdClientServiceNotReady"),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				serviceInterface := managementClusterClient.CoreV1().Services(hcp.Namespace)
				service, err := serviceInterface.Get(
					ctx,
					fmt.Sprintf("e-%s-etcd-client", cluster.Name),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())

				serviceApplyConfiguration, err := corev1ac.ExtractService(service, k8sFieldManager)
				g.Expect(err).To(Succeed())
				g.Expect(serviceInterface.Apply(ctx,
					serviceApplyConfiguration.WithSpec(
						corev1ac.ServiceSpec().
							WithClusterIP("2.2.2.2"),
					),
					k8sOptions,
				)).Error().To(Succeed())
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.EtcdClusterReadyCondition,
						Not(Equal("EtcdClientServiceNotReady")),
					),
				},
			},
		},
		{
			name: "Make ETCD Statefulset Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.EtcdClusterReadyCondition,
						Equal("EtcdStatefulSetIsNotReady"),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				statefulSetInterface := managementClusterClient.AppsV1().StatefulSets(hcp.Namespace)
				statefulSet, err := statefulSetInterface.Get(
					ctx,
					fmt.Sprintf("%s-etcd", cluster.Name),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())

				statefulSetApplyConfiguration, err := appsv1ac.ExtractStatefulSet(statefulSet, k8sFieldManager)
				g.Expect(err).To(Succeed())
				g.Expect(statefulSetInterface.ApplyStatus(ctx,
					statefulSetApplyConfiguration.WithStatus(
						appsv1ac.StatefulSetStatus().
							WithReadyReplicas(*statefulSet.Spec.Replicas).
							WithReplicas(*statefulSet.Spec.Replicas).
							WithUpdatedReplicas(*statefulSet.Spec.Replicas).
							WithAvailableReplicas(*statefulSet.Spec.Replicas),
					),
					k8sOptions,
				)).Error().To(Succeed())
			},
			expectError: "statefulset offline",
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(hcp.Status.ETCDVolumeSize.Cmp(resource.MustParse("1Gi"))).To(Equal(0))
				g.Expect(managementClusterClient.NetworkingV1().NetworkPolicies(hcp.Namespace).Get(
					ctx, fmt.Sprintf("%s-etcd", cluster.Name), metav1.GetOptions{},
				)).Error().To(Succeed())
			},
		},
		{
			name: "Make ETCD Cluster Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.EtcdClusterReadyCondition,
						Equal("EtcdClusterFailed"),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				etcdClient.AlarmError = nil
				etcdClient.StatusError = nil
				etcdClient.StatusResponses = map[string]*clientv3.StatusResponse{
					"etcd-0": {
						DbSize: 1024,
					},
					"etcd-1": {
						DbSize: 1024,
					},
					"etcd-2": {
						DbSize: 2048,
					},
				}
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.EtcdClusterReadyCondition,
					),
				},
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(hcp.Status.ETCDVolumeUsage).To(EqualResource(resource.MustParse("2Ki")))
			},
		},
		{
			name: "Verify konnectivity Config",
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(managementClusterClient.CoreV1().ConfigMaps(hcp.Namespace).Get(
					ctx,
					fmt.Sprintf("%s-konnectivity", cluster.Name),
					metav1.GetOptions{},
				)).Error().To(Succeed())
			},
		},
		{
			name: "Verify no audit Config",
			verifyResources: func(ctx context.Context, g *WithT) {
				_, err := managementClusterClient.CoreV1().Secrets(hcp.Namespace).Get(
					ctx,
					fmt.Sprintf("%s-audit", cluster.Name),
					metav1.GetOptions{},
				)
				g.Expect(err).To(MatchError(apierrors.IsNotFound, "IsNotFound"))
			},
		},
		{
			name: "Make API Server Deployment Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
						Equal("ApiServerDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				managementClusterClient,
				hcp.Namespace,
				fmt.Sprintf("%s-%s", cluster.Name, "api-server"),
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
						Not(ContainSubstring("ApiServerDeploymentNotReady")),
					),
				},
			},
		},
		{
			name: "Make Controller Manager Deployment Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
						ContainSubstring("ControllerManagerDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				managementClusterClient,
				hcp.Namespace,
				fmt.Sprintf("%s-%s", cluster.Name, "controller-manager"),
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
						Not(ContainSubstring("ControllerManagerDeploymentNotReady")),
					),
				},
			},
		},
		{
			name: "Make Scheduler Deployment Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
						Equal("SchedulerDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				managementClusterClient,
				hcp.Namespace,
				fmt.Sprintf("%s-%s", cluster.Name, "scheduler"),
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
					),
				},
			},
		},
		{
			name: "Make Api Server TLS Route Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerTLSRoutesReadyCondition,
						Equal("ApiServerTlsRouteNotReady"),
					),
				},
			},
			simulateExternalSystems: makeTLSRouteReady(gatewayInterface, hcp, cluster.Name),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerTLSRoutesReadyCondition,
						Not(Equal("ApiServerTlsRouteNotReady")),
					),
				},
			},
		},
		{
			name: "Make Konnectivity TLS Route Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerTLSRoutesReadyCondition,
						Equal("KonnectivityTlsRouteNotReady"),
					),
				},
			},
			simulateExternalSystems: makeTLSRouteReady(
				gatewayInterface, hcp, fmt.Sprintf("%s-konnectivity", cluster.Name),
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.APIServerTLSRoutesReadyCondition,
					),
				},
			},
		},
		{
			name: "Verify Metadata ConfigMaps exist",
			verifyResources: func(ctx context.Context, g *WithT) {
				coreV1Interface := workloadClusterClient.CoreV1()
				g.Expect(coreV1Interface.ConfigMaps(metav1.NamespacePublic).Get(ctx,
					api.ConfigMapClusterInfo, metav1.GetOptions{}),
				).Error().To(Succeed())
				g.Expect(coreV1Interface.ConfigMaps(metav1.NamespaceSystem).Get(ctx,
					konstants.KubeadmConfigConfigMap, metav1.GetOptions{}),
				).Error().To(Succeed())
				g.Expect(coreV1Interface.ConfigMaps(metav1.NamespaceSystem).Get(ctx,
					konstants.KubeletBaseConfigurationConfigMap, metav1.GetOptions{}),
				).Error().To(Succeed())
			},
		},
		{
			name: "Verify CoreDNS and Konnectivity Deployments are scaled to 0",
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := workloadClusterClient.AppsV1().Deployments(metav1.NamespaceSystem)
				corednsDeployment, err := deploymentInterface.Get(ctx, "coredns", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				g.Expect(corednsDeployment.Spec.Replicas).To(PointTo(Equal(int32(0))))
				konnectivityDeployment, err := deploymentInterface.Get(ctx, "konnectivity-agent", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				g.Expect(konnectivityDeployment.Spec.Replicas).To(PointTo(Equal(int32(0))))
			},
		},
		{
			name: "Add Node to Cluster",
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				g.Expect(workloadClusterClient.CoreV1().Nodes().Create(ctx, &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
				}, metav1.CreateOptions{FieldManager: k8sFieldManager})).Error().To(Succeed())
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
						Equal("KubeProxyDaemonsetNotReady"),
					),
					NewConditionVerification(
						v1alpha1.WorkloadKubeProxyReadyCondition,
						Equal("KubeProxyDaemonsetNotReady"),
					),
				},
			},
		},
		{
			name: "Make Kube Proxy Daemonset Ready",
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				daemonSetInterface := workloadClusterClient.AppsV1().DaemonSets(metav1.NamespaceSystem)
				daemonSet, err := daemonSetInterface.Get(ctx, "kube-proxy", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				daemonSetApplyConfiguration, err := appsv1ac.ExtractDaemonSet(
					daemonSet, k8sFieldManager,
				)
				g.Expect(err).To(Succeed())
				g.Expect(daemonSetInterface.ApplyStatus(
					ctx, daemonSetApplyConfiguration.
						WithStatus(
							appsv1ac.DaemonSetStatus().
								WithNumberAvailable(1).
								WithNumberReady(1).
								WithUpdatedNumberScheduled(daemonSet.Status.UpdatedNumberScheduled).
								WithDesiredNumberScheduled(daemonSet.Status.DesiredNumberScheduled),
						), k8sOptions,
				)).Error().To(Succeed())
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.WorkloadKubeProxyReadyCondition,
					),
				},
			},
		},
		{
			name: "Verify CoreDNS Deployment is scaled to 1",
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := workloadClusterClient.AppsV1().Deployments(metav1.NamespaceSystem)
				corednsDeployment, err := deploymentInterface.Get(ctx, "coredns", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				g.Expect(corednsDeployment.Spec.Replicas).To(PointTo(Equal(int32(1))))
			},
		},
		{
			name: "Make CoreDNS Deployment Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
						Equal("CoreDnsDeploymentNotReady"),
					),
					NewConditionVerification(
						v1alpha1.WorkloadCoreDNSReadyCondition,
						Equal("CoreDnsDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				workloadClusterClient,
				metav1.NamespaceSystem,
				"coredns",
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.WorkloadCoreDNSReadyCondition,
					),
				},
			},
		},
		{
			name: "Verify Konnectivity Agent Deployment is scaled to 1",
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := workloadClusterClient.AppsV1().Deployments(metav1.NamespaceSystem)
				konnectivityDeployment, err := deploymentInterface.Get(ctx, "konnectivity-agent", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				g.Expect(konnectivityDeployment.Spec.Replicas).To(PointTo(Equal(int32(1))))
			},
		},
		{
			name: "Make Konnectivity Agent Deployment Ready",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
						Equal("KonnectivityAgentDeploymentNotReady"),
					),
					NewConditionVerification(
						v1alpha1.WorkloadKonnectivityReadyCondition,
						Equal("KonnectivityAgentDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				workloadClusterClient,
				metav1.NamespaceSystem,
				"konnectivity-agent",
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.WorkloadKonnectivityReadyCondition,
					),
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
					),
				},
			},
		},
		{
			name: "Add 3 Nodes to Cluster",
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				slices.RepeatBy(3, func(i int) bool {
					g.Expect(workloadClusterClient.CoreV1().Nodes().Create(ctx, &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("node-%d", i+2),
						},
					}, metav1.CreateOptions{FieldManager: k8sFieldManager})).Error().To(Succeed())
					return true
				})
			},
		},
		{
			name: "Make Kube Proxy Daemonset Ready After Adding Nodes",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
						Equal("KubeProxyDaemonsetNotReady"),
					),
					NewConditionVerification(
						v1alpha1.WorkloadKubeProxyReadyCondition,
						Equal("KubeProxyDaemonsetNotReady"),
					),
				},
			},
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				daemonSetInterface := workloadClusterClient.AppsV1().DaemonSets(metav1.NamespaceSystem)
				daemonSet, err := daemonSetInterface.Get(ctx, "kube-proxy", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				daemonSetApplyConfiguration, err := appsv1ac.ExtractDaemonSet(
					daemonSet, k8sFieldManager,
				)
				g.Expect(err).To(Succeed())
				g.Expect(daemonSetInterface.ApplyStatus(
					ctx, daemonSetApplyConfiguration.
						WithStatus(
							appsv1ac.DaemonSetStatus().
								WithNumberAvailable(4).
								WithNumberReady(4).
								WithUpdatedNumberScheduled(daemonSet.Status.UpdatedNumberScheduled).
								WithDesiredNumberScheduled(daemonSet.Status.DesiredNumberScheduled),
						), k8sOptions,
				)).Error().To(Succeed())
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.WorkloadKubeProxyReadyCondition,
					),
				},
			},
		},
		{
			name: "Verify CoreDNS Deployment is scaled to 2",
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := workloadClusterClient.AppsV1().Deployments(metav1.NamespaceSystem)
				corednsDeployment, err := deploymentInterface.Get(ctx, "coredns", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				g.Expect(corednsDeployment.Spec.Replicas).To(PointTo(Equal(int32(2))))
			},
		},
		{
			name: "Make CoreDNS Deployment Ready After Scale Up",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
						Equal("CoreDnsDeploymentNotReady"),
					),
					NewConditionVerification(
						v1alpha1.WorkloadCoreDNSReadyCondition,
						Equal("CoreDnsDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				workloadClusterClient,
				metav1.NamespaceSystem,
				"coredns",
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.WorkloadCoreDNSReadyCondition,
					),
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
					),
				},
			},
		},
		{
			name: "Verify Konnectivity Agent Deployment is scaled to 2",
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := workloadClusterClient.AppsV1().Deployments(metav1.NamespaceSystem)
				konnectivityDeployment, err := deploymentInterface.Get(ctx, "konnectivity-agent", metav1.GetOptions{})
				g.Expect(err).To(Succeed())
				g.Expect(konnectivityDeployment.Spec.Replicas).To(PointTo(Equal(int32(2))))
			},
		},
		{
			name: "Make Konnectivity Agent Deployment Ready After Scale Up",
			verifyConditionsBefore: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
						Equal("KonnectivityAgentDeploymentNotReady"),
					),
					NewConditionVerification(
						v1alpha1.WorkloadKonnectivityReadyCondition,
						Equal("KonnectivityAgentDeploymentNotReady"),
					),
				},
			},
			simulateExternalSystems: makeDeploymentReady(
				workloadClusterClient,
				metav1.NamespaceSystem,
				"konnectivity-agent",
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.WorkloadKonnectivityReadyCondition,
					),
					NewConditionVerification(
						v1alpha1.WorkloadClusterResourcesReadyCondition,
					),
				},
			},
		},
		{
			name: "Scale Up to 3 Replicas",
			patchHCP: func() {
				hcp.Spec.Replicas = ptr.To(int32(3))
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := managementClusterClient.AppsV1().Deployments(hcp.Namespace)
				apiServerDeployment, err := deploymentInterface.Get(
					ctx,
					fmt.Sprintf("%s-%s", cluster.Name, "api-server"),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())
				g.Expect(apiServerDeployment.Spec.Replicas).To(PointTo(Equal(int32(3))))
			},
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				false: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
						Equal("ApiServerDeploymentNotReady"),
					),
				},
			},
		},
		{
			name: "Make API Server Deployment Ready After Scale Up",
			simulateExternalSystems: makeDeploymentReady(
				managementClusterClient,
				hcp.Namespace,
				fmt.Sprintf("%s-%s", cluster.Name, "api-server"),
			),
			verifyConditionsAfter: map[bool][]types2.GomegaMatcher{
				true: {
					NewConditionVerification(
						v1alpha1.APIServerDeploymentsReadyCondition,
					),
				},
			},
		},
		{
			name: "Scale Up ETCD storage",
			patchHCP: func() {
				hcp.Spec.ETCD.VolumeSize = ptr.To(resource.MustParse("2Gi"))
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				statefulSet, err := managementClusterClient.AppsV1().StatefulSets(hcp.Namespace).Get(
					ctx,
					fmt.Sprintf("%s-etcd", cluster.Name),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())
				quantity := resource.MustParse("2Gi")
				haveCorrectRequestQuantity := HaveField("Spec.Resources.Requests",
					HaveKeyWithValue(corev1.ResourceName("storage"), EqualResource(quantity)),
				)
				g.Expect(statefulSet.Spec.VolumeClaimTemplates).To(ContainElement(haveCorrectRequestQuantity))
				g.Expect(statefulSet.Spec.Template.ObjectMeta.Annotations).To(HaveKeyWithValue(
					"storage-sizes", quantity.String(),
				))

				selector, err := metav1.LabelSelectorAsSelector(statefulSet.Spec.Selector)
				g.Expect(err).To(Succeed())
				persistentVolumeClaims, err := managementClusterClient.CoreV1().PersistentVolumeClaims(hcp.Namespace).
					List(ctx,
						metav1.ListOptions{
							LabelSelector: selector.String(),
						},
					)
				g.Expect(err).To(Succeed())
				g.Expect(persistentVolumeClaims.Items).To(HaveLen(int(*statefulSet.Spec.Replicas)))
				g.Expect(persistentVolumeClaims.Items).To(HaveEach(haveCorrectRequestQuantity))
			},
		},
		{
			name: "Switch from Custom Etcd volume size to autogrow",
			patchHCP: func() {
				hcp.Spec.ETCD.VolumeSize = nil
				hcp.Spec.ETCD.AutoGrow = ptr.To(true)
			},
		},
		{
			name: "Let Etcd grow",
			simulateExternalSystems: func(ctx context.Context, g *WithT) {
				etcdClient.StatusResponses = map[string]*clientv3.StatusResponse{
					"etcd-0": {
						DbSize: ptr.To(resource.MustParse("1.5Gi")).Value(),
					},
					"etcd-1": {
						DbSize: ptr.To(resource.MustParse("1Gi")).Value(),
					},
					"etcd-2": {
						DbSize: ptr.To(resource.MustParse("500Mi")).Value(),
					},
				}
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(hcp.Status.ETCDVolumeUsage).To(EqualResource(resource.MustParse("1.5Gi")))
			},
		},
		{
			name: "Verify Etcd has been resized",
			verifyResources: func(ctx context.Context, g *WithT) {
				g.Expect(hcp.Status.ETCDVolumeSize).To(EqualResource(resource.MustParse("2Gi")))
			},
		},
		{
			name: "Add Audit Config",
			patchHCP: func() {
				hcp.Spec.Deployment.APIServer.Audit = &v1alpha1.Audit{
					Policy: auditv1.Policy{
						Rules: []auditv1.PolicyRule{
							{
								Level: auditv1.LevelNone,
							},
						},
					},
				}
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := managementClusterClient.AppsV1().Deployments(hcp.Namespace)
				deployment, err := deploymentInterface.Get(
					ctx,
					fmt.Sprintf("%s-%s", cluster.Name, "api-server"),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())
				g.Expect(deployment.Spec.Template.Spec.Containers).To(
					ContainElement(
						And(
							HaveField("Name", "kube-apiserver"),
							HaveField("Args", ContainElement(
								ContainSubstring("--audit-policy-file"),
							)),
						),
					),
				)
			},
		},
		{
			name: "Add single webhook targets to audit config",
			patchHCP: func() {
				hcp.Spec.Deployment.APIServer.Audit.Webhook = &v1alpha1.AuditWebhook{
					Targets: []v1alpha1.AuditWebhookTarget{
						{
							Server: "https://example.com/audit1",
						},
					},
				}
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := managementClusterClient.AppsV1().Deployments(hcp.Namespace)
				deployment, err := deploymentInterface.Get(
					ctx,
					fmt.Sprintf("%s-%s", cluster.Name, "api-server"),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())
				g.Expect(deployment.Spec.Template.Spec.Containers).To(
					ContainElement(
						And(
							HaveField("Name", "kube-apiserver"),
							HaveField("Args", ContainElement(
								ContainSubstring("--audit-webhook-config-file"),
							)),
						),
					),
				)
				g.Expect(deployment.Spec.Template.Spec.InitContainers).To(BeEmpty())
			},
		},
		{
			name: "Add multiple webhook targets to audit config",
			patchHCP: func() {
				hcp.Spec.Deployment.APIServer.Audit.Webhook = &v1alpha1.AuditWebhook{
					Targets: []v1alpha1.AuditWebhookTarget{
						{
							Server: "https://example.com/audit1",
						},
						{
							Server: "https://example.com/audit2",
						},
					},
				}
			},
			verifyResources: func(ctx context.Context, g *WithT) {
				deploymentInterface := managementClusterClient.AppsV1().Deployments(hcp.Namespace)
				deployment, err := deploymentInterface.Get(
					ctx,
					fmt.Sprintf("%s-%s", cluster.Name, "api-server"),
					metav1.GetOptions{},
				)
				g.Expect(err).To(Succeed())
				g.Expect(deployment.Spec.Template.Spec.Containers).To(
					ContainElement(
						And(
							HaveField("Name", "kube-apiserver"),
							HaveField("Args", ContainElement(
								ContainSubstring("--audit-webhook-config-file"),
							)),
						),
					),
				)
				g.Expect(deployment.Spec.Template.Spec.InitContainers).To(
					ContainElement(
						HaveField("Name", "audit-webhook"),
					),
				)
			},
		},
	}...)

	for index, phase := range phases {
		t.Logf("Phase %d: %s", index+1, phase.name)
		if phase.patchHCP != nil {
			oldHcp := hcp.DeepCopy()
			phase.patchHCP()
			g.Expect(hostedControlPlaneWebhook.ValidateUpdate(ctx, oldHcp, hcp)).Error().To(Succeed())
			g.Expect(k8sClient.Update(ctx, hcp)).To(Succeed())
		}

		if phase.patchCluster != nil {
			phase.patchCluster()
			g.Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		}

		if len(phase.verifyConditionsBefore) > 0 {
			t.Log("  Verifying pre-conditions")
			verifyConditions(phase.verifyConditionsBefore, hcp, g)
		}

		if phase.simulateExternalSystems != nil {
			t.Logf("  Simulating external systems")
			phase.simulateExternalSystems(ctx, g)
			simulateK8sAPI(ctx, managementClusterClient, g)
			simulateK8sAPI(ctx, workloadClusterClient, g)
		}

		t.Log("  Reconciling")
		result, err := reconciler.Reconcile(ctx, req)

		if phase.expectError != "" {
			g.Expect(err).To(MatchError(ContainSubstring(phase.expectError)))
		} else {
			g.Expect(err).To(Succeed())
		}

		if phase.expectNoRequeue {
			g.Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
		}

		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(infraCluster), infraCluster)).To(Succeed())

		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())

		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)).To(Succeed())

		if len(phase.verifyConditionsAfter) > 0 {
			t.Log("  Verifying post-conditions")
			verifyConditions(phase.verifyConditionsAfter, hcp, g)
		}

		if phase.verifyResources != nil {
			t.Log("  Verifying resources")
			phase.verifyResources(ctx, g)
		}
	}

	t.Log("Final State Verification")

	g.Expect(slices.Filter(hcp.Status.Conditions, func(condition metav1.Condition, _ int) bool {
		return condition.Type != capiv2.PausedCondition && condition.Status != metav1.ConditionTrue
	})).To(BeEmpty())
	g.Expect(hcp.Status.Ready).To(BeTrue())
	g.Expect(hcp.Status.Initialization.ControlPlaneInitialized).To(PointTo(BeTrue()))

	// Check that major conditions exist
	majorConditions := []string{
		v1alpha1.CACertificatesReadyCondition,
		v1alpha1.CertificatesReadyCondition,
		v1alpha1.APIServerServiceReadyCondition,
		v1alpha1.KubeconfigReadyCondition,
		v1alpha1.EtcdClusterReadyCondition,
	}

	foundConditions := 0
	for _, condType := range majorConditions {
		if conditions.Get(hcp, condType) != nil {
			foundConditions++
		}
	}

	g.Expect(foundConditions).To(BeNumerically(">", 0), "Should have at least some conditions set")
}

func simulateK8sAPI(ctx context.Context, kubernetesClient kubernetes.Interface, g *WithT) {
	namespaces := []string{metav1.NamespaceSystem}
	nodes, err := kubernetesClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	g.Expect(err).To(Succeed())
	allNodesCount := int32(len(nodes.Items))

	daemonSets, err := kubernetesClient.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	g.Expect(err).To(Succeed())
	for _, daemonSet := range daemonSets.Items {
		namespaces = append(namespaces, daemonSet.Namespace)
		g.Expect(daemonSet.Spec.Template.Spec.NodeSelector).
			To(BeEmpty(), "DaemonSet %s has a node selector, cannot scale reliably", daemonSet.Name)
		daemonSetApplyConfiguration, err := appsv1ac.ExtractDaemonSet(&daemonSet, k8sFieldManager)
		g.Expect(err).To(Succeed())
		g.Expect(kubernetesClient.AppsV1().DaemonSets(daemonSet.Namespace).ApplyStatus(ctx,
			daemonSetApplyConfiguration.WithStatus(
				appsv1ac.DaemonSetStatus().
					WithDesiredNumberScheduled(allNodesCount).
					WithNumberAvailable(daemonSet.Status.NumberAvailable).
					WithNumberReady(daemonSet.Status.NumberReady).
					WithUpdatedNumberScheduled(allNodesCount),
			),
			k8sOptions,
		)).Error().To(Succeed())
	}

	statefulSets, err := kubernetesClient.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	g.Expect(err).To(Succeed())
	for _, statefulSet := range statefulSets.Items {
		namespaces = append(namespaces, statefulSet.Namespace)
		for _, persistentVolumeClaimTemplate := range statefulSet.Spec.VolumeClaimTemplates {
			slices.RepeatBy(int(*statefulSet.Spec.Replicas), func(i int) bool {
				g.Expect(kubernetesClient.CoreV1().PersistentVolumeClaims(statefulSet.Namespace).Apply(ctx,
					corev1ac.PersistentVolumeClaim(fmt.Sprintf(
						"%s-%s-%d",
						persistentVolumeClaimTemplate.Name, statefulSet.Name, i,
					), statefulSet.Namespace).
						WithSpec(corev1ac.PersistentVolumeClaimSpec().
							WithResources(
								corev1ac.VolumeResourceRequirements().WithRequests(
									persistentVolumeClaimTemplate.Spec.Resources.Requests,
								),
							),
						).
						WithLabels(slices.Assign(
							persistentVolumeClaimTemplate.Labels,
							statefulSet.Spec.Selector.MatchLabels,
						)),
					k8sOptions,
				)).Error().To(Succeed())
				return true
			})
		}
	}

	for _, namespace := range namespaces {
		g.Expect(kubernetesClient.CoreV1().ConfigMaps(namespace).Apply(ctx,
			corev1ac.ConfigMap(rootcacertpublisher.RootCACertConfigMapName, namespace).
				WithData(map[string]string{
					konstants.CACertName: "fake-root-ca-cert",
				}),
			k8sOptions,
		)).Error().To(Succeed())
	}
}

func verifyConditions(after map[bool][]types2.GomegaMatcher, hcp *v1alpha1.HostedControlPlane, g *WithT) {
	for status, matchers := range after {
		conditionStatus := slices.Ternary(status, metav1.ConditionTrue, metav1.ConditionFalse)
		partitionedConditions := slices.GroupBy(hcp.Status.Conditions,
			func(condition metav1.Condition) bool {
				return condition.Status == conditionStatus
			})
		switch len(partitionedConditions) {
		case 0:
			g.Expect(matchers).To(BeEmpty())
			continue
		case 1:
			g.Expect(partitionedConditions[true]).To(ContainElements(matchers))
			continue
		case 2:
			if len(partitionedConditions[true]) < len(partitionedConditions[false]) {
				g.Expect(partitionedConditions[true]).To(ContainElements(matchers))
			} else {
				g.Expect(partitionedConditions[false]).ToNot(ContainElements(matchers))
			}
		}
	}
}

func makeTLSRouteReady(
	gatewayInterface *gwfake.Clientset,
	hcp *v1alpha1.HostedControlPlane,
	name string,
) func(ctx context.Context, g *WithT) {
	return func(ctx context.Context, g *WithT) {
		tlsRouteInterface := gatewayInterface.GatewayV1alpha2().TLSRoutes(hcp.Namespace)
		tlsRoute, err := tlsRouteInterface.Get(ctx, name, metav1.GetOptions{})
		g.Expect(err).To(Succeed())

		tlsRouteApplyConfiguration, err := v1alpha2.ExtractTLSRoute(tlsRoute, gatewayFieldManager)
		g.Expect(err).To(Succeed())
		g.Expect(tlsRouteInterface.ApplyStatus(ctx,
			tlsRouteApplyConfiguration.WithStatus(
				v1alpha2.TLSRouteStatus().
					WithParents(
						v1.RouteParentStatus().
							WithParentRef(
								v1.ParentReference().
									WithNamespace(gwv1.Namespace(hcp.Spec.Gateway.Namespace)).
									WithName(gwv1.ObjectName(hcp.Spec.Gateway.Name)),
							).
							WithConditions(
								v2.Condition().
									WithType(string(gwv1.RouteConditionAccepted)).
									WithStatus(metav1.ConditionTrue),
							),
					),
			),
			gatewayOptions,
		)).Error().To(Succeed())
	}
}

func makeDeploymentReady(
	kubernetesInterface kubernetes.Interface,
	namespace string,
	name string,
) func(ctx context.Context, g *WithT) {
	return func(ctx context.Context, g *WithT) {
		deploymentInterface := kubernetesInterface.AppsV1().Deployments(namespace)
		deployment, err := deploymentInterface.Get(ctx, name, metav1.GetOptions{})
		g.Expect(err).To(Succeed())

		deploymentApplyConfiguration, err := appsv1ac.ExtractDeployment(deployment, k8sFieldManager)
		g.Expect(err).To(Succeed())
		g.Expect(deploymentInterface.ApplyStatus(ctx,
			deploymentApplyConfiguration.WithStatus(
				appsv1ac.DeploymentStatus().
					WithReadyReplicas(*deployment.Spec.Replicas).
					WithReplicas(*deployment.Spec.Replicas).
					WithUpdatedReplicas(*deployment.Spec.Replicas).
					WithAvailableReplicas(*deployment.Spec.Replicas),
			),
			k8sOptions,
		)).Error().To(Succeed())
	}
}

func makeCertificateReady(
	certManagerClient *certmanagerfake.Clientset,
	managementClusterClient *alias.ManagementClusterClient,
	hcp *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	name string,
) func(ctx context.Context, g *WithT) {
	return func(ctx context.Context, g *WithT) {
		certificatesInterface := certManagerClient.CertmanagerV1().Certificates(cluster.Namespace)
		secretInterface := managementClusterClient.CoreV1().Secrets(hcp.Namespace)
		certName := fmt.Sprintf("%s-%s", cluster.Name, name)
		cert, err := certificatesInterface.Get(ctx, certName, metav1.GetOptions{})
		g.Expect(err).To(Succeed())
		certificateApplyConfiguration, err := certmanagerv1ac.ExtractCertificate(
			cert,
			certManagerFieldManager,
		)
		g.Expect(err).To(Succeed())
		g.Expect(certificatesInterface.ApplyStatus(ctx, certificateApplyConfiguration.WithStatus(
			certmanagerv1ac.CertificateStatus().
				WithConditions(
					certmanagerv1ac.CertificateCondition().
						WithType(certmanagerv1.CertificateConditionReady).
						WithStatus("True").
						WithReason("Ready"),
				),
		), certManagerOptions)).Error().To(Succeed())

		g.Expect(secretInterface.Apply(ctx,
			corev1ac.Secret(cert.Spec.SecretName, hcp.Namespace).
				WithData(
					map[string][]byte{
						"tls.crt": []byte(fmt.Sprintf("fake-%s-cert", name)),
						"tls.key": []byte(fmt.Sprintf("fake-%s-key", name)),
						"ca.crt":  []byte(fmt.Sprintf("fake-%s-cert", name)),
					},
				),
			certManagerOptions,
		)).Error().To(Succeed())
	}
}

func createInfraClusterCRD(group string, version string, kind string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: contract.CalculateCRDName(group, kind),
			Labels: map[string]string{
				"cluster.x-k8s.io/v1beta2": version,
			},
		},
	}
}

func makeIssuerReady(
	certManagerClient *certmanagerfake.Clientset,
	cluster *capiv2.Cluster,
	name string,
) func(ctx context.Context, g *WithT) {
	return func(ctx context.Context, g *WithT) {
		issuersInterface := certManagerClient.CertmanagerV1().Issuers(cluster.Namespace)
		issuerName := fmt.Sprintf("%s-%s", cluster.Name, name)
		issuer, err := issuersInterface.Get(ctx, issuerName, metav1.GetOptions{})
		g.Expect(err).To(Succeed())
		issuerApplyConfiguration, err := certmanagerv1ac.ExtractIssuer(issuer, certManagerFieldManager)
		g.Expect(err).To(Succeed())
		g.Expect(issuersInterface.ApplyStatus(ctx, issuerApplyConfiguration.WithStatus(
			certmanagerv1ac.IssuerStatus().
				WithConditions(
					certmanagerv1ac.IssuerCondition().
						WithType(certmanagerv1.IssuerConditionReady).
						WithStatus("True").
						WithReason("Ready"),
				),
		), certManagerOptions)).Error().To(Succeed())
	}
}
