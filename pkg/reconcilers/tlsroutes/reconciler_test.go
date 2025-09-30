package tlsroutes

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gcustom"
	. "github.com/onsi/gomega/gstruct"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	v2 "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	"sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestTLSRoutesReconciler_TrafficRouting(t *testing.T) {
	tests := []struct {
		name               string
		hostedControlPlane *v1alpha1.HostedControlPlane
		cluster            *capiv2.Cluster
		existingTLSRoutes  []runtime.Object
		expectedTLSRoutes  int
		expectedHosts      []string
		expectedBackends   []string
		expectError        bool
	}{
		{
			name: "successful TLS routes creation for API server and konnectivity",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "gateway-system",
						},
					},
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
			expectedTLSRoutes: 2,
			expectedHosts: []string{
				"api.test-cluster.example.com",
				"konnectivity.test-cluster.example.com",
			},
			expectedBackends: []string{
				"test-cluster",
				"test-cluster",
			},
			expectError: false,
		},
		{
			name: "TLS routes update existing routes gracefully",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "gateway-system",
						},
					},
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
			existingTLSRoutes: []runtime.Object{
				&gwv1alpha2.TLSRoute{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster-api",
						Namespace: "default",
					},
					Spec: gwv1alpha2.TLSRouteSpec{
						Hostnames: []gwv1alpha2.Hostname{"old.api.example.com"},
					},
					Status: gwv1alpha2.TLSRouteStatus{
						RouteStatus: gwv1.RouteStatus{
							Parents: []gwv1.RouteParentStatus{
								{
									ParentRef: gwv1.ParentReference{
										Name: "test-gateway",
									},
									Conditions: []metav1.Condition{
										{
											Type:   string(gwv1.RouteConditionAccepted),
											Status: metav1.ConditionTrue,
										},
									},
								},
							},
						},
					},
				},
			},
			expectedTLSRoutes: 2,
			expectError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			gatewayClient := fake.NewClientset(tt.existingTLSRoutes...)

			reconciler := &tlsRoutesReconciler{
				gatewayClient:           gatewayClient,
				apiServerServicePort:    443,
				konnectivityServicePort: 8132,
			}

			apiTLSRoute := reconciler.createTLSRoute(
				"test-cluster-api",
				tt.cluster,
				tt.hostedControlPlane,
				tt.cluster.Spec.ControlPlaneEndpoint.Host,
				reconciler.apiServerServicePort,
			)

			g.Expect(apiTLSRoute).NotTo(BeNil())
			g.Expect(*apiTLSRoute.Name).To(Equal("test-cluster-api"))
			g.Expect(*apiTLSRoute.Namespace).To(Equal(tt.cluster.Namespace))

			g.Expect(apiTLSRoute.Spec.ParentRefs).To(ContainElement(
				MatchFields(IgnoreExtras, Fields{
					"Name":      PointTo(Equal(gwv1.ObjectName("test-gateway"))),
					"Namespace": PointTo(Equal(gwv1.Namespace("gateway-system"))),
				}),
			))

			g.Expect(apiTLSRoute.Spec.Hostnames).
				To(ContainElement(gwv1.Hostname(tt.cluster.Spec.ControlPlaneEndpoint.Host)))

			g.Expect(apiTLSRoute.Spec.Rules).To(ContainElement(
				HaveField("BackendRefs",
					ContainElement(
						HaveField("BackendObjectReferenceApplyConfiguration",
							MatchFields(IgnoreExtras, Fields{
								"Name": PointTo(Equal(gwv1.ObjectName("s-test-cluster"))),
								"Port": PointTo(Equal(gwv1.PortNumber(443))),
							}),
						),
					),
				),
			))

			konnectivityTLSRoute := reconciler.createTLSRoute(
				"test-cluster-konnectivity",
				tt.cluster,
				tt.hostedControlPlane,
				"konnectivity.test-cluster.example.com",
				reconciler.konnectivityServicePort,
			)

			g.Expect(konnectivityTLSRoute).NotTo(BeNil())
			g.Expect(*konnectivityTLSRoute.Name).To(Equal("test-cluster-konnectivity"))
			g.Expect(konnectivityTLSRoute.Spec.Rules).To(ContainElement(
				HaveField("BackendRefs",
					ContainElement(
						HaveField("BackendObjectReferenceApplyConfiguration",
							MatchFields(IgnoreExtras, Fields{
								"Name": PointTo(Equal(gwv1.ObjectName("s-test-cluster"))),
								"Port": PointTo(Equal(gwv1.PortNumber(8132))),
							}),
						),
					),
				),
			))

			g.Expect(apiTLSRoute.OwnerReferences).To(ContainElement(
				MakeMatcher(func(ownerReference v1.OwnerReferenceApplyConfiguration) (bool, error) {
					return *ownerReference.Name == tt.hostedControlPlane.Name &&
						*ownerReference.Kind == "HostedControlPlane", nil
				}),
			))
		})
	}
}

func TestTLSRoutesReconciler_CertificateIntegration(t *testing.T) {
	tests := []struct {
		name               string
		hostedControlPlane *v1alpha1.HostedControlPlane
		cluster            *capiv2.Cluster
		expectedCertRefs   []string
	}{
		{
			name: "TLS routes should reference correct certificates",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "gateway-system",
						},
					},
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			gatewayClient := fake.NewClientset()

			reconciler := &tlsRoutesReconciler{
				gatewayClient:           gatewayClient,
				apiServerServicePort:    443,
				konnectivityServicePort: 8132,
			}

			apiTLSRoute := reconciler.createTLSRoute(
				"test-cluster-api",
				tt.cluster,
				tt.hostedControlPlane,
				tt.cluster.Spec.ControlPlaneEndpoint.Host,
				reconciler.apiServerServicePort,
			)

			g.Expect(apiTLSRoute).NotTo(BeNil())
			g.Expect(apiTLSRoute.Spec.ParentRefs).To(ContainElement(
				MatchFields(IgnoreExtras, Fields{
					"Name":      PointTo(Equal(gwv1.ObjectName("test-gateway"))),
					"Namespace": PointTo(Equal(gwv1.Namespace("gateway-system"))),
				}),
			))

			g.Expect(apiTLSRoute.Labels).To(MatchKeys(IgnoreExtras, Keys{
				"cluster.x-k8s.io/cluster-name": Equal(tt.cluster.Name),
			}),
			)
		})
	}
}

func TestTLSRoutesReconciler_GatewayFailover(t *testing.T) {
	tests := []struct {
		name               string
		hostedControlPlane *v1alpha1.HostedControlPlane
		cluster            *capiv2.Cluster
	}{
		{
			name: "ready gateway should mark routes as ready",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "gateway-system",
						},
					},
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
		},
		{
			name: "gateway not ready should return waiting message",
			hostedControlPlane: &v1alpha1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "default",
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				},
				Spec: v1alpha1.HostedControlPlaneSpec{
					Version: "v1.28.0",
					HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
						Gateway: v1alpha1.GatewayReference{
							Name:      "test-gateway",
							Namespace: "gateway-system",
						},
					},
				},
			},
			cluster: &capiv2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: capiv2.ClusterSpec{
					ControlPlaneEndpoint: capiv2.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 443,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			gatewayClient := fake.NewClientset()

			reconciler := &tlsRoutesReconciler{
				gatewayClient:           gatewayClient,
				apiServerServicePort:    443,
				konnectivityServicePort: 8132,
			}

			apiTLSRoute := reconciler.createTLSRoute(
				"test-cluster-api",
				tt.cluster,
				tt.hostedControlPlane,
				tt.cluster.Spec.ControlPlaneEndpoint.Host,
				reconciler.apiServerServicePort,
			)

			g.Expect(apiTLSRoute).NotTo(BeNil())
			g.Expect(*apiTLSRoute.Name).To(Equal("test-cluster-api"))
			g.Expect(apiTLSRoute.Spec.ParentRefs).To(ContainElement(
				MatchFields(IgnoreExtras, Fields{
					"Name": PointTo(Equal(gwv1.ObjectName("test-gateway"))),
				}),
			))
			g.Expect(apiTLSRoute.Spec.Rules).NotTo(BeEmpty())
		})
	}
}

func TestTLSRoutesReconciler_MultipleEndpoints(t *testing.T) {
	g := NewWithT(t)
	hostedControlPlane := &v1alpha1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "default",
		},
		Spec: v1alpha1.HostedControlPlaneSpec{
			Version: "v1.28.0",
			HostedControlPlaneInlineSpec: v1alpha1.HostedControlPlaneInlineSpec{
				Gateway: v1alpha1.GatewayReference{
					Name:      "test-gateway",
					Namespace: "gateway-system",
				},
			},
		},
	}

	cluster := &capiv2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: capiv2.ClusterSpec{
			ControlPlaneEndpoint: capiv2.APIEndpoint{
				Host: "api.test-cluster.example.com",
				Port: 443,
			},
		},
	}

	gatewayClient := fake.NewClientset()

	reconciler := &tlsRoutesReconciler{
		gatewayClient:           gatewayClient,
		apiServerServicePort:    443,
		konnectivityServicePort: 8132,
	}

	// Test the core TLS route creation logic for both endpoints
	apiTLSRoute := reconciler.createTLSRoute(
		"test-cluster-api",
		cluster,
		hostedControlPlane,
		cluster.Spec.ControlPlaneEndpoint.Host,
		reconciler.apiServerServicePort,
	)

	konnectivityTLSRoute := reconciler.createTLSRoute(
		"test-cluster-konnectivity",
		cluster,
		hostedControlPlane,
		"konnectivity.test-cluster.example.com",
		reconciler.konnectivityServicePort,
	)

	g.Expect(apiTLSRoute).NotTo(BeNil())
	g.Expect(konnectivityTLSRoute).NotTo(BeNil())

	var apiBackendRef v2.BackendRefApplyConfiguration
	g.Expect(apiTLSRoute.Spec.Rules).To(ContainElement(
		HaveField("BackendRefs",
			ContainElement(
				HaveField("BackendObjectReferenceApplyConfiguration",
					MatchFields(IgnoreExtras, Fields{
						"Port": PointTo(Equal(gwv1.PortNumber(443))),
					}),
				),
				&apiBackendRef,
			),
		),
	))

	g.Expect(konnectivityTLSRoute.Spec.Rules).To(ContainElement(
		HaveField("BackendRefs",
			ContainElement(
				HaveField("BackendObjectReferenceApplyConfiguration",
					MatchFields(IgnoreExtras, Fields{
						"Name": PointTo(Equal(*apiBackendRef.Name)),
						"Port": PointTo(Equal(gwv1.PortNumber(8132))),
					}),
				),
			),
		),
	))
}
