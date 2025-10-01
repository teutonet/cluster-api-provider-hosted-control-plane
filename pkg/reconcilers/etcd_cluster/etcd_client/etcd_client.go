package etcd_client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	tracer                  = tracing.GetTracer("EtcdClient")
	errETCDCAFailedToAppend = errors.New("failed to append etcd CA certificate")
)

type EtcdClient interface {
	GetStatuses(ctx context.Context) (map[string]*clientv3.StatusResponse, error)
	CreateSnapshot(ctx context.Context) (*clientv3.SnapshotResponse, error)
	ListAlarms(ctx context.Context) (*clientv3.AlarmResponse, error)
	DisarmAlarm(ctx context.Context, alarm *clientv3.AlarmMember) error
}

type etcdClient struct {
	kubernetesClient  kubernetes.Interface
	caPool            *x509.CertPool
	clientCertificate tls.Certificate
	endpoints         map[string]string
	anyEndpoint       string
	serverPort        int32
}

var _ EtcdClient = &etcdClient{}

type EtcdClientFactory = func(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	serverPort int32,
) (EtcdClient, error)

func NewEtcdClient(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	serverPort int32,
) (EtcdClient, error) {
	return tracing.WithSpan(ctx, tracer, "NewEtcdClient",
		func(ctx context.Context, span trace.Span) (EtcdClient, error) {
			caPool, clientCertificate, err := getETCDConnectionTLS(ctx, kubernetesClient, hostedControlPlane, cluster)
			if err != nil {
				return nil, err
			}
			return &etcdClient{
				kubernetesClient:  kubernetesClient,
				caPool:            caPool,
				clientCertificate: clientCertificate,
				endpoints:         names.GetEtcdDNSNames(cluster),
				serverPort:        serverPort,
				anyEndpoint: fmt.Sprintf("https://%s", net.JoinHostPort(
					names.GetEtcdClientServiceDNSName(cluster),
					strconv.Itoa(int(serverPort)),
				)),
			}, nil
		},
	)
}

func (e *etcdClient) GetStatuses(ctx context.Context) (map[string]*clientv3.StatusResponse, error) {
	return tracing.WithSpan(ctx, tracer, "EtcdClient.GetStatuses",
		func(ctx context.Context, span trace.Span) (map[string]*clientv3.StatusResponse, error) {
			return callETCDFuncOnAllMembers(
				ctx,
				e.caPool,
				e.clientCertificate,
				e.endpoints,
				e.serverPort,
				clientv3.Client.Status,
			)
		},
	)
}

func (e *etcdClient) CreateSnapshot(ctx context.Context) (*clientv3.SnapshotResponse, error) {
	return tracing.WithSpan(ctx, tracer, "EtcdClient.CreateSnapshot",
		func(ctx context.Context, span trace.Span) (*clientv3.SnapshotResponse, error) {
			return callETCDFuncOnAnyMember(
				ctx,
				e.caPool,
				e.clientCertificate,
				e.anyEndpoint,
				clientv3.Client.SnapshotWithVersion,
			)
		},
	)
}

func (e *etcdClient) ListAlarms(ctx context.Context) (*clientv3.AlarmResponse, error) {
	return tracing.WithSpan(ctx, tracer, "EtcdClient.ListAlarms",
		func(ctx context.Context, span trace.Span) (*clientv3.AlarmResponse, error) {
			return callETCDFuncOnAnyMember(
				ctx,
				e.caPool,
				e.clientCertificate,
				e.anyEndpoint,
				clientv3.Client.AlarmList,
			)
		},
	)
}

func (e *etcdClient) DisarmAlarm(ctx context.Context, alarm *clientv3.AlarmMember) error {
	return tracing.WithSpan1(ctx, tracer, "EtcdClient.DisarmAlarm",
		func(ctx context.Context, span trace.Span) error {
			_, err := callETCDFuncOnAnyMember(
				ctx,
				e.caPool,
				e.clientCertificate,
				e.anyEndpoint,
				func(client clientv3.Client, ctx context.Context) (*clientv3.AlarmResponse, error) {
					response, err := client.AlarmDisarm(ctx, alarm)
					if err != nil {
						return nil, fmt.Errorf(
							"failed to disarm etcd alarm %s for member %d: %w",
							alarm.Alarm.String(), alarm.MemberID, err,
						)
					}
					return response, nil
				},
			)
			return err
		},
	)
}

func callETCDFuncOnAllMembers[R any](
	ctx context.Context,
	caPool *x509.CertPool,
	clientCertificate tls.Certificate,
	endpoints map[string]string,
	etcdServerPort int32,
	etcdFunc func(client clientv3.Client, ctx context.Context, endpoint string) (*R, error),
) (map[string]*R, error) {
	return tracing.WithSpan(ctx, tracer, "CallETCDFuncOnAllMembers",
		func(ctx context.Context, span trace.Span) (map[string]*R, error) {
			endpointMap := slices.MapValues(endpoints, func(endpoint string, _ string) string {
				return fmt.Sprintf("https://%s", net.JoinHostPort(endpoint, strconv.Itoa(int(etcdServerPort))))
			})
			results := make(map[string]*R, len(endpointMap))
			var errs error
			for _, endpoint := range endpointMap {
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
	cluster *capiv2.Cluster,
) (*x509.CertPool, tls.Certificate, error) {
	return tracing.WithSpan3(ctx, tracer, "GetETCDConnectionTLS",
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
	etcdFunc func(client clientv3.Client, ctx context.Context, endpoint string) (R, error),
) (R, error) {
	return tracing.WithSpan(ctx, tracer, "CallETCDFuncOnMember",
		func(ctx context.Context, span trace.Span) (R, error) {
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
				return *new(R), fmt.Errorf("failed to create etcd client for endpoint %s: %w", endpoint, err)
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

			var result R
			_, _, err = slices.AttemptWithDelay(4, 5*time.Second, func(_ int, _ time.Duration) error {
				var err error
				result, err = etcdFunc(*etcdClient, ctx, endpoint)
				return err
			})

			return result, errorsUtil.IfErrErrorf("failed to call etcd function on endpoint %s: %w", endpoint, err)
		},
	)
}

func callETCDFuncOnAnyMember[R any](
	ctx context.Context,
	caPool *x509.CertPool,
	clientCertificate tls.Certificate,
	endpoint string,
	etcdFunc func(client clientv3.Client, ctx context.Context) (R, error),
) (R, error) {
	return tracing.WithSpan(ctx, tracer, "CallETCDFuncOnAnyMember",
		func(ctx context.Context, span trace.Span) (R, error) {
			return callETCDFuncOnMember(ctx, endpoint, caPool, clientCertificate,
				func(client clientv3.Client, ctx context.Context, _ string) (R, error) {
					return etcdFunc(client, ctx)
				},
			)
		},
	)
}
