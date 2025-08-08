package hostedcontrolplane

import (
	"context"
	"fmt"

	etcdv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/api/v1alpha1"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type EtcdClusterReconciler struct {
	client           client.Client
	kubernetesClient kubernetes.Interface
}

//+kubebuilder:rbac:groups=druid.gardener.cloud,resources=etcds,verbs=create;update

func (er *EtcdClusterReconciler) ReconcileEtcdCluster(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileEtcdCluster",
		func(ctx context.Context, span trace.Span) error {
			etcdCluster := &etcdv1alpha1.Etcd{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("e-%s", hostedControlPlane.Name),
					Namespace: hostedControlPlane.Namespace,
					Labels:    names.GetLabels(hostedControlPlane.Name),
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         hostedControlPlane.APIVersion,
							Kind:               hostedControlPlane.Kind,
							Name:               hostedControlPlane.Name,
							UID:                hostedControlPlane.UID,
							Controller:         ptr.To(true),
							BlockOwnerDeletion: ptr.To(true),
						},
					},
				},
				Spec: etcdv1alpha1.EtcdSpec{
					Labels:   names.GetLabels(hostedControlPlane.Name),
					Replicas: 3,
					Etcd: etcdv1alpha1.EtcdConfig{
						ClientUrlTLS: &etcdv1alpha1.TLSConfig{
							TLSCASecretRef: etcdv1alpha1.SecretReference{
								SecretReference: corev1.SecretReference{
									Name:      names.GetEtcdCASecretName(hostedControlPlane.Name),
									Namespace: hostedControlPlane.Namespace,
								},
								DataKey: ptr.To(corev1.TLSCertKey),
							},
							ServerTLSSecretRef: corev1.SecretReference{
								Name:      names.GetEtcdServerSecretName(hostedControlPlane.Name),
								Namespace: hostedControlPlane.Namespace,
							},
							ClientTLSSecretRef: corev1.SecretReference{
								Name:      names.GetEtcdAPIServerClientSecretName(hostedControlPlane.Name),
								Namespace: hostedControlPlane.Namespace,
							},
						},
						PeerUrlTLS: &etcdv1alpha1.TLSConfig{
							TLSCASecretRef: etcdv1alpha1.SecretReference{
								SecretReference: corev1.SecretReference{
									Name:      names.GetEtcdCASecretName(hostedControlPlane.Name),
									Namespace: hostedControlPlane.Namespace,
								},
								DataKey: ptr.To(corev1.TLSCertKey),
							},
							ServerTLSSecretRef: corev1.SecretReference{
								Name:      names.GetEtcdPeerSecretName(hostedControlPlane.Name),
								Namespace: hostedControlPlane.Namespace,
							},
							ClientTLSSecretRef: corev1.SecretReference{
								Name:      names.GetEtcdPeerSecretName(hostedControlPlane.Name),
								Namespace: hostedControlPlane.Namespace,
							},
						},
					},
					Backup: etcdv1alpha1.BackupSpec{
						TLS: &etcdv1alpha1.TLSConfig{
							TLSCASecretRef: etcdv1alpha1.SecretReference{
								SecretReference: corev1.SecretReference{
									Name:      names.GetEtcdCASecretName(hostedControlPlane.Name),
									Namespace: hostedControlPlane.Namespace,
								},
								DataKey: ptr.To(corev1.TLSCertKey),
							},
							ServerTLSSecretRef: corev1.SecretReference{
								Name:      names.GetEtcdServerSecretName(hostedControlPlane.Name),
								Namespace: hostedControlPlane.Namespace,
							},
							ClientTLSSecretRef: corev1.SecretReference{
								Name:      names.GetEtcdBackupSecretName(hostedControlPlane.Name),
								Namespace: hostedControlPlane.Namespace,
							},
						},
					},
					StorageCapacity: &[]resource.Quantity{resource.MustParse("8Gi")}[0],
				},
			}

			secretReferences := slices.Flatten(slices.Map([]*etcdv1alpha1.TLSConfig{
				etcdCluster.Spec.Etcd.ClientUrlTLS,
				etcdCluster.Spec.Etcd.PeerUrlTLS,
				etcdCluster.Spec.Backup.TLS,
			},
				func(tlsConfig *etcdv1alpha1.TLSConfig, _ int) []string {
					return []string{
						tlsConfig.TLSCASecretRef.SecretReference.Name,
						tlsConfig.ServerTLSSecretRef.Name,
						tlsConfig.ClientTLSSecretRef.Name,
					}
				}),
			)

			secretChecksum, err := util.CalculateSecretChecksum(ctx, er.kubernetesClient,
				hostedControlPlane.Namespace,
				secretReferences,
			)
			if err != nil {
				return fmt.Errorf("failed to calculate etcd secret checksum: %w", err)
			}
			etcdCluster.Spec.Annotations = map[string]string{
				"checksum/secrets": secretChecksum,
			}

			existingEtcd := &etcdv1alpha1.Etcd{}
			err = er.client.Get(ctx, client.ObjectKey{
				Name:      etcdCluster.Name,
				Namespace: etcdCluster.Namespace,
			}, existingEtcd)

			if err != nil {
				if apierrors.IsNotFound(err) {
					return errorsUtil.IfErrErrorf("failed to create etcd cluster: %w",
						er.client.Create(ctx, etcdCluster),
					)
				} else {
					return errorsUtil.IfErrErrorf("failed to get etcd cluster: %w", err)
				}
			} else {
				etcdCluster.ResourceVersion = existingEtcd.ResourceVersion
				return errorsUtil.IfErrErrorf("failed to update etcd cluster: %w",
					er.client.Update(ctx, etcdCluster),
				)
			}
		},
	)
}
