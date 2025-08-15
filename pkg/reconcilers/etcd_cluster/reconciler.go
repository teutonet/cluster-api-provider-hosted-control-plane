package etcd_cluster

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type EtcdClusterReconciler interface {
	ReconcileEtcdCluster(
		ctx context.Context,
		hostedControlPlane *v1alpha1.HostedControlPlane,
		cluster *capiv1.Cluster,
	) error
}

func NewEtcdClusterReconciler(
	kubernetesClient kubernetes.Interface,
	etcdClientPort int32,
	componentLabel string,
) EtcdClusterReconciler {
	return &etcdClusterReconciler{
		kubernetesClient: kubernetesClient,
		etcdClientPort:   etcdClientPort,
		componentLabel:   componentLabel,
		tracer:           tracing.GetTracer("EtcdCluster"),
	}
}

type etcdClusterReconciler struct {
	kubernetesClient kubernetes.Interface
	etcdClientPort   int32
	componentLabel   string
	tracer           string
}

var _ EtcdClusterReconciler = &etcdClusterReconciler{}

var (
	errStatefulSetRecreateRequired = fmt.Errorf(
		"recreate required for etcd StatefulSet: %w",
		operatorutil.ErrRequeueRequired,
	)
	errStatefulsetNotReady = fmt.Errorf("etcd StatefulSet is not ready: %w", operatorutil.ErrRequeueRequired)
)

//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=create;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=create;patch

func (er *etcdClusterReconciler) ReconcileEtcdCluster(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, er.tracer, "ReconcileEtcdCluster",
		func(ctx context.Context, span trace.Span) error {
			clientPort := corev1ac.ContainerPort().
				WithName("client").
				WithContainerPort(er.etcdClientPort).
				WithProtocol(corev1.ProtocolTCP)

			peerPort := corev1ac.ContainerPort().
				WithName("peer").
				WithContainerPort(2380).
				WithProtocol(corev1.ProtocolTCP)

			metricsPort := corev1ac.ContainerPort().
				WithName("metrics").
				WithContainerPort(2381).
				WithProtocol(corev1.ProtocolTCP)

			if err := er.reconcileService(ctx,
				hostedControlPlane, cluster,
				names.GetEtcdServiceName(cluster),
				true,
				clientPort, peerPort, metricsPort,
			); err != nil {
				return fmt.Errorf("failed to reconcile etcd service: %w", err)
			}

			if err := er.reconcileService(ctx,
				hostedControlPlane, cluster,
				names.GetEtcdClientServiceName(cluster),
				false,
				clientPort, peerPort, metricsPort,
			); err != nil {
				return fmt.Errorf("failed to reconcile etcd client service: %w", err)
			}

			return errorsUtil.IfErrErrorf("failed to reconcile etcd StatefulSet: %w",
				er.reconcileStatefulSet(ctx, hostedControlPlane, cluster, clientPort, peerPort, metricsPort),
			)
		},
	)
}

func (er *etcdClusterReconciler) reconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	name string,
	headless bool,
	clientPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) error {
	spec := corev1ac.ServiceSpec().
		WithType(corev1.ServiceTypeClusterIP).
		WithSelector(names.GetControlPlaneLabels(cluster, er.componentLabel)).
		WithPorts(
			corev1ac.ServicePort().
				WithName("etcd-client").
				WithPort(er.etcdClientPort).
				WithTargetPort(intstr.FromString(*clientPort.Name)).
				WithProtocol(corev1.ProtocolTCP),
			corev1ac.ServicePort().
				WithName("client-peer").
				WithPort(2380).
				WithTargetPort(intstr.FromString(*peerPort.Name)).
				WithProtocol(corev1.ProtocolTCP),
			corev1ac.ServicePort().
				WithName("client-metrics").
				WithPort(2381).
				WithTargetPort(intstr.FromString(*metricsPort.Name)).
				WithProtocol(corev1.ProtocolTCP),
		)

	if headless {
		spec = spec.WithClusterIP(corev1.ClusterIPNone).WithPublishNotReadyAddresses(true)
	}

	service := corev1ac.Service(name, hostedControlPlane.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, er.componentLabel)).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(spec)

	_, err := er.kubernetesClient.CoreV1().Services(hostedControlPlane.Namespace).
		Apply(ctx, service, operatorutil.ApplyOptions)

	return errorsUtil.IfErrErrorf("failed to apply etcd service: %w", err)
}

func (er *etcdClusterReconciler) reconcileStatefulSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	clientPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) error {
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
		clientPort, peerPort, metricsPort,
	)
	template := corev1ac.PodTemplateSpec().
		WithLabels(names.GetControlPlaneLabels(cluster, er.componentLabel)).
		WithAnnotations(map[string]string{
			"storage-size": hostedControlPlane.Spec.ETCD.VolumeSize.String(),
		}).
		WithSpec(corev1ac.PodSpec().
			WithTopologySpreadConstraints(
				operatorutil.CreatePodTopologySpreadConstraints(
					names.GetControlPlaneSelector(cluster, er.componentLabel),
				),
			).
			WithContainers(container).
			WithVolumes(etcdDataVolume, etcdCertificatesVolume),
		)

	template, err := operatorutil.SetChecksumAnnotations(ctx, er.kubernetesClient, cluster.Namespace, template)
	if err != nil {
		return fmt.Errorf("failed to set checksum annotations: %w", err)
	}

	statefulSetName := names.GetEtcdStatefulSetName(cluster)
	statefulSet := appsv1ac.StatefulSet(statefulSetName, hostedControlPlane.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, er.componentLabel)).
		WithOwnerReferences(operatorutil.GetOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(appsv1ac.StatefulSetSpec().
			WithServiceName(names.GetEtcdServiceName(cluster)).
			WithReplicas(3).
			WithPodManagementPolicy(appsv1.ParallelPodManagement).
			WithUpdateStrategy(appsv1ac.StatefulSetUpdateStrategy().WithRollingUpdate(
				appsv1ac.RollingUpdateStatefulSetStrategy().WithMaxUnavailable(intstr.FromInt32(1)),
			)).
			WithSelector(names.GetControlPlaneSelector(cluster, er.componentLabel)).
			WithTemplate(template).
			WithVolumeClaimTemplates(etcdDataVolumeClaimTemplate).
			WithPersistentVolumeClaimRetentionPolicy(appsv1ac.StatefulSetPersistentVolumeClaimRetentionPolicy().
				WithWhenDeleted(appsv1.DeletePersistentVolumeClaimRetentionPolicyType),
			),
		)

	statefulSetObj, err := er.kubernetesClient.AppsV1().StatefulSets(hostedControlPlane.Namespace).
		Apply(ctx, statefulSet, operatorutil.ApplyOptions)

	if apierrors.IsInvalid(err) {
		if err := er.kubernetesClient.AppsV1().StatefulSets(hostedControlPlane.Namespace).Delete(ctx,
			statefulSetName,
			metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationOrphan)},
		); err != nil {
			return fmt.Errorf("failed to delete existing etcd StatefulSet: %w", err)
		}
		return errStatefulSetRecreateRequired
	}

	if err != nil {
		return errorsUtil.IfErrErrorf("failed to apply etcd StatefulSet: %w", err)
	}

	if statefulSetObj.Status.ReadyReplicas != *statefulSetObj.Spec.Replicas {
		return errStatefulsetNotReady
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
					corev1.ResourceStorage: hostedControlPlane.Spec.ETCD.VolumeSize,
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
	clientPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ContainerApplyConfiguration {
	return corev1ac.Container().
		WithName("etcd").
		WithImage("registry.k8s.io/etcd:3.5.21-0").
		WithImagePullPolicy(corev1.PullAlways).
		WithCommand("etcd").
		WithArgs(er.buildEtcdArgs(
			hostedControlPlane, cluster,
			etcdDataVolumeMount, etcdCertificatesVolumeMount,
			clientPort, peerPort, metricsPort,
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
				WithName("ETCDCTL_API").
				WithValue("3"),
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
		WithPorts(clientPort, peerPort, metricsPort).
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
	clientPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) []string {
	certificatesDir := *etcdCertificatesVolumeMount.MountPath
	podUrl := fmt.Sprintf(
		"https://$(POD_NAME).%s.%s.svc",
		names.GetEtcdServiceName(cluster), hostedControlPlane.Namespace,
	)

	args := map[string]string{
		"name":                        "$(POD_NAME)",
		"data-dir":                    *etcdDataVolumeMount.MountPath,
		"listen-peer-urls":            fmt.Sprintf("https://0.0.0.0:%d", *peerPort.ContainerPort),
		"listen-client-urls":          fmt.Sprintf("https://0.0.0.0:%d", *clientPort.ContainerPort),
		"advertise-client-urls":       fmt.Sprintf("%s:%d", podUrl, *clientPort.ContainerPort),
		"initial-cluster-state":       "new",
		"initial-cluster-token":       "etcd-cluster",
		"initial-cluster":             er.buildInitialCluster(cluster, peerPort),
		"initial-advertise-peer-urls": fmt.Sprintf("%s:%d", podUrl, *peerPort.ContainerPort),
		"listen-metrics-urls":         fmt.Sprintf("http://0.0.0.0:%d", *metricsPort.ContainerPort),
		"auto-compaction-mode":        "revision",
		"auto-compaction-retention":   "1000",
		"client-cert-auth":            "true",
		"trusted-ca-file":             path.Join(certificatesDir, konstants.CACertName),
		"cert-file":                   path.Join(certificatesDir, "server.crt"),
		"key-file":                    path.Join(certificatesDir, "server.key"),
		"peer-client-cert-auth":       "true",
		"peer-trusted-ca-file":        path.Join(certificatesDir, konstants.CACertName),
		"peer-cert-file":              path.Join(certificatesDir, "peer.crt"),
		"peer-key-file":               path.Join(certificatesDir, "peer.key"),
		"quota-backend-bytes":         "8589934592",
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
