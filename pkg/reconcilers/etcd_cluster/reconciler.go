package etcd_cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/version"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var errETCDCAFailedToAppend = errors.New("failed to append etcd CA certificate")

type EtcdClusterReconciler interface {
	ReconcileEtcdCluster(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) (string, error)
}

func NewEtcdClusterReconciler(
	kubernetesClient kubernetes.Interface,
	recorder record.EventRecorder,
	etcdServerPort int32,
	etcdServerStorageBuffer resource.Quantity,
	etcdServerStorageIncrement resource.Quantity,
	componentLabel string,
	apiServerComponentLabel string,
) EtcdClusterReconciler {
	return &etcdClusterReconciler{
		ManagementResourceReconciler: reconcilers.ManagementResourceReconciler{
			Tracer:           tracing.GetTracer("EtcdCluster"),
			KubernetesClient: kubernetesClient,
		},
		recorder:                   recorder,
		etcdServerPort:             etcdServerPort,
		etcdPeerPort:               2380,
		etcdServerStorageBuffer:    etcdServerStorageBuffer,
		etcdServerStorageIncrement: etcdServerStorageIncrement,
		componentLabel:             componentLabel,
		apiServerComponentLabel:    apiServerComponentLabel,
	}
}

type etcdClusterReconciler struct {
	reconcilers.ManagementResourceReconciler
	recorder                   record.EventRecorder
	etcdServerPort             int32
	etcdPeerPort               int32
	etcdServerStorageBuffer    resource.Quantity
	etcdServerStorageIncrement resource.Quantity
	componentLabel             string
	apiServerComponentLabel    string
}

var _ EtcdClusterReconciler = &etcdClusterReconciler{}

func (er *etcdClusterReconciler) ReconcileEtcdCluster(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (string, error) {
	return tracing.WithSpan(ctx, er.Tracer, "ReconcileEtcdCluster",
		func(ctx context.Context, span trace.Span) (string, error) {
			span.SetAttributes(
				attribute.String("etcd.volume.size", hostedControlPlane.Status.ETCDVolumeSize.String()),
				attribute.Bool("etcd.auto.grow", hostedControlPlane.Spec.ETCD.AutoGrow),
				attribute.String("etcd.volume.usage", hostedControlPlane.Status.ETCDVolumeUsage.String()),
			)
			serverPort := corev1ac.ContainerPort().
				WithName("server").
				WithContainerPort(er.etcdServerPort).
				WithProtocol(corev1.ProtocolTCP)

			peerPort := corev1ac.ContainerPort().
				WithName("peer").
				WithContainerPort(2380).
				WithProtocol(corev1.ProtocolTCP)

			metricsPort := corev1ac.ContainerPort().
				WithName("metrics").
				WithContainerPort(2381).
				WithProtocol(corev1.ProtocolTCP)

			if notReadyReason, err := er.reconcileService(ctx,
				hostedControlPlane, cluster,
				names.GetEtcdServiceName(cluster),
				true,
				serverPort, peerPort, metricsPort,
			); err != nil {
				return "", fmt.Errorf("failed to reconcile etcd service: %w", err)
			} else if notReadyReason != "" {
				return notReadyReason, nil
			}

			if notReadyReason, err := er.reconcileService(ctx,
				hostedControlPlane, cluster,
				names.GetEtcdClientServiceName(cluster),
				false,
				serverPort, peerPort, metricsPort,
			); err != nil {
				return "", fmt.Errorf("failed to reconcile etcd client service: %w", err)
			} else if notReadyReason != "" {
				return notReadyReason, nil
			}

			hostedControlPlane.Status.ETCDVolumeSize = er.getETCDVolumeSize(hostedControlPlane)

			if err := er.reconcilePVCSizes(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to reconcile size of etcd PVCs: %w", err)
			}

			if ready, err := er.reconcileStatefulSet(
				ctx, hostedControlPlane, cluster, serverPort, peerPort, metricsPort,
			); err != nil {
				return "", fmt.Errorf("failed to reconcile etcd StatefulSet: %w", err)
			} else if !ready {
				return "etcd StatefulSet is not ready", nil
			}

			if err := er.reconcileETCDSpaceUsage(ctx, hostedControlPlane, cluster); err != nil {
				return "", fmt.Errorf("failed to reconcile etcd space usage: %w", err)
			}

			return "", nil
		},
	)
}

//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=patch;list

func (er *etcdClusterReconciler) reconcilePVCSizes(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, er.Tracer, "ReconcilePVCSizes",
		func(ctx context.Context, span trace.Span) error {
			span.SetAttributes(
				attribute.String("etcd.volume.size", hostedControlPlane.Status.ETCDVolumeSize.String()),
				attribute.String("etcd.volume.usage", hostedControlPlane.Status.ETCDVolumeUsage.String()),
			)
			pvcClient := er.KubernetesClient.CoreV1().PersistentVolumeClaims(hostedControlPlane.Namespace)

			pvcList, err := pvcClient.List(ctx, metav1.ListOptions{
				LabelSelector: strings.Join(slices.MapToSlice(
					names.GetControlPlaneSelector(cluster, er.componentLabel).MatchLabels,
					func(key string, value string) string {
						return fmt.Sprintf("%s=%s", key, value)
					},
				), ","),
			})
			if err != nil {
				return fmt.Errorf("failed to list etcd PVCs: %w", err)
			}

			for _, pvc := range pvcList.Items {
				if pvc.Spec.Resources.Requests.Storage().Cmp(hostedControlPlane.Status.ETCDVolumeSize) == -1 {
					pvcWithNewSize := corev1ac.PersistentVolumeClaim(pvc.Name, pvc.Namespace).
						WithSpec(corev1ac.PersistentVolumeClaimSpec().WithResources(
							corev1ac.VolumeResourceRequirements().WithRequests(corev1.ResourceList{
								corev1.ResourceStorage: hostedControlPlane.Status.ETCDVolumeSize,
							})),
						)
					_, err := pvcClient.Apply(ctx, pvcWithNewSize, operatorutil.ApplyOptions)
					if err != nil {
						return fmt.Errorf(
							"failed to apply size change to etcd PVC %s: %w",
							path.Join(pvc.Namespace, pvc.Name),
							err,
						)
					}
					er.recorder.Eventf(
						hostedControlPlane,
						corev1.EventTypeNormal,
						"EtcdVolumeResize",
						"Resized etcd volume %s/%s from %s to %s",
						pvc.Namespace, pvc.Name,
						pvc.Spec.Resources.Requests.Storage().String(),
						hostedControlPlane.Status.ETCDVolumeSize.String(),
					)
				}
			}

			return nil
		},
	)
}

func (er *etcdClusterReconciler) getETCDVolumeSize(hostedControlPlane *v1alpha1.HostedControlPlane) resource.Quantity {
	if hostedControlPlane.Spec.ETCD.AutoGrow {
		value := hostedControlPlane.Status.ETCDVolumeSize.DeepCopy()
		value.Sub(hostedControlPlane.Status.ETCDVolumeUsage)
		if value.Cmp(er.etcdServerStorageBuffer) == -1 {
			newValue := hostedControlPlane.Status.ETCDVolumeSize.DeepCopy()
			newValue.Add(er.etcdServerStorageIncrement)
			er.recorder.Eventf(
				hostedControlPlane,
				corev1.EventTypeNormal,
				"EtcdVolumeAutoResize",
				"Auto-resized etcd volume from %s to %s",
				hostedControlPlane.Status.ETCDVolumeSize.String(),
				newValue.String(),
			)
			return newValue
		}
		return hostedControlPlane.Status.ETCDVolumeSize
	} else {
		return hostedControlPlane.Spec.ETCD.VolumeSize
	}
}

func (er *etcdClusterReconciler) reconcileETCDSpaceUsage(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, er.Tracer, "ReconcileETCDSpaceUsage",
		func(ctx context.Context, span trace.Span) (err error) {
			statuses, err := callETCDFuncOnAllMembers(
				ctx,
				er.KubernetesClient,
				hostedControlPlane,
				cluster,
				er.etcdServerPort,
				clientv3.Client.Status,
			)
			if err != nil {
				return fmt.Errorf("failed to get etcd member statuses: %w", err)
			}

			dbSize := slices.Max(slices.Map(slices.Values(statuses),
				func(status *clientv3.StatusResponse, _ int) int64 {
					return status.DbSize
				},
			))

			dbSizeQuantity := resource.NewQuantity(dbSize, resource.BinarySI)
			if dbSizeQuantity.Cmp(resource.MustParse("1G")) >= 0 {
				dbSizeQuantity.SetScaled(dbSizeQuantity.ScaledValue(resource.Giga), resource.Giga)
			} else {
				dbSizeQuantity.SetScaled(dbSizeQuantity.ScaledValue(resource.Mega), resource.Mega)
			}
			hostedControlPlane.Status.ETCDVolumeUsage = *dbSizeQuantity
			span.SetAttributes(
				attribute.String("etcd.volume.usage", hostedControlPlane.Status.ETCDVolumeUsage.String()),
			)

			return nil
		},
	)
}

func callETCDFuncOnAllMembers[R any](
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	etcdServerPort int32,
	etcdFunc func(client clientv3.Client, ctx context.Context, endpoint string) (*R, error),
) (map[string]*R, error) {
	return tracing.WithSpan(ctx, "EtcdCluster", "CallETCDFuncOnAllMembers",
		func(ctx context.Context, span trace.Span) (map[string]*R, error) {
			caPool, clientCertificate, err := getETCDConnectionTLS(ctx, kubernetesClient, hostedControlPlane, cluster)
			if err != nil {
				return nil, err
			}
			endpoints := slices.MapValues(names.GetEtcdDNSNames(cluster), func(endpoint string, _ string) string {
				return fmt.Sprintf("https://%s", net.JoinHostPort(endpoint, strconv.Itoa(int(etcdServerPort))))
			})
			results := make(map[string]*R, len(endpoints))
			var errs error
			for _, endpoint := range endpoints {
				result, err := callETCDFuncOnMember(ctx, endpoint, caPool, clientCertificate, etcdFunc)
				errs = errors.Join(errs, err)
				results[endpoint] = result
			}

			return results, errs
		},
	)
}

func getETCDConnectionTLS(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) (*x509.CertPool, tls.Certificate, error) {
	return tracing.WithSpan3(ctx, "EtcdCluster", "GetETCDConnectionTLS",
		func(ctx context.Context, span trace.Span) (*x509.CertPool, tls.Certificate, error) {
			secrets := kubernetesClient.CoreV1().Secrets(hostedControlPlane.Namespace)
			etcdCASecret, err := secrets.
				Get(ctx, names.GetEtcdCASecretName(cluster), metav1.GetOptions{})
			if err != nil {
				return nil, tls.Certificate{}, fmt.Errorf("failed to get etcd CA secret: %w", err)
			}

			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(etcdCASecret.Data[corev1.TLSCertKey]) {
				return nil, tls.Certificate{}, errETCDCAFailedToAppend
			}

			clientCertificateSecret, err := secrets.
				Get(ctx, names.GetEtcdControllerClientCertificateSecretName(cluster), metav1.GetOptions{})
			if err != nil {
				return nil, tls.Certificate{}, fmt.Errorf(
					"failed to get etcd API server client certificate secret: %w",
					err,
				)
			}
			clientCertificate, err := tls.X509KeyPair(
				clientCertificateSecret.Data[corev1.TLSCertKey],
				clientCertificateSecret.Data[corev1.TLSPrivateKeyKey],
			)
			if err != nil {
				return nil, tls.Certificate{}, fmt.Errorf("failed to load etcd API server client certificate: %w", err)
			}
			return caPool, clientCertificate, nil
		},
	)
}

func callETCDFuncOnMember[R any](
	ctx context.Context,
	endpoint string,
	caPool *x509.CertPool,
	clientCertificate tls.Certificate,
	etcdFunc func(client clientv3.Client, ctx context.Context, endpoint string) (*R, error),
) (*R, error) {
	return tracing.WithSpan(ctx, "EtcdCluster", "CallETCDFuncOnMember",
		func(ctx context.Context, span trace.Span) (*R, error) {
			span.SetAttributes(
				attribute.String("etcd.endpoint", endpoint),
			)
			etcdConfig := clientv3.Config{
				Endpoints: []string{endpoint},
				Logger:    zap.NewNop(),
				TLS: &tls.Config{
					InsecureSkipVerify: false,
					RootCAs:            caPool,
					Certificates:       []tls.Certificate{clientCertificate},
					MinVersion:         tls.VersionTLS12,
				},
				DialTimeout: 10 * time.Second,
			}
			etcdClient, err := clientv3.New(etcdConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to create etcd client for endpoint %s: %w", endpoint, err)
			}
			defer func() {
				if closeErr := etcdClient.Close(); closeErr != nil {
					err = errors.Join(
						err,
						fmt.Errorf("failed to close etcd client for endpoint %s: %w", endpoint, closeErr),
					)
				}
			}()

			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			return etcdFunc(*etcdClient, ctx, endpoint)
		},
	)
}

func callETCDFuncOnAnyMember[R any](
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	etcdServerPort int32,
	etcdFunc func(client clientv3.Client, ctx context.Context) (*R, error),
) (*R, error) {
	return tracing.WithSpan(ctx, "EtcdCluster", "CallETCDFuncOnAnyMember",
		func(ctx context.Context, span trace.Span) (*R, error) {
			caPool, clientCertificate, err := getETCDConnectionTLS(ctx, kubernetesClient, hostedControlPlane, cluster)
			if err != nil {
				return nil, err
			}

			endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(
				names.GetEtcdClientServiceDNSName(cluster),
				strconv.Itoa(int(etcdServerPort)),
			))

			return callETCDFuncOnMember(ctx, endpoint, caPool, clientCertificate,
				func(client clientv3.Client, ctx context.Context, _ string) (*R, error) {
					return etcdFunc(client, ctx)
				},
			)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=services,verbs=create;patch

func (er *etcdClusterReconciler) reconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	name string,
	headless bool,
	serverPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) (string, error) {
	return tracing.WithSpan(ctx, er.Tracer, "ReconcileEtcdService",
		func(ctx context.Context, span trace.Span) (string, error) {
			span.SetAttributes(
				attribute.String("service.name", name),
				attribute.Bool("service.headless", headless),
			)

			_, ready, err := er.ReconcileService(
				ctx,
				hostedControlPlane,
				cluster,
				hostedControlPlane.Namespace,
				name,
				corev1.ServiceTypeClusterIP,
				headless,
				er.componentLabel,
				[]*corev1ac.ServicePortApplyConfiguration{
					corev1ac.ServicePort().
						WithName("etcd-server").
						WithPort(er.etcdServerPort).
						WithTargetPort(intstr.FromString(*serverPort.Name)).
						WithProtocol(corev1.ProtocolTCP),
					corev1ac.ServicePort().
						WithName("client-peer").
						WithPort(er.etcdPeerPort).
						WithTargetPort(intstr.FromString(*peerPort.Name)).
						WithProtocol(corev1.ProtocolTCP),
					corev1ac.ServicePort().
						WithName("client-metrics").
						WithPort(2381).
						WithTargetPort(intstr.FromString(*metricsPort.Name)).
						WithProtocol(corev1.ProtocolTCP),
				},
			)
			if err != nil {
				return "", err
			} else if !ready {
				return fmt.Sprintf("etcd service %s not ready", name), nil
			}
			return "", nil
		},
	)
}

func (er *etcdClusterReconciler) reconcileStatefulSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	serverPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) (bool, error) {
	return tracing.WithSpan(ctx, er.Tracer, "ReconcileEtcdStatefulSet",
		func(ctx context.Context, span trace.Span) (bool, error) {
			span.SetAttributes(
				attribute.String("etcd.volume.size", hostedControlPlane.Status.ETCDVolumeSize.String()),
				attribute.Int("etcd.replicas", 3),
			)
			etcdCertificatesVolume := er.createEtcdCertificatesVolume(cluster)

			etcdDataVolumeClaimTemplate := er.createEtcdDataVolumeClaimTemplate(hostedControlPlane)
			etcdDataVolume := er.createVolumeFromTemplate(etcdDataVolumeClaimTemplate)

			etcdDataVolumeMount := corev1ac.VolumeMount().
				WithName(*etcdDataVolume.Name).
				WithMountPath("/var/lib/etcd")

			etcdCertificatesVolumeMount := corev1ac.VolumeMount().
				WithName(*etcdCertificatesVolume.Name).
				WithMountPath("/etc/etcd").
				WithReadOnly(true)

			container := er.createEtcdContainer(
				hostedControlPlane, cluster,
				etcdDataVolumeMount, etcdCertificatesVolumeMount,
				serverPort, peerPort, metricsPort,
			)

			if _, ready, err := er.ReconcileStatefulset(
				ctx,
				hostedControlPlane,
				cluster,
				names.GetEtcdStatefulSetName(cluster),
				hostedControlPlane.Namespace,
				reconcilers.PodOptions{
					Annotations: map[string]string{
						"storage-sizes": etcdDataVolumeClaimTemplate.Spec.Resources.Requests.Storage().String(),
					},
					PriorityClassName: hostedControlPlane.Spec.ETCD.PriorityClassName,
				},
				names.GetEtcdServiceName(cluster),
				appsv1.ParallelPodManagement,
				appsv1ac.StatefulSetUpdateStrategy().WithRollingUpdate(
					appsv1ac.RollingUpdateStatefulSetStrategy().WithMaxUnavailable(intstr.FromInt32(1)),
				),
				er.componentLabel,
				map[int32][]string{
					er.etcdServerPort: {er.apiServerComponentLabel},
					er.etcdPeerPort:   {er.componentLabel},
				},
				map[int32][]string{
					er.etcdPeerPort: {er.componentLabel},
				},
				3,
				[]slices.Tuple2[*corev1ac.ContainerApplyConfiguration, reconcilers.ContainerOptions]{
					slices.T2(container, reconcilers.ContainerOptions{}),
				},
				[]*corev1ac.VolumeApplyConfiguration{etcdDataVolume, etcdCertificatesVolume},
				[]*corev1ac.PersistentVolumeClaimApplyConfiguration{etcdDataVolumeClaimTemplate},
				appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicy().
					WithWhenDeleted(appsv1.DeletePersistentVolumeClaimRetentionPolicyType).
					WithWhenScaled(appsv1.RetainPersistentVolumeClaimRetentionPolicyType),
			); err != nil {
				return false, err
			} else if !ready {
				return false, nil
			}

			return true, er.etcdIsHealthy(ctx, hostedControlPlane, cluster)
		},
	)
}

func (er *etcdClusterReconciler) etcdIsHealthy(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	alarmResponse, err := callETCDFuncOnAnyMember(
		ctx,
		er.KubernetesClient,
		hostedControlPlane,
		cluster,
		er.etcdServerPort,
		clientv3.Client.AlarmList,
	)
	if err != nil {
		return err
	}
	if len(alarmResponse.Alarms) > 0 {
		var outOfSpaceAlarms []*etcdserverpb.AlarmMember
		if hostedControlPlane.Spec.ETCD.AutoGrow {
			// Disarm NOSPACE, as we automatically upscale the storage and the alarm is not relevant anymore.
			outOfSpaceAlarms = slices.Filter(alarmResponse.Alarms, func(alarm *etcdserverpb.AlarmMember, _ int) bool {
				return alarm.Alarm == etcdserverpb.AlarmType_NOSPACE
			})
			for _, outdatedAlarm := range outOfSpaceAlarms {
				_, err := callETCDFuncOnAnyMember(
					ctx,
					er.KubernetesClient,
					hostedControlPlane,
					cluster,
					er.etcdServerPort,
					func(client clientv3.Client, ctx context.Context) (*clientv3.AlarmResponse, error) {
						er.recorder.Eventf(
							hostedControlPlane,
							corev1.EventTypeNormal,
							"EtcdAlarmDisarm",
							"Disarmed etcd alarm %s for member %d",
							outdatedAlarm.Alarm.String(),
							outdatedAlarm.MemberID,
						)
						return client.AlarmDisarm(ctx, (*clientv3.AlarmMember)(outdatedAlarm))
					},
				)
				if err != nil {
					return fmt.Errorf("failed to disarm etcd alarm for non-existent member: %w", err)
				}
			}
		}
		activeAlarms, _ := slices.Difference(alarmResponse.Alarms, outOfSpaceAlarms)
		if len(activeAlarms) > 0 {
			return fmt.Errorf("etcd cluster has active alarms: %w", errors.Join(slices.Map(activeAlarms,
				func(alarm *etcdserverpb.AlarmMember, _ int) error {
					//nolint:err113 // we don't get a real error from the API, therefore we create one here
					return fmt.Errorf("etcd member %d has alarm: %w", alarm.MemberID, errors.New(alarm.Alarm.String()))
				},
			)...))
		}
	}
	return nil
}

func (er *etcdClusterReconciler) createVolumeFromTemplate(
	volumeClaimTemplate *corev1ac.PersistentVolumeClaimApplyConfiguration,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().WithName(*volumeClaimTemplate.Name)
}

func (er *etcdClusterReconciler) createEtcdDataVolumeClaimTemplate(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) *corev1ac.PersistentVolumeClaimApplyConfiguration {
	return corev1ac.PersistentVolumeClaim("etcd-data", "").
		WithSpec(corev1ac.PersistentVolumeClaimSpec().
			WithAccessModes(corev1.ReadWriteOnce).
			WithResources(corev1ac.VolumeResourceRequirements().
				WithRequests(corev1.ResourceList{
					corev1.ResourceStorage: hostedControlPlane.Status.ETCDVolumeSize,
				}),
			),
		)
}

func (er *etcdClusterReconciler) createEtcdCertificatesVolume(
	cluster *capiv1.Cluster,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().
		WithName("etcd-certificates").
		WithProjected(corev1ac.ProjectedVolumeSource().
			WithSources(
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetEtcdCASecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath(konstants.CACertName),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetEtcdServerSecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath("server.crt"),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath("server.key"),
					),
				),
				corev1ac.VolumeProjection().WithSecret(corev1ac.SecretProjection().
					WithName(names.GetEtcdPeerSecretName(cluster)).
					WithItems(
						corev1ac.KeyToPath().
							WithKey(corev1.TLSCertKey).
							WithPath("peer.crt"),
						corev1ac.KeyToPath().
							WithKey(corev1.TLSPrivateKeyKey).
							WithPath("peer.key"),
					),
				),
			),
		)
}

func (er *etcdClusterReconciler) createEtcdContainer(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	etcdDataVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	etcdCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	serverPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	return corev1ac.Container().
		WithName("etcd").
		WithImage(fmt.Sprintf("registry.k8s.io/etcd:%s-0", version.Version)).
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("etcd").
		WithArgs(er.buildEtcdArgs(
			hostedControlPlane, cluster,
			etcdDataVolumeMount, etcdCertificatesVolumeMount,
			serverPort, peerPort, metricsPort,
		)...).
		WithEnv(
			corev1ac.EnvVar().
				WithName("POD_NAME").
				WithValueFrom(corev1ac.EnvVarSource().
					WithFieldRef(corev1ac.ObjectFieldSelector().
						WithFieldPath("metadata.name"),
					),
				),
			corev1ac.EnvVar().
				WithName("ETCDCTL_CACERT").
				WithValue(path.Join(*etcdCertificatesVolumeMount.MountPath, konstants.CACertName)),
			corev1ac.EnvVar().
				WithName("ETCDCTL_CERT").
				WithValue(path.Join(*etcdCertificatesVolumeMount.MountPath, "server.crt")),
			corev1ac.EnvVar().
				WithName("ETCDCTL_KEY").
				WithValue(path.Join(*etcdCertificatesVolumeMount.MountPath, "server.key")),
		).
		WithPorts(serverPort, peerPort, metricsPort).
		WithStartupProbe(operatorutil.CreateStartupProbe(metricsPort, "/readyz", corev1.URISchemeHTTP)).
		WithReadinessProbe(operatorutil.CreateReadinessProbe(metricsPort, "/readyz", corev1.URISchemeHTTP)).
		WithLivenessProbe(operatorutil.CreateLivenessProbe(metricsPort, "/livez", corev1.URISchemeHTTP)).
		WithVolumeMounts(etcdDataVolumeMount, etcdCertificatesVolumeMount)
}

func (er *etcdClusterReconciler) buildEtcdArgs(
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	etcdDataVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	etcdCertificatesVolumeMount *corev1ac.VolumeMountApplyConfiguration,
	serverPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	certificatesDir := *etcdCertificatesVolumeMount.MountPath
	podUrl := fmt.Sprintf(
		"https://$(POD_NAME).%s.%s.svc",
		names.GetEtcdServiceName(cluster), hostedControlPlane.Namespace,
	)

	// 90% of the volume size
	storageQuota := hostedControlPlane.Status.ETCDVolumeSize.Value() * 90 / 100

	args := map[string]string{
		"name":                        "$(POD_NAME)",
		"data-dir":                    *etcdDataVolumeMount.MountPath,
		"listen-peer-urls":            fmt.Sprintf("https://0.0.0.0:%d", *peerPort.ContainerPort),
		"listen-client-urls":          fmt.Sprintf("https://0.0.0.0:%d", *serverPort.ContainerPort),
		"advertise-client-urls":       fmt.Sprintf("%s:%d", podUrl, *serverPort.ContainerPort),
		"initial-cluster-state":       "new",
		"initial-cluster-token":       "etcd-cluster",
		"initial-cluster":             er.buildInitialCluster(cluster, peerPort),
		"initial-advertise-peer-urls": fmt.Sprintf("%s:%d", podUrl, *peerPort.ContainerPort),
		"listen-metrics-urls":         fmt.Sprintf("http://0.0.0.0:%d", *metricsPort.ContainerPort),
		"auto-compaction-mode":        "periodic",
		"auto-compaction-retention":   "72h",
		"snapshot-count":              "10000",
		"client-cert-auth":            "true",
		"trusted-ca-file":             path.Join(certificatesDir, konstants.CACertName),
		"cert-file":                   path.Join(certificatesDir, "server.crt"),
		"key-file":                    path.Join(certificatesDir, "server.key"),
		"peer-client-cert-auth":       "true",
		"peer-trusted-ca-file":        path.Join(certificatesDir, konstants.CACertName),
		"peer-cert-file":              path.Join(certificatesDir, "peer.crt"),
		"peer-key-file":               path.Join(certificatesDir, "peer.key"),
		"quota-backend-bytes":         strconv.Itoa(int(storageQuota)),
	}

	return operatorutil.ArgsToSlice(args)
}

func (er *etcdClusterReconciler) buildInitialCluster(
	cluster *capiv1.Cluster,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
) string {
	entries := slices.MapToSlice(names.GetEtcdDNSNames(cluster),
		func(host string, dnsName string) string {
			return fmt.Sprintf("%s=https://%s:%d", host, dnsName, *peerPort.ContainerPort)
		},
	)
	sort.Strings(entries)
	return strings.Join(entries, ",")
}
