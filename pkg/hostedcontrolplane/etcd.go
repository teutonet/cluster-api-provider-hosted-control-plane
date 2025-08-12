package hostedcontrolplane

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
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsacv1 "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type EtcdClusterReconciler struct {
	client           client.Client
	kubernetesClient kubernetes.Interface
}

var (
	ErrStatefulSetRecreateRequired = fmt.Errorf("recreate required for etcd StatefulSet: %w", ErrRequeueRequired)
	ErrStatefulsetNotReady         = fmt.Errorf("etcd StatefulSet is not ready: %w", ErrRequeueRequired)
)

var ComponentETCD = "etcd"

//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=create;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=create;patch

func (er *EtcdClusterReconciler) ReconcileEtcdCluster(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
) error {
	return tracing.WithSpan1(ctx, hostedControlPlaneReconcilerTracer, "ReconcileEtcdCluster",
		func(ctx context.Context, span trace.Span) error {
			clientPort := corev1ac.ContainerPort().
				WithName("client").
				WithContainerPort(2379).
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
				clientPort, peerPort, metricsPort,
			); err != nil {
				return fmt.Errorf("failed to reconcile etcd service: %w", err)
			}

			return errorsUtil.IfErrErrorf("failed to reconcile etcd StatefulSet: %w",
				er.reconcileStatefulSet(ctx, hostedControlPlane, cluster, clientPort, peerPort, metricsPort),
			)
		},
	)
}

func (er *EtcdClusterReconciler) reconcileService(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	clientPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) error {
	service := corev1ac.Service(names.GetEtcdServiceName(cluster), hostedControlPlane.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, ComponentETCD)).
		WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithSpec(corev1ac.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithPublishNotReadyAddresses(true).
			WithPorts(
				corev1ac.ServicePort().
					WithName("etcd-client").
					WithPort(2379).
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
			).
			WithSelector(names.GetControlPlaneLabels(cluster, ComponentETCD)),
		)

	_, err := er.kubernetesClient.CoreV1().Services(hostedControlPlane.Namespace).
		Apply(ctx, service, applyOptions)

	return errorsUtil.IfErrErrorf("failed to apply etcd service: %w", err)
}

func (er *EtcdClusterReconciler) reconcileStatefulSet(
	ctx context.Context,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv1.Cluster,
	clientPort *corev1ac.ContainerPortApplyConfiguration,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
	metricsPort *corev1ac.ContainerPortApplyConfiguration,
) error {
	etcdCertificatesVolume := er.createEtcdCertificatesVolume(cluster)

	etcdDataVolumeClaimTemplate := er.createEtcdDataVolumeClaimTemplate()
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
		WithLabels(names.GetControlPlaneLabels(cluster, ComponentETCD)).
		WithSpec(corev1ac.PodSpec().
			WithTopologySpreadConstraints(
				operatorutil.CreatePodTopologySpreadConstraints(
					names.GetControlPlaneSelector(cluster, ComponentETCD),
				),
			).
			WithContainers(container).
			WithVolumes(etcdDataVolume, etcdCertificatesVolume),
		)

	secretChecksum, err := util.CalculateSecretChecksum(ctx, er.kubernetesClient,
		hostedControlPlane.Namespace,
		extractSecretNames(template.Spec.Volumes),
	)
	if err != nil {
		return fmt.Errorf("failed to calculate etcd secret checksum: %w", err)
	}

	statefulSetName := names.GetEtcdStatefulSetName(cluster)
	statefulSet := appsacv1.StatefulSet(statefulSetName, hostedControlPlane.Namespace).
		WithLabels(names.GetControlPlaneLabels(cluster, ComponentETCD)).
		WithOwnerReferences(getOwnerReferenceApplyConfiguration(hostedControlPlane)).
		WithAnnotations(map[string]string{
			"checksum/secrets": secretChecksum,
		}).
		WithSpec(appsacv1.StatefulSetSpec().
			WithServiceName(names.GetEtcdServiceName(cluster)).
			WithReplicas(3).
			WithPodManagementPolicy(appsv1.ParallelPodManagement).
			WithUpdateStrategy(appsacv1.StatefulSetUpdateStrategy().WithRollingUpdate(
				appsacv1.RollingUpdateStatefulSetStrategy().WithMaxUnavailable(intstr.FromInt32(1)),
			)).
			WithSelector(names.GetControlPlaneSelector(cluster, ComponentETCD)).
			WithTemplate(template.WithAnnotations(map[string]string{
				"checksum/secrets": secretChecksum,
			})).
			WithVolumeClaimTemplates(etcdDataVolumeClaimTemplate).
			WithPersistentVolumeClaimRetentionPolicy(appsacv1.StatefulSetPersistentVolumeClaimRetentionPolicy().
				WithWhenDeleted(appsv1.DeletePersistentVolumeClaimRetentionPolicyType),
			),
		)

	statefulSetObj, err := er.kubernetesClient.AppsV1().StatefulSets(hostedControlPlane.Namespace).
		Apply(ctx, statefulSet, applyOptions)

	if apierrors.IsInvalid(err) {
		if err := er.kubernetesClient.AppsV1().StatefulSets(hostedControlPlane.Namespace).Delete(ctx,
			statefulSetName,
			metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationOrphan)},
		); err != nil {
			return fmt.Errorf("failed to delete existing etcd StatefulSet: %w", err)
		}
		return ErrStatefulSetRecreateRequired
	}

	if err != nil {
		return errorsUtil.IfErrErrorf("failed to apply etcd StatefulSet: %w", err)
	}

	if statefulSetObj.Status.ReadyReplicas != *statefulSetObj.Spec.Replicas {
		return ErrStatefulsetNotReady
	}

	return nil
}

func (er *EtcdClusterReconciler) createVolumeFromTemplate(
	volumeClaimTemplate *corev1ac.PersistentVolumeClaimApplyConfiguration,
) *corev1ac.VolumeApplyConfiguration {
	return corev1ac.Volume().WithName(*volumeClaimTemplate.Name)
}

func (er *EtcdClusterReconciler) createEtcdDataVolumeClaimTemplate() *corev1ac.PersistentVolumeClaimApplyConfiguration {
	return corev1ac.PersistentVolumeClaim("etcd-data", "").
		WithSpec(corev1ac.PersistentVolumeClaimSpec().
			WithAccessModes(corev1.ReadWriteOnce).
			WithResources(corev1ac.VolumeResourceRequirements().
				WithRequests(corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("8Gi"),
				}),
			),
		)
}

func (er *EtcdClusterReconciler) createEtcdCertificatesVolume(
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
							WithPath("ca.crt"),
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

func (er *EtcdClusterReconciler) createEtcdContainer(
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
				WithValue(path.Join(*etcdCertificatesVolumeMount.MountPath, "ca.crt")),
			corev1ac.EnvVar().
				WithName("ETCDCTL_CERT").
				WithValue(path.Join(*etcdCertificatesVolumeMount.MountPath, "server.crt")),
			corev1ac.EnvVar().
				WithName("ETCDCTL_KEY").
				WithValue(path.Join(*etcdCertificatesVolumeMount.MountPath, "server.key")),
		).
		WithPorts(clientPort, peerPort, metricsPort).
		WithStartupProbe(er.createStartupProbe(metricsPort)).
		WithReadinessProbe(er.createReadinessProbe(metricsPort)).
		WithLivenessProbe(er.createLivenessProbe(metricsPort)).
		WithVolumeMounts(etcdDataVolumeMount, etcdCertificatesVolumeMount)
}

func (er *EtcdClusterReconciler) buildEtcdArgs(
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
		"trusted-ca-file":             path.Join(certificatesDir, "ca.crt"),
		"cert-file":                   path.Join(certificatesDir, "server.crt"),
		"key-file":                    path.Join(certificatesDir, "server.key"),
		"peer-client-cert-auth":       "true",
		"peer-trusted-ca-file":        path.Join(certificatesDir, "ca.crt"),
		"peer-cert-file":              path.Join(certificatesDir, "peer.crt"),
		"peer-key-file":               path.Join(certificatesDir, "peer.key"),
		"quota-backend-bytes":         "8589934592",
	}

	return operatorutil.ArgsToSlice(args)
}

func (er *EtcdClusterReconciler) buildInitialCluster(
	cluster *capiv1.Cluster,
	peerPort *corev1ac.ContainerPortApplyConfiguration,
) string {
	entries := slices.MapToSlice(names.GetEtcdDNSNames(cluster),
		func(host string, serviceSuffix string) string {
			return fmt.Sprintf("%s=https://%s:%d", host, serviceSuffix, *peerPort.ContainerPort)
		},
	)
	sort.Strings(entries)
	return strings.Join(entries, ",")
}

func (er *EtcdClusterReconciler) createStartupProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return er.createProbe("/readyz", probePort).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(10).
		WithFailureThreshold(30).
		WithPeriodSeconds(3)
}

func (er *EtcdClusterReconciler) createReadinessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return er.createProbe("/readyz", probePort).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(3).
		WithPeriodSeconds(5)
}

func (er *EtcdClusterReconciler) createLivenessProbe(
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return er.createProbe("/livez", probePort).
		WithInitialDelaySeconds(0).
		WithTimeoutSeconds(15).
		WithFailureThreshold(8).
		WithPeriodSeconds(10)
}

func (er *EtcdClusterReconciler) createProbe(
	path string,
	probePort *corev1ac.ContainerPortApplyConfiguration,
) *corev1ac.ProbeApplyConfiguration {
	return corev1ac.Probe().WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath(path).
		WithPort(intstr.FromString(*probePort.Name)).
		WithScheme(corev1.URISchemeHTTP),
	)
}
