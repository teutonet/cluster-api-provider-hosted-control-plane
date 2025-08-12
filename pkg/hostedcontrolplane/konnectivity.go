package hostedcontrolplane

import (
	"bytes"
	"context"
	"fmt"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;update;patch

func (r *HostedControlPlaneReconciler) reconcileKonnectivityConfig(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileKonnectivityConfig",
		func(ctx context.Context, span trace.Span) error {
			egressSelectorConfig := &v1beta1.EgressSelectorConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.k8s.io/v1beta1",
					Kind:       "EgressSelectorConfiguration",
				},
				EgressSelections: []v1beta1.EgressSelection{
					{
						Name: "cluster",
						Connection: v1beta1.Connection{
							ProxyProtocol: v1beta1.ProtocolGRPC,
							Transport: &v1beta1.Transport{
								UDS: &v1beta1.UDSTransport{
									UDSName: "/run/konnectivity/konnectivity-server.sock",
								},
							},
						},
					},
				},
			}

			buf, err := ToYaml(egressSelectorConfig)
			if err != nil {
				return err
			}

			configMap := corev1ac.ConfigMap(
				names.GetKonnectivityConfigMapName(cluster),
				hostedControlPlane.Namespace,
			).
				WithLabels(names.GetControlPlaneLabels(cluster, "")).
				WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
				WithData(map[string]string{
					EgressSelectorConfigurationFileName: buf.String(),
				})

			_, err = r.KubernetesClient.CoreV1().ConfigMaps(hostedControlPlane.Namespace).
				Apply(ctx, configMap, applyOptions)
			return errorsUtil.IfErrErrorf("failed to patch konnectivity configmap: %w", err)
		},
	)
}

func ToYaml(obj runtime.Object) (*bytes.Buffer, error) {
	scheme := runtime.NewScheme()
	encoder := json.NewSerializerWithOptions(json.SimpleMetaFactory{}, scheme, scheme, json.SerializerOptions{
		Yaml:   true,
		Pretty: true,
		Strict: false,
	})

	buf := bytes.NewBuffer([]byte{})
	if err := encoder.Encode(obj, buf); err != nil {
		return nil, fmt.Errorf("failed to encode egress selector config: %w", err)
	}
	return buf, nil
}
